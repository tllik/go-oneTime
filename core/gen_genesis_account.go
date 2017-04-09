// Code generated by github.com/fjl/gencodec. DO NOT EDIT.

package core

import (
	"encoding/json"
	"errors"
	"math/big"

	"github.com/lab2528/go-oneTime/common"
	"github.com/lab2528/go-oneTime/common/hexutil"
	"github.com/lab2528/go-oneTime/common/math"
)

func (g GenesisAccount) MarshalJSON() ([]byte, error) {
	type GenesisAccount struct {
		Code    hexutil.Bytes               `json:"code,omitempty"`
		Storage map[common.Hash]common.Hash `json:"storage,omitempty"`
		Balance *math.HexOrDecimal256       `json:"balance" gencodec:"required"`
		Nonce   math.HexOrDecimal64         `json:"nonce,omitempty"`
	}
	var enc GenesisAccount
	enc.Code = g.Code
	enc.Storage = g.Storage
	enc.Balance = (*math.HexOrDecimal256)(g.Balance)
	enc.Nonce = math.HexOrDecimal64(g.Nonce)
	return json.Marshal(&enc)
}

func (g *GenesisAccount) UnmarshalJSON(input []byte) error {
	type GenesisAccount struct {
		Code    hexutil.Bytes               `json:"code,omitempty"`
		Storage map[common.Hash]common.Hash `json:"storage,omitempty"`
		Balance *math.HexOrDecimal256       `json:"balance" gencodec:"required"`
		Nonce   *math.HexOrDecimal64        `json:"nonce,omitempty"`
	}
	var dec GenesisAccount
	if err := json.Unmarshal(input, &dec); err != nil {
		return err
	}
	if dec.Code != nil {
		g.Code = dec.Code
	}
	if dec.Storage != nil {
		g.Storage = dec.Storage
	}
	if dec.Balance == nil {
		return errors.New("missing required field 'balance' for GenesisAccount")
	}
	g.Balance = (*big.Int)(dec.Balance)
	if dec.Nonce != nil {
		g.Nonce = uint64(*dec.Nonce)
	}
	return nil
}