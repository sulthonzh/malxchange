package evm

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/shopspring/decimal"
)

// gasNative is the fixed cost of a plain value transfer.
const gasNative uint64 = 21000

// gasToken is the estimated cost of an ERC-20 transfer (spec #22:
// 65000, comfortably above the typical ~52k hot path).
const gasToken uint64 = 65000

// priceMultiplier pads SuggestGasPrice to land under load.
var priceMultiplier = big.NewInt(12) // /10 → 1.2×

// TransferNative signs and broadcasts a legacy type-0 value transfer
// of amount ETH (18dp exact) from the given raw private key.
func (c *Client) TransferNative(ctx context.Context, privKey []byte, to string, amount decimal.Decimal) (string, error) {
	value := toUnits(amount, NativeDecimals)
	if value.Sign() <= 0 {
		return "", fmt.Errorf("evm: non-positive native amount %s", amount)
	}
	return c.send(ctx, privKey, common.HexToAddress(to), value, gasNative, nil)
}

// TransferToken signs and broadcasts an ERC-20 transfer of amount
// tokens (tokenDecimals dp) from the given raw private key.
func (c *Client) TransferToken(ctx context.Context, contract string, privKey []byte, to string, amount decimal.Decimal, tokenDecimals int32) (string, error) {
	units := toUnits(amount, tokenDecimals)
	if units.Sign() <= 0 {
		return "", fmt.Errorf("evm: non-positive token amount %s", amount)
	}
	data, err := erc20ABI.Pack("transfer", common.HexToAddress(to), units)
	if err != nil {
		return "", err
	}
	return c.send(ctx, privKey, common.HexToAddress(contract), nil, gasToken, data)
}

// send performs the serialized nonce→sign→broadcast cycle for a
// legacy EIP-155 transaction from privKey.
func (c *Client) send(ctx context.Context, privKey []byte, to common.Address, value *big.Int, gas uint64, data []byte) (string, error) {
	key, err := crypto.ToECDSA(privKey)
	if err != nil {
		return "", fmt.Errorf("evm: bad private key: %w", err)
	}
	from := crypto.PubkeyToAddress(key.PublicKey)

	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	nonce, err := rpc(c, func(cl *ethclient.Client) (uint64, error) {
		return cl.NonceAt(ctx, from, nil) // head; anvil automines so pending==head
	})
	if err != nil {
		return "", err
	}
	base, err := rpc(c, func(cl *ethclient.Client) (*big.Int, error) {
		return cl.SuggestGasPrice(ctx)
	})
	if err != nil {
		return "", err
	}
	gasPrice := new(big.Int).Mul(base, priceMultiplier)
	gasPrice.Div(gasPrice, big.NewInt(10))

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		GasPrice: gasPrice,
		Gas:      gas,
		To:       &to,
		Value:    value,
		Data:     data,
	})
	signed, err := types.SignTx(tx, types.NewEIP155Signer(c.chainID), key)
	if err != nil {
		return "", err
	}
	err = rpcErr(c, func(cl *ethclient.Client) error {
		return cl.SendTransaction(ctx, signed)
	})
	if err != nil {
		return "", err
	}
	return signed.Hash().Hex(), nil
}

// HotBalance is the native (18dp) balance of addr at head.
func (c *Client) HotBalance(ctx context.Context, addr string) (decimal.Decimal, error) {
	wei, err := rpc(c, func(cl *ethclient.Client) (*big.Int, error) {
		return cl.BalanceAt(ctx, common.HexToAddress(addr), nil)
	})
	if err != nil {
		return decimal.Decimal{}, err
	}
	return weiToDecimal(wei), nil
}

// TokenBalance is the ERC-20 balance of addr, scaled by the token's
// on-chain (or registered) decimals.
func (c *Client) TokenBalance(ctx context.Context, contract, addr string) (decimal.Decimal, error) {
	token := common.HexToAddress(contract)
	data, err := erc20ABI.Pack("balanceOf", common.HexToAddress(addr))
	if err != nil {
		return decimal.Decimal{}, err
	}
	out, err := c.callContract(ctx, token, data)
	if err != nil {
		return decimal.Decimal{}, err
	}
	vals, err := erc20ABI.Unpack("balanceOf", out)
	if err != nil {
		return decimal.Decimal{}, err
	}
	if len(vals) != 1 {
		return decimal.Decimal{}, fmt.Errorf("evm: balanceOf() returned no value")
	}
	raw, ok := vals[0].(*big.Int)
	if !ok {
		return decimal.Decimal{}, fmt.Errorf("evm: balanceOf() returned unexpected type")
	}
	dp, err := c.decimalsFor(ctx, token)
	if err != nil {
		return decimal.Decimal{}, err
	}
	return decimal.NewFromBigInt(raw, -dp), nil
}

// EnsureGas tops target up to wantWei wei of native coin from the hot
// key, so a deposit address can pay gas for a token sweep. No-op when
// the target already holds at least wantWei.
func (c *Client) EnsureGas(ctx context.Context, hotKey []byte, target string, wantWei *big.Int) error {
	bal, err := rpc(c, func(cl *ethclient.Client) (*big.Int, error) {
		return cl.BalanceAt(ctx, common.HexToAddress(target), nil)
	})
	if err != nil {
		return err
	}
	if bal.Cmp(wantWei) >= 0 {
		return nil
	}
	need := new(big.Int).Sub(wantWei, bal)
	amount := weiToDecimal(need)
	_, err = c.TransferNative(ctx, hotKey, target, amount)
	return err
}
