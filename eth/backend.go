// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package eth implements the Ethereum protocol.
package eth

import (
	"fmt"
	"math/big"
	"regexp"
	"sync"

	"github.com/lab2528/go-oneTime/accounts"
	"github.com/lab2528/go-oneTime/common"
	"github.com/lab2528/go-oneTime/consensus"
	"github.com/lab2528/go-oneTime/consensus/ethash"
	"github.com/lab2528/go-oneTime/core"
	"github.com/lab2528/go-oneTime/core/types"
	"github.com/lab2528/go-oneTime/core/vm"
	"github.com/lab2528/go-oneTime/eth/downloader"
	"github.com/lab2528/go-oneTime/eth/filters"
	"github.com/lab2528/go-oneTime/eth/gasprice"
	"github.com/lab2528/go-oneTime/ethdb"
	"github.com/lab2528/go-oneTime/event"
	"github.com/lab2528/go-oneTime/internal/ethapi"
	"github.com/lab2528/go-oneTime/log"
	"github.com/lab2528/go-oneTime/miner"
	"github.com/lab2528/go-oneTime/node"
	"github.com/lab2528/go-oneTime/p2p"
	"github.com/lab2528/go-oneTime/params"
	"github.com/lab2528/go-oneTime/rpc"
)

var (
	datadirInUseErrnos = map[uint]bool{11: true, 32: true, 35: true}
	portInUseErrRE     = regexp.MustCompile("address already in use")
)

type Config struct {
	// The genesis block, which is inserted if the database is empty.
	// If nil, the Ethereum main net block is used.
	Genesis *core.Genesis

	NetworkId int // Network ID to use for selecting peers to connect to

	FastSync   bool // Enables the state download based fast synchronisation algorithm
	LightMode  bool // Running in light client mode
	LightServ  int  // Maximum percentage of time allowed for serving LES requests
	LightPeers int  // Maximum number of LES client peers
	MaxPeers   int  // Maximum number of global peers

	SkipBcVersionCheck bool // e.g. blockchain export
	DatabaseCache      int
	DatabaseHandles    int

	DocRoot   string
	PowFake   bool
	PowTest   bool
	PowShared bool
	ExtraData []byte

	EthashCacheDir       string
	EthashCachesInMem    int
	EthashCachesOnDisk   int
	EthashDatasetDir     string
	EthashDatasetsInMem  int
	EthashDatasetsOnDisk int

	Etherbase    common.Address
	GasPrice     *big.Int
	MinerThreads int
	SolcPath     string

	GpoBlocks     int
	GpoPercentile int

	EnablePreimageRecording bool
}

type LesServer interface {
	Start(srvr *p2p.Server)
	Stop()
	Protocols() []p2p.Protocol
}

// Ethereum implements the Ethereum full node service.
type Ethereum struct {
	chainConfig *params.ChainConfig
	// Channel for shutting down the service
	shutdownChan  chan bool // Channel for shutting down the ethereum
	stopDbUpgrade func()    // stop chain db sequential key upgrade
	// Handlers
	txPool          *core.TxPool
	txMu            sync.Mutex
	blockchain      *core.BlockChain
	protocolManager *ProtocolManager
	lesServer       LesServer
	// DB interfaces
	chainDb ethdb.Database // Block chain database

	eventMux       *event.TypeMux
	engine         consensus.Engine
	accountManager *accounts.Manager

	ApiBackend *EthApiBackend

	miner        *miner.Miner
	Mining       bool
	MinerThreads int
	etherbase    common.Address
	solcPath     string

	netVersionId  int
	netRPCService *ethapi.PublicNetAPI
}

func (s *Ethereum) AddLesServer(ls LesServer) {
	s.lesServer = ls
	s.protocolManager.lesServer = ls
}

// New creates a new Ethereum object (including the
// initialisation of the common Ethereum object)
func New(ctx *node.ServiceContext, config *Config) (*Ethereum, error) {
	chainDb, err := CreateDB(ctx, config, "chaindata")
	if err != nil {
		return nil, err
	}
	stopDbUpgrade := upgradeSequentialKeys(chainDb)
	chainConfig, genesisHash, genesisErr := core.SetupGenesisBlock(chainDb, config.Genesis)
	if _, ok := genesisErr.(*params.ConfigCompatError); genesisErr != nil && !ok {
		return nil, genesisErr
	}
	log.Info("Initialised chain configuration", "config", chainConfig)

	eth := &Ethereum{
		chainDb:        chainDb,
		chainConfig:    chainConfig,
		eventMux:       ctx.EventMux,
		accountManager: ctx.AccountManager,
		engine:         CreateConsensusEngine(ctx, config, chainConfig, chainDb),
		shutdownChan:   make(chan bool),
		stopDbUpgrade:  stopDbUpgrade,
		netVersionId:   config.NetworkId,
		etherbase:      config.Etherbase,
		MinerThreads:   config.MinerThreads,
		solcPath:       config.SolcPath,
	}

	if err := addMipmapBloomBins(chainDb); err != nil {
		return nil, err
	}
	log.Info("Initialising Ethereum protocol", "versions", ProtocolVersions, "network", config.NetworkId)

	if !config.SkipBcVersionCheck {
		bcVersion := core.GetBlockChainVersion(chainDb)
		if bcVersion != core.BlockChainVersion && bcVersion != 0 {
			return nil, fmt.Errorf("Blockchain DB version mismatch (%d / %d). Run geth upgradedb.\n", bcVersion, core.BlockChainVersion)
		}
		core.WriteBlockChainVersion(chainDb, core.BlockChainVersion)
	}

	vmConfig := vm.Config{EnablePreimageRecording: config.EnablePreimageRecording}
	eth.blockchain, err = core.NewBlockChain(chainDb, eth.chainConfig, eth.engine, eth.eventMux, vmConfig)
	if err != nil {
		return nil, err
	}
	// Rewind the chain in case of an incompatible config upgrade.
	if compat, ok := genesisErr.(*params.ConfigCompatError); ok {
		log.Warn("Rewinding chain to upgrade configuration", "err", compat)
		eth.blockchain.SetHead(compat.RewindTo)
		core.WriteChainConfig(chainDb, genesisHash, chainConfig)
	}

	newPool := core.NewTxPool(eth.chainConfig, eth.EventMux(), eth.blockchain.State, eth.blockchain.GasLimit)
	eth.txPool = newPool

	maxPeers := config.MaxPeers
	if config.LightServ > 0 {
		// if we are running a light server, limit the number of ETH peers so that we reserve some space for incoming LES connections
		// temporary solution until the new peer connectivity API is finished
		halfPeers := maxPeers / 2
		maxPeers -= config.LightPeers
		if maxPeers < halfPeers {
			maxPeers = halfPeers
		}
	}

	if eth.protocolManager, err = NewProtocolManager(eth.chainConfig, config.FastSync, config.NetworkId, maxPeers, eth.eventMux, eth.txPool, eth.engine, eth.blockchain, chainDb); err != nil {
		return nil, err
	}

	eth.miner = miner.New(eth, eth.chainConfig, eth.EventMux(), eth.engine)
	eth.miner.SetGasPrice(config.GasPrice)
	eth.miner.SetExtra(config.ExtraData)

	eth.ApiBackend = &EthApiBackend{eth, nil}
	gpoParams := gasprice.Config{
		Blocks:     config.GpoBlocks,
		Percentile: config.GpoPercentile,
		Default:    config.GasPrice,
	}
	eth.ApiBackend.gpo = gasprice.NewOracle(eth.ApiBackend, gpoParams)

	return eth, nil
}

// CreateDB creates the chain database.
func CreateDB(ctx *node.ServiceContext, config *Config, name string) (ethdb.Database, error) {
	db, err := ctx.OpenDatabase(name, config.DatabaseCache, config.DatabaseHandles)
	if db, ok := db.(*ethdb.LDBDatabase); ok {
		db.Meter("eth/db/chaindata/")
	}
	return db, err
}

// CreateConsensusEngine creates the required type of consensus engine instance for an Ethereum service
func CreateConsensusEngine(ctx *node.ServiceContext, config *Config, chainConfig *params.ChainConfig, db ethdb.Database) consensus.Engine {
	switch {
	case config.PowFake:
		log.Warn("Ethash used in fake mode")
		return ethash.NewFaker()
	case config.PowTest:
		log.Warn("Ethash used in test mode")
		return ethash.NewTester()
	case config.PowShared:
		log.Warn("Ethash used in shared mode")
		return ethash.NewShared()
	default:
		return ethash.New(ctx.ResolvePath(config.EthashCacheDir), config.EthashCachesInMem, config.EthashCachesOnDisk,
			config.EthashDatasetDir, config.EthashDatasetsInMem, config.EthashDatasetsOnDisk)
	}
}

// APIs returns the collection of RPC services the ethereum package offers.
// NOTE, some of these services probably need to be moved to somewhere else.
func (s *Ethereum) APIs() []rpc.API {
	apis := ethapi.GetAPIs(s.ApiBackend, s.solcPath)

	// Append any APIs exposed explicitly by the consensus engine
	apis = append(apis, s.engine.APIs(s.BlockChain())...)

	// Append all the local APIs and return
	return append(apis, []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicEthereumAPI(s),
			Public:    true,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicMinerAPI(s),
			Public:    true,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   downloader.NewPublicDownloaderAPI(s.protocolManager.downloader, s.eventMux),
			Public:    true,
		}, {
			Namespace: "miner",
			Version:   "1.0",
			Service:   NewPrivateMinerAPI(s),
			Public:    false,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   filters.NewPublicFilterAPI(s.ApiBackend, false),
			Public:    true,
		}, {
			Namespace: "admin",
			Version:   "1.0",
			Service:   NewPrivateAdminAPI(s),
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPublicDebugAPI(s),
			Public:    true,
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPrivateDebugAPI(s.chainConfig, s),
		}, {
			Namespace: "net",
			Version:   "1.0",
			Service:   s.netRPCService,
			Public:    true,
		},
	}...)
}

func (s *Ethereum) ResetWithGenesisBlock(gb *types.Block) {
	s.blockchain.ResetWithGenesisBlock(gb)
}

func (s *Ethereum) Etherbase() (eb common.Address, err error) {
	if s.etherbase != (common.Address{}) {
		return s.etherbase, nil
	}
	if wallets := s.AccountManager().Wallets(); len(wallets) > 0 {
		if accounts := wallets[0].Accounts(); len(accounts) > 0 {
			return accounts[0].Address, nil
		}
	}
	return common.Address{}, fmt.Errorf("etherbase address must be explicitly specified")
}

// set in js console via admin interface or wrapper from cli flags
func (self *Ethereum) SetEtherbase(etherbase common.Address) {
	self.etherbase = etherbase
	self.miner.SetEtherbase(etherbase)
}

func (s *Ethereum) StartMining() error {
	eb, err := s.Etherbase()
	if err != nil {
		log.Error("Cannot start mining without etherbase", "err", err)
		return fmt.Errorf("etherbase missing: %v", err)
	}
	go s.miner.Start(eb)
	return nil
}

func (s *Ethereum) StopMining()         { s.miner.Stop() }
func (s *Ethereum) IsMining() bool      { return s.miner.Mining() }
func (s *Ethereum) Miner() *miner.Miner { return s.miner }

func (s *Ethereum) AccountManager() *accounts.Manager  { return s.accountManager }
func (s *Ethereum) BlockChain() *core.BlockChain       { return s.blockchain }
func (s *Ethereum) TxPool() *core.TxPool               { return s.txPool }
func (s *Ethereum) EventMux() *event.TypeMux           { return s.eventMux }
func (s *Ethereum) Engine() consensus.Engine           { return s.engine }
func (s *Ethereum) ChainDb() ethdb.Database            { return s.chainDb }
func (s *Ethereum) IsListening() bool                  { return true } // Always listening
func (s *Ethereum) EthVersion() int                    { return int(s.protocolManager.SubProtocols[0].Version) }
func (s *Ethereum) NetVersion() int                    { return s.netVersionId }
func (s *Ethereum) Downloader() *downloader.Downloader { return s.protocolManager.downloader }

// Protocols implements node.Service, returning all the currently configured
// network protocols to start.
func (s *Ethereum) Protocols() []p2p.Protocol {
	if s.lesServer == nil {
		return s.protocolManager.SubProtocols
	} else {
		return append(s.protocolManager.SubProtocols, s.lesServer.Protocols()...)
	}
}

// Start implements node.Service, starting all internal goroutines needed by the
// Ethereum protocol implementation.
func (s *Ethereum) Start(srvr *p2p.Server) error {
	s.netRPCService = ethapi.NewPublicNetAPI(srvr, s.NetVersion())

	s.protocolManager.Start()
	if s.lesServer != nil {
		s.lesServer.Start(srvr)
	}
	return nil
}

// Stop implements node.Service, terminating all internal goroutines used by the
// Ethereum protocol.
func (s *Ethereum) Stop() error {
	if s.stopDbUpgrade != nil {
		s.stopDbUpgrade()
	}
	s.blockchain.Stop()
	s.protocolManager.Stop()
	if s.lesServer != nil {
		s.lesServer.Stop()
	}
	s.txPool.Stop()
	s.miner.Stop()
	s.eventMux.Stop()

	s.chainDb.Close()
	close(s.shutdownChan)

	return nil
}

// This function will wait for a shutdown and resumes main thread execution
func (s *Ethereum) WaitForShutdown() {
	<-s.shutdownChan
}
