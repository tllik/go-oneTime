// Copyright 2015 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

// Package utils contains internal helper functions for go-ethereum commands.
package utils

import (
	"crypto/ecdsa"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/lab2528/go-oneTime/accounts"
	"github.com/lab2528/go-oneTime/accounts/keystore"
	"github.com/lab2528/go-oneTime/common"
	"github.com/lab2528/go-oneTime/consensus/ethash"
	"github.com/lab2528/go-oneTime/core"
	"github.com/lab2528/go-oneTime/core/state"
	"github.com/lab2528/go-oneTime/core/vm"
	"github.com/lab2528/go-oneTime/crypto"
	"github.com/lab2528/go-oneTime/eth"
	"github.com/lab2528/go-oneTime/ethdb"
	"github.com/lab2528/go-oneTime/ethstats"
	"github.com/lab2528/go-oneTime/event"
	"github.com/lab2528/go-oneTime/les"
	"github.com/lab2528/go-oneTime/log"
	"github.com/lab2528/go-oneTime/metrics"
	"github.com/lab2528/go-oneTime/node"
	"github.com/lab2528/go-oneTime/p2p/discover"
	"github.com/lab2528/go-oneTime/p2p/discv5"
	"github.com/lab2528/go-oneTime/p2p/nat"
	"github.com/lab2528/go-oneTime/p2p/netutil"
	"github.com/lab2528/go-oneTime/params"
	"github.com/lab2528/go-oneTime/rpc"
	whisper "github.com/lab2528/go-oneTime/whisper/whisperv2"
	"gopkg.in/urfave/cli.v1"
)

func init() {
	cli.AppHelpTemplate = `{{.Name}} {{if .Flags}}[global options] {{end}}command{{if .Flags}} [command options]{{end}} [arguments...]

VERSION:
   {{.Version}}

COMMANDS:
   {{range .Commands}}{{.Name}}{{with .ShortName}}, {{.}}{{end}}{{ "\t" }}{{.Usage}}
   {{end}}{{if .Flags}}
GLOBAL OPTIONS:
   {{range .Flags}}{{.}}
   {{end}}{{end}}
`

	cli.CommandHelpTemplate = `{{.Name}}{{if .Subcommands}} command{{end}}{{if .Flags}} [command options]{{end}} [arguments...]
{{if .Description}}{{.Description}}
{{end}}{{if .Subcommands}}
SUBCOMMANDS:
	{{range .Subcommands}}{{.Name}}{{with .ShortName}}, {{.}}{{end}}{{ "\t" }}{{.Usage}}
	{{end}}{{end}}{{if .Flags}}
OPTIONS:
	{{range .Flags}}{{.}}
	{{end}}{{end}}
`
}

// NewApp creates an app with sane defaults.
func NewApp(gitCommit, usage string) *cli.App {
	app := cli.NewApp()
	app.Name = filepath.Base(os.Args[0])
	app.Author = ""
	//app.Authors = nil
	app.Email = ""
	app.Version = params.Version
	if gitCommit != "" {
		app.Version += "-" + gitCommit[:8]
	}
	app.Usage = usage
	return app
}

// These are all the command line flags we support.
// If you add to this list, please remember to include the
// flag in the appropriate command definition.
//
// The flags are defined here so their names and help texts
// are the same for all commands.

var (
	// General settings
	DataDirFlag = DirectoryFlag{
		Name:  "datadir",
		Usage: "Data directory for the databases and keystore",
		Value: DirectoryString{node.DefaultDataDir()},
	}
	KeyStoreDirFlag = DirectoryFlag{
		Name:  "keystore",
		Usage: "Directory for the keystore (default = inside the datadir)",
	}
	EthashCacheDirFlag = DirectoryFlag{
		Name:  "ethash.cachedir",
		Usage: "Directory to store the ethash verification caches (default = inside the datadir)",
	}
	EthashCachesInMemoryFlag = cli.IntFlag{
		Name:  "ethash.cachesinmem",
		Usage: "Number of recent ethash caches to keep in memory (16MB each)",
		Value: 2,
	}
	EthashCachesOnDiskFlag = cli.IntFlag{
		Name:  "ethash.cachesondisk",
		Usage: "Number of recent ethash caches to keep on disk (16MB each)",
		Value: 3,
	}
	EthashDatasetDirFlag = DirectoryFlag{
		Name:  "ethash.dagdir",
		Usage: "Directory to store the ethash mining DAGs (default = inside home folder)",
	}
	EthashDatasetsInMemoryFlag = cli.IntFlag{
		Name:  "ethash.dagsinmem",
		Usage: "Number of recent ethash mining DAGs to keep in memory (1+GB each)",
		Value: 1,
	}
	EthashDatasetsOnDiskFlag = cli.IntFlag{
		Name:  "ethash.dagsondisk",
		Usage: "Number of recent ethash mining DAGs to keep on disk (1+GB each)",
		Value: 2,
	}
	NetworkIdFlag = cli.IntFlag{
		Name:  "networkid",
		Usage: "Network identifier (integer, 1=Frontier, 2=Morden (disused), 3=Ropsten)",
		Value: eth.NetworkId,
	}
	TestNetFlag = cli.BoolFlag{
		Name:  "testnet",
		Usage: "Ropsten network: pre-configured proof-of-work test network",
	}
	DevModeFlag = cli.BoolFlag{
		Name:  "dev",
		Usage: "Developer mode: pre-configured private network with several debugging flags",
	}
	IdentityFlag = cli.StringFlag{
		Name:  "identity",
		Usage: "Custom node name",
	}
	DocRootFlag = DirectoryFlag{
		Name:  "docroot",
		Usage: "Document Root for HTTPClient file scheme",
		Value: DirectoryString{homeDir()},
	}
	FastSyncFlag = cli.BoolFlag{
		Name:  "fast",
		Usage: "Enable fast syncing through state downloads",
	}
	LightModeFlag = cli.BoolFlag{
		Name:  "light",
		Usage: "Enable light client mode",
	}
	LightServFlag = cli.IntFlag{
		Name:  "lightserv",
		Usage: "Maximum percentage of time allowed for serving LES requests (0-90)",
		Value: 0,
	}
	LightPeersFlag = cli.IntFlag{
		Name:  "lightpeers",
		Usage: "Maximum number of LES client peers",
		Value: 20,
	}
	LightKDFFlag = cli.BoolFlag{
		Name:  "lightkdf",
		Usage: "Reduce key-derivation RAM & CPU usage at some expense of KDF strength",
	}
	// Performance tuning settings
	CacheFlag = cli.IntFlag{
		Name:  "cache",
		Usage: "Megabytes of memory allocated to internal caching (min 16MB / database forced)",
		Value: 128,
	}
	TrieCacheGenFlag = cli.IntFlag{
		Name:  "trie-cache-gens",
		Usage: "Number of trie node generations to keep in memory",
		Value: int(state.MaxTrieCacheGen),
	}
	// Miner settings
	MiningEnabledFlag = cli.BoolFlag{
		Name:  "mine",
		Usage: "Enable mining",
	}
	MinerThreadsFlag = cli.IntFlag{
		Name:  "minerthreads",
		Usage: "Number of CPU threads to use for mining",
		Value: runtime.NumCPU(),
	}
	TargetGasLimitFlag = cli.Uint64Flag{
		Name:  "targetgaslimit",
		Usage: "Target gas limit sets the artificial target gas floor for the blocks to mine",
		Value: params.GenesisGasLimit.Uint64(),
	}
	EtherbaseFlag = cli.StringFlag{
		Name:  "etherbase",
		Usage: "Public address for block mining rewards (default = first account created)",
		Value: "0",
	}
	GasPriceFlag = BigFlag{
		Name:  "gasprice",
		Usage: "Minimal gas price to accept for mining a transactions",
		Value: big.NewInt(20 * params.Shannon),
	}
	ExtraDataFlag = cli.StringFlag{
		Name:  "extradata",
		Usage: "Block extra data set by the miner (default = client version)",
	}
	// Account settings
	UnlockedAccountFlag = cli.StringFlag{
		Name:  "unlock",
		Usage: "Comma separated list of accounts to unlock",
		Value: "",
	}
	PasswordFileFlag = cli.StringFlag{
		Name:  "password",
		Usage: "Password file to use for non-inteactive password input",
		Value: "",
	}

	VMForceJitFlag = cli.BoolFlag{
		Name:  "forcejit",
		Usage: "Force the JIT VM to take precedence",
	}
	VMJitCacheFlag = cli.IntFlag{
		Name:  "jitcache",
		Usage: "Amount of cached JIT VM programs",
		Value: 64,
	}
	VMEnableJitFlag = cli.BoolFlag{
		Name:  "jitvm",
		Usage: "Enable the JIT VM",
	}
	VMEnableDebugFlag = cli.BoolFlag{
		Name:  "vmdebug",
		Usage: "Record information useful for VM and contract debugging",
	}
	// Logging and debug settings
	EthStatsURLFlag = cli.StringFlag{
		Name:  "ethstats",
		Usage: "Reporting URL of a ethstats service (nodename:secret@host:port)",
	}
	MetricsEnabledFlag = cli.BoolFlag{
		Name:  metrics.MetricsEnabledFlag,
		Usage: "Enable metrics collection and reporting",
	}
	FakePoWFlag = cli.BoolFlag{
		Name:  "fakepow",
		Usage: "Disables proof-of-work verification",
	}
	NoCompactionFlag = cli.BoolFlag{
		Name:  "nocompaction",
		Usage: "Disables db compaction after import",
	}
	// RPC settings
	RPCEnabledFlag = cli.BoolFlag{
		Name:  "rpc",
		Usage: "Enable the HTTP-RPC server",
	}
	RPCListenAddrFlag = cli.StringFlag{
		Name:  "rpcaddr",
		Usage: "HTTP-RPC server listening interface",
		Value: node.DefaultHTTPHost,
	}
	RPCPortFlag = cli.IntFlag{
		Name:  "rpcport",
		Usage: "HTTP-RPC server listening port",
		Value: node.DefaultHTTPPort,
	}
	RPCCORSDomainFlag = cli.StringFlag{
		Name:  "rpccorsdomain",
		Usage: "Comma separated list of domains from which to accept cross origin requests (browser enforced)",
		Value: "",
	}
	RPCApiFlag = cli.StringFlag{
		Name:  "rpcapi",
		Usage: "API's offered over the HTTP-RPC interface",
		Value: rpc.DefaultHTTPApis,
	}
	IPCDisabledFlag = cli.BoolFlag{
		Name:  "ipcdisable",
		Usage: "Disable the IPC-RPC server",
	}
	IPCApiFlag = cli.StringFlag{
		Name:  "ipcapi",
		Usage: "APIs offered over the IPC-RPC interface",
		Value: rpc.DefaultIPCApis,
	}
	IPCPathFlag = DirectoryFlag{
		Name:  "ipcpath",
		Usage: "Filename for IPC socket/pipe within the datadir (explicit paths escape it)",
		Value: DirectoryString{"geth.ipc"},
	}
	WSEnabledFlag = cli.BoolFlag{
		Name:  "ws",
		Usage: "Enable the WS-RPC server",
	}
	WSListenAddrFlag = cli.StringFlag{
		Name:  "wsaddr",
		Usage: "WS-RPC server listening interface",
		Value: node.DefaultWSHost,
	}
	WSPortFlag = cli.IntFlag{
		Name:  "wsport",
		Usage: "WS-RPC server listening port",
		Value: node.DefaultWSPort,
	}
	WSApiFlag = cli.StringFlag{
		Name:  "wsapi",
		Usage: "API's offered over the WS-RPC interface",
		Value: rpc.DefaultHTTPApis,
	}
	WSAllowedOriginsFlag = cli.StringFlag{
		Name:  "wsorigins",
		Usage: "Origins from which to accept websockets requests",
		Value: "",
	}
	ExecFlag = cli.StringFlag{
		Name:  "exec",
		Usage: "Execute JavaScript statement (only in combination with console/attach)",
	}
	PreloadJSFlag = cli.StringFlag{
		Name:  "preload",
		Usage: "Comma separated list of JavaScript files to preload into the console",
	}

	// Network Settings
	MaxPeersFlag = cli.IntFlag{
		Name:  "maxpeers",
		Usage: "Maximum number of network peers (network disabled if set to 0)",
		Value: 25,
	}
	MaxPendingPeersFlag = cli.IntFlag{
		Name:  "maxpendpeers",
		Usage: "Maximum number of pending connection attempts (defaults used if set to 0)",
		Value: 0,
	}
	ListenPortFlag = cli.IntFlag{
		Name:  "port",
		Usage: "Network listening port",
		Value: 30303,
	}
	BootnodesFlag = cli.StringFlag{
		Name:  "bootnodes",
		Usage: "Comma separated enode URLs for P2P discovery bootstrap",
		Value: "",
	}
	NodeKeyFileFlag = cli.StringFlag{
		Name:  "nodekey",
		Usage: "P2P node key file",
	}
	NodeKeyHexFlag = cli.StringFlag{
		Name:  "nodekeyhex",
		Usage: "P2P node key as hex (for testing)",
	}
	NATFlag = cli.StringFlag{
		Name:  "nat",
		Usage: "NAT port mapping mechanism (any|none|upnp|pmp|extip:<IP>)",
		Value: "any",
	}
	NoDiscoverFlag = cli.BoolFlag{
		Name:  "nodiscover",
		Usage: "Disables the peer discovery mechanism (manual peer addition)",
	}
	DiscoveryV5Flag = cli.BoolFlag{
		Name:  "v5disc",
		Usage: "Enables the experimental RLPx V5 (Topic Discovery) mechanism",
	}
	NetrestrictFlag = cli.StringFlag{
		Name:  "netrestrict",
		Usage: "Restricts network communication to the given IP networks (CIDR masks)",
	}

	WhisperEnabledFlag = cli.BoolFlag{
		Name:  "shh",
		Usage: "Enable Whisper",
	}

	// ATM the url is left to the user and deployment to
	JSpathFlag = cli.StringFlag{
		Name:  "jspath",
		Usage: "JavaScript root path for `loadScript`",
		Value: ".",
	}
	SolcPathFlag = cli.StringFlag{
		Name:  "solc",
		Usage: "Solidity compiler command to be used",
		Value: "solc",
	}

	// Gas price oracle settings
	GpoBlocksFlag = cli.IntFlag{
		Name:  "gpoblocks",
		Usage: "Number of recent blocks to check for gas prices",
		Value: 10,
	}
	GpoPercentileFlag = cli.IntFlag{
		Name:  "gpopercentile",
		Usage: "Suggested gas price is the given percentile of a set of recent transaction gas prices",
		Value: 50,
	}
)

// MakeDataDir retrieves the currently requested data directory, terminating
// if none (or the empty string) is specified. If the node is starting a testnet,
// the a subdirectory of the specified datadir will be used.
func MakeDataDir(ctx *cli.Context) string {
	if path := ctx.GlobalString(DataDirFlag.Name); path != "" {
		// TODO: choose a different location outside of the regular datadir.
		if ctx.GlobalBool(TestNetFlag.Name) {
			return filepath.Join(path, "testnet")
		}
		return path
	}
	Fatalf("Cannot determine default data directory, please set manually (--datadir)")
	return ""
}

// MakeEthashCacheDir returns the directory to use for storing the ethash cache
// dumps.
func MakeEthashCacheDir(ctx *cli.Context) string {
	if ctx.GlobalIsSet(EthashCacheDirFlag.Name) && ctx.GlobalString(EthashCacheDirFlag.Name) == "" {
		return ""
	}
	if !ctx.GlobalIsSet(EthashCacheDirFlag.Name) {
		return "ethash"
	}
	return ctx.GlobalString(EthashCacheDirFlag.Name)
}

// MakeEthashDatasetDir returns the directory to use for storing the full ethash
// dataset dumps.
func MakeEthashDatasetDir(ctx *cli.Context) string {
	if !ctx.GlobalIsSet(EthashDatasetDirFlag.Name) {
		home := os.Getenv("HOME")
		if home == "" {
			if user, err := user.Current(); err == nil {
				home = user.HomeDir
			}
		}
		if runtime.GOOS == "windows" {
			return filepath.Join(home, "AppData", "Ethash")
		}
		return filepath.Join(home, ".ethash")
	}
	return ctx.GlobalString(EthashDatasetDirFlag.Name)
}

// MakeIPCPath creates an IPC path configuration from the set command line flags,
// returning an empty string if IPC was explicitly disabled, or the set path.
func MakeIPCPath(ctx *cli.Context) string {
	if ctx.GlobalBool(IPCDisabledFlag.Name) {
		return ""
	}
	return ctx.GlobalString(IPCPathFlag.Name)
}

// MakeNodeKey creates a node key from set command line flags, either loading it
// from a file or as a specified hex value. If neither flags were provided, this
// method returns nil and an emphemeral key is to be generated.
func MakeNodeKey(ctx *cli.Context) *ecdsa.PrivateKey {
	var (
		hex  = ctx.GlobalString(NodeKeyHexFlag.Name)
		file = ctx.GlobalString(NodeKeyFileFlag.Name)

		key *ecdsa.PrivateKey
		err error
	)
	switch {
	case file != "" && hex != "":
		Fatalf("Options %q and %q are mutually exclusive", NodeKeyFileFlag.Name, NodeKeyHexFlag.Name)

	case file != "":
		if key, err = crypto.LoadECDSA(file); err != nil {
			Fatalf("Option %q: %v", NodeKeyFileFlag.Name, err)
		}

	case hex != "":
		if key, err = crypto.HexToECDSA(hex); err != nil {
			Fatalf("Option %q: %v", NodeKeyHexFlag.Name, err)
		}
	}
	return key
}

// makeNodeUserIdent creates the user identifier from CLI flags.
func makeNodeUserIdent(ctx *cli.Context) string {
	var comps []string
	if identity := ctx.GlobalString(IdentityFlag.Name); len(identity) > 0 {
		comps = append(comps, identity)
	}
	if ctx.GlobalBool(VMEnableJitFlag.Name) {
		comps = append(comps, "JIT")
	}
	return strings.Join(comps, "/")
}

// MakeBootstrapNodes creates a list of bootstrap nodes from the command line
// flags, reverting to pre-configured ones if none have been specified.
func MakeBootstrapNodes(ctx *cli.Context) []*discover.Node {
	urls := params.MainnetBootnodes
	if ctx.GlobalIsSet(BootnodesFlag.Name) {
		urls = strings.Split(ctx.GlobalString(BootnodesFlag.Name), ",")
	} else if ctx.GlobalBool(TestNetFlag.Name) {
		urls = params.TestnetBootnodes
	}

	bootnodes := make([]*discover.Node, 0, len(urls))
	for _, url := range urls {
		node, err := discover.ParseNode(url)
		if err != nil {
			log.Error("Bootstrap URL invalid", "enode", url, "err", err)
			continue
		}
		bootnodes = append(bootnodes, node)
	}
	return bootnodes
}

// MakeBootstrapNodesV5 creates a list of bootstrap nodes from the command line
// flags, reverting to pre-configured ones if none have been specified.
func MakeBootstrapNodesV5(ctx *cli.Context) []*discv5.Node {
	urls := params.DiscoveryV5Bootnodes
	if ctx.GlobalIsSet(BootnodesFlag.Name) {
		urls = strings.Split(ctx.GlobalString(BootnodesFlag.Name), ",")
	}

	bootnodes := make([]*discv5.Node, 0, len(urls))
	for _, url := range urls {
		node, err := discv5.ParseNode(url)
		if err != nil {
			log.Error("Bootstrap URL invalid", "enode", url, "err", err)
			continue
		}
		bootnodes = append(bootnodes, node)
	}
	return bootnodes
}

// MakeListenAddress creates a TCP listening address string from set command
// line flags.
func MakeListenAddress(ctx *cli.Context) string {
	return fmt.Sprintf(":%d", ctx.GlobalInt(ListenPortFlag.Name))
}

// MakeDiscoveryV5Address creates a UDP listening address string from set command
// line flags for the V5 discovery protocol.
func MakeDiscoveryV5Address(ctx *cli.Context) string {
	return fmt.Sprintf(":%d", ctx.GlobalInt(ListenPortFlag.Name)+1)
}

// MakeNAT creates a port mapper from set command line flags.
func MakeNAT(ctx *cli.Context) nat.Interface {
	natif, err := nat.Parse(ctx.GlobalString(NATFlag.Name))
	if err != nil {
		Fatalf("Option %s: %v", NATFlag.Name, err)
	}
	return natif
}

// MakeRPCModules splits input separated by a comma and trims excessive white
// space from the substrings.
func MakeRPCModules(input string) []string {
	result := strings.Split(input, ",")
	for i, r := range result {
		result[i] = strings.TrimSpace(r)
	}
	return result
}

// MakeHTTPRpcHost creates the HTTP RPC listener interface string from the set
// command line flags, returning empty if the HTTP endpoint is disabled.
func MakeHTTPRpcHost(ctx *cli.Context) string {
	if !ctx.GlobalBool(RPCEnabledFlag.Name) {
		return ""
	}
	return ctx.GlobalString(RPCListenAddrFlag.Name)
}

// MakeWSRpcHost creates the WebSocket RPC listener interface string from the set
// command line flags, returning empty if the HTTP endpoint is disabled.
func MakeWSRpcHost(ctx *cli.Context) string {
	if !ctx.GlobalBool(WSEnabledFlag.Name) {
		return ""
	}
	return ctx.GlobalString(WSListenAddrFlag.Name)
}

// MakeDatabaseHandles raises out the number of allowed file handles per process
// for Geth and returns half of the allowance to assign to the database.
func MakeDatabaseHandles() int {
	if err := raiseFdLimit(2048); err != nil {
		Fatalf("Failed to raise file descriptor allowance: %v", err)
	}
	limit, err := getFdLimit()
	if err != nil {
		Fatalf("Failed to retrieve file descriptor allowance: %v", err)
	}
	if limit > 2048 { // cap database file descriptors even if more is available
		limit = 2048
	}
	return limit / 2 // Leave half for networking and other stuff
}

// MakeAddress converts an account specified directly as a hex encoded string or
// a key index in the key store to an internal account representation.
func MakeAddress(ks *keystore.KeyStore, account string) (accounts.Account, error) {
	// If the specified account is a valid address, return it
	if common.IsHexAddress(account) {
		return accounts.Account{Address: common.HexToAddress(account)}, nil
	}
	// Otherwise try to interpret the account as a keystore index
	index, err := strconv.Atoi(account)
	if err != nil || index < 0 {
		return accounts.Account{}, fmt.Errorf("invalid account address or index %q", account)
	}
	accs := ks.Accounts()
	if len(accs) <= index {
		return accounts.Account{}, fmt.Errorf("index %d higher than number of accounts %d", index, len(accs))
	}
	return accs[index], nil
}

// MakeEtherbase retrieves the etherbase either from the directly specified
// command line flags or from the keystore if CLI indexed.
func MakeEtherbase(ks *keystore.KeyStore, ctx *cli.Context) common.Address {
	accounts := ks.Accounts()
	if !ctx.GlobalIsSet(EtherbaseFlag.Name) && len(accounts) == 0 {
		log.Warn("No etherbase set and no accounts found as default")
		return common.Address{}
	}
	etherbase := ctx.GlobalString(EtherbaseFlag.Name)
	if etherbase == "" {
		return common.Address{}
	}
	// If the specified etherbase is a valid address, return it
	account, err := MakeAddress(ks, etherbase)
	if err != nil {
		Fatalf("Option %q: %v", EtherbaseFlag.Name, err)
	}
	return account.Address
}

// MakeMinerExtra resolves extradata for the miner from the set command line flags
// or returns a default one composed on the client, runtime and OS metadata.
func MakeMinerExtra(extra []byte, ctx *cli.Context) []byte {
	if ctx.GlobalIsSet(ExtraDataFlag.Name) {
		return []byte(ctx.GlobalString(ExtraDataFlag.Name))
	}
	return extra
}

// MakePasswordList reads password lines from the file specified by --password.
func MakePasswordList(ctx *cli.Context) []string {
	path := ctx.GlobalString(PasswordFileFlag.Name)
	if path == "" {
		return nil
	}
	text, err := ioutil.ReadFile(path)
	if err != nil {
		Fatalf("Failed to read password file: %v", err)
	}
	lines := strings.Split(string(text), "\n")
	// Sanitise DOS line endings.
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines
}

// MakeNode configures a node with no services from command line flags.
func MakeNode(ctx *cli.Context, name, gitCommit string) *node.Node {
	vsn := params.Version
	if gitCommit != "" {
		vsn += "-" + gitCommit[:8]
	}

	// if we're running a light client or server, force enable the v5 peer discovery unless it is explicitly disabled with --nodiscover
	// note that explicitly specifying --v5disc overrides --nodiscover, in which case the later only disables v4 discovery
	forceV5Discovery := (ctx.GlobalBool(LightModeFlag.Name) || ctx.GlobalInt(LightServFlag.Name) > 0) && !ctx.GlobalBool(NoDiscoverFlag.Name)

	config := &node.Config{
		DataDir:           MakeDataDir(ctx),
		KeyStoreDir:       ctx.GlobalString(KeyStoreDirFlag.Name),
		UseLightweightKDF: ctx.GlobalBool(LightKDFFlag.Name),
		PrivateKey:        MakeNodeKey(ctx),
		Name:              name,
		Version:           vsn,
		UserIdent:         makeNodeUserIdent(ctx),
		NoDiscovery:       ctx.GlobalBool(NoDiscoverFlag.Name) || ctx.GlobalBool(LightModeFlag.Name), // always disable v4 discovery in light client mode
		DiscoveryV5:       ctx.GlobalBool(DiscoveryV5Flag.Name) || forceV5Discovery,
		DiscoveryV5Addr:   MakeDiscoveryV5Address(ctx),
		BootstrapNodes:    MakeBootstrapNodes(ctx),
		BootstrapNodesV5:  MakeBootstrapNodesV5(ctx),
		ListenAddr:        MakeListenAddress(ctx),
		NAT:               MakeNAT(ctx),
		MaxPeers:          ctx.GlobalInt(MaxPeersFlag.Name),
		MaxPendingPeers:   ctx.GlobalInt(MaxPendingPeersFlag.Name),
		IPCPath:           MakeIPCPath(ctx),
		HTTPHost:          MakeHTTPRpcHost(ctx),
		HTTPPort:          ctx.GlobalInt(RPCPortFlag.Name),
		HTTPCors:          ctx.GlobalString(RPCCORSDomainFlag.Name),
		HTTPModules:       MakeRPCModules(ctx.GlobalString(RPCApiFlag.Name)),
		WSHost:            MakeWSRpcHost(ctx),
		WSPort:            ctx.GlobalInt(WSPortFlag.Name),
		WSOrigins:         ctx.GlobalString(WSAllowedOriginsFlag.Name),
		WSModules:         MakeRPCModules(ctx.GlobalString(WSApiFlag.Name)),
	}
	if ctx.GlobalBool(DevModeFlag.Name) {
		if !ctx.GlobalIsSet(DataDirFlag.Name) {
			config.DataDir = filepath.Join(os.TempDir(), "/ethereum_dev_mode")
		}
		// --dev mode does not need p2p networking.
		config.MaxPeers = 0
		config.ListenAddr = ":0"
	}
	if netrestrict := ctx.GlobalString(NetrestrictFlag.Name); netrestrict != "" {
		list, err := netutil.ParseNetlist(netrestrict)
		if err != nil {
			Fatalf("Option %q: %v", NetrestrictFlag.Name, err)
		}
		config.NetRestrict = list
	}

	stack, err := node.New(config)
	if err != nil {
		Fatalf("Failed to create the protocol stack: %v", err)
	}
	return stack
}

// RegisterEthService configures eth.Ethereum from command line flags and adds it to the
// given node.
func RegisterEthService(ctx *cli.Context, stack *node.Node, extra []byte) {
	// Avoid conflicting network flags
	networks, netFlags := 0, []cli.BoolFlag{DevModeFlag, TestNetFlag}
	for _, flag := range netFlags {
		if ctx.GlobalBool(flag.Name) {
			networks++
		}
	}
	if networks > 1 {
		Fatalf("The %v flags are mutually exclusive", netFlags)
	}
	ks := stack.AccountManager().Backends(keystore.KeyStoreType)[0].(*keystore.KeyStore)

	ethConf := &eth.Config{
		Etherbase:               MakeEtherbase(ks, ctx),
		FastSync:                ctx.GlobalBool(FastSyncFlag.Name),
		LightMode:               ctx.GlobalBool(LightModeFlag.Name),
		LightServ:               ctx.GlobalInt(LightServFlag.Name),
		LightPeers:              ctx.GlobalInt(LightPeersFlag.Name),
		MaxPeers:                ctx.GlobalInt(MaxPeersFlag.Name),
		DatabaseCache:           ctx.GlobalInt(CacheFlag.Name),
		DatabaseHandles:         MakeDatabaseHandles(),
		NetworkId:               ctx.GlobalInt(NetworkIdFlag.Name),
		MinerThreads:            ctx.GlobalInt(MinerThreadsFlag.Name),
		ExtraData:               MakeMinerExtra(extra, ctx),
		DocRoot:                 ctx.GlobalString(DocRootFlag.Name),
		GasPrice:                GlobalBig(ctx, GasPriceFlag.Name),
		GpoBlocks:               ctx.GlobalInt(GpoBlocksFlag.Name),
		GpoPercentile:           ctx.GlobalInt(GpoPercentileFlag.Name),
		SolcPath:                ctx.GlobalString(SolcPathFlag.Name),
		EthashCacheDir:          MakeEthashCacheDir(ctx),
		EthashCachesInMem:       ctx.GlobalInt(EthashCachesInMemoryFlag.Name),
		EthashCachesOnDisk:      ctx.GlobalInt(EthashCachesOnDiskFlag.Name),
		EthashDatasetDir:        MakeEthashDatasetDir(ctx),
		EthashDatasetsInMem:     ctx.GlobalInt(EthashDatasetsInMemoryFlag.Name),
		EthashDatasetsOnDisk:    ctx.GlobalInt(EthashDatasetsOnDiskFlag.Name),
		EnablePreimageRecording: ctx.GlobalBool(VMEnableDebugFlag.Name),
	}

	// Override any default configs in dev mode or the test net
	switch {
	case ctx.GlobalBool(TestNetFlag.Name):
		if !ctx.GlobalIsSet(NetworkIdFlag.Name) {
			ethConf.NetworkId = 3
		}
		ethConf.Genesis = core.DefaultTestnetGenesisBlock()
	case ctx.GlobalBool(DevModeFlag.Name):
		ethConf.Genesis = core.DevGenesisBlock()
		if !ctx.GlobalIsSet(GasPriceFlag.Name) {
			ethConf.GasPrice = new(big.Int)
		}
		ethConf.PowTest = true
	}
	// Override any global options pertaining to the Ethereum protocol
	if gen := ctx.GlobalInt(TrieCacheGenFlag.Name); gen > 0 {
		state.MaxTrieCacheGen = uint16(gen)
	}

	if ethConf.LightMode {
		if err := stack.Register(func(ctx *node.ServiceContext) (node.Service, error) {
			return les.New(ctx, ethConf)
		}); err != nil {
			Fatalf("Failed to register the Ethereum light node service: %v", err)
		}
	} else {
		if err := stack.Register(func(ctx *node.ServiceContext) (node.Service, error) {
			fullNode, err := eth.New(ctx, ethConf)
			if fullNode != nil && ethConf.LightServ > 0 {
				ls, _ := les.NewLesServer(fullNode, ethConf)
				fullNode.AddLesServer(ls)
			}
			return fullNode, err
		}); err != nil {
			Fatalf("Failed to register the Ethereum full node service: %v", err)
		}
	}
}

// RegisterShhService configures Whisper and adds it to the given node.
func RegisterShhService(stack *node.Node) {
	if err := stack.Register(func(*node.ServiceContext) (node.Service, error) { return whisper.New(), nil }); err != nil {
		Fatalf("Failed to register the Whisper service: %v", err)
	}
}

// RegisterEthStatsService configures the Ethereum Stats daemon and adds it to
// th egiven node.
func RegisterEthStatsService(stack *node.Node, url string) {
	if err := stack.Register(func(ctx *node.ServiceContext) (node.Service, error) {
		// Retrieve both eth and les services
		var ethServ *eth.Ethereum
		ctx.Service(&ethServ)

		var lesServ *les.LightEthereum
		ctx.Service(&lesServ)

		return ethstats.New(url, ethServ, lesServ)
	}); err != nil {
		Fatalf("Failed to register the Ethereum Stats service: %v", err)
	}
}

// SetupNetwork configures the system for either the main net or some test network.
func SetupNetwork(ctx *cli.Context) {
	params.TargetGasLimit = new(big.Int).SetUint64(ctx.GlobalUint64(TargetGasLimitFlag.Name))
}

func ChainDbName(ctx *cli.Context) string {
	if ctx.GlobalBool(LightModeFlag.Name) {
		return "lightchaindata"
	} else {
		return "chaindata"
	}
}

// MakeChainDatabase open an LevelDB using the flags passed to the client and will hard crash if it fails.
func MakeChainDatabase(ctx *cli.Context, stack *node.Node) ethdb.Database {
	var (
		cache   = ctx.GlobalInt(CacheFlag.Name)
		handles = MakeDatabaseHandles()
		name    = ChainDbName(ctx)
	)

	chainDb, err := stack.OpenDatabase(name, cache, handles)
	if err != nil {
		Fatalf("Could not open database: %v", err)
	}
	return chainDb
}

func MakeGenesis(ctx *cli.Context) *core.Genesis {
	var genesis *core.Genesis
	switch {
	case ctx.GlobalBool(TestNetFlag.Name):
		genesis = core.DefaultTestnetGenesisBlock()
	case ctx.GlobalBool(DevModeFlag.Name):
		genesis = core.DevGenesisBlock()
	}
	return genesis
}

// MakeChain creates a chain manager from set command line flags.
func MakeChain(ctx *cli.Context, stack *node.Node) (chain *core.BlockChain, chainDb ethdb.Database) {
	var err error
	chainDb = MakeChainDatabase(ctx, stack)

	engine := ethash.NewFaker()
	if !ctx.GlobalBool(FakePoWFlag.Name) {
		engine = ethash.New("", 1, 0, "", 1, 0)
	}
	config, _, err := core.SetupGenesisBlock(chainDb, MakeGenesis(ctx))
	if err != nil {
		Fatalf("%v", err)
	}
	vmcfg := vm.Config{EnablePreimageRecording: ctx.GlobalBool(VMEnableDebugFlag.Name)}
	chain, err = core.NewBlockChain(chainDb, config, engine, new(event.TypeMux), vmcfg)
	if err != nil {
		Fatalf("Can't create BlockChain: %v", err)
	}
	return chain, chainDb
}

// MakeConsolePreloads retrieves the absolute paths for the console JavaScript
// scripts to preload before starting.
func MakeConsolePreloads(ctx *cli.Context) []string {
	// Skip preloading if there's nothing to preload
	if ctx.GlobalString(PreloadJSFlag.Name) == "" {
		return nil
	}
	// Otherwise resolve absolute paths and return them
	preloads := []string{}

	assets := ctx.GlobalString(JSpathFlag.Name)
	for _, file := range strings.Split(ctx.GlobalString(PreloadJSFlag.Name), ",") {
		preloads = append(preloads, common.AbsolutePath(assets, strings.TrimSpace(file)))
	}
	return preloads
}