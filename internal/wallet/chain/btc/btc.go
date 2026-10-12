// Package btc is the BTC/LTC JSON-RPC client: watcher reads, UTXO
// access, and local P2WPKH signing (issue #22). LTC transactions are
// byte-identical to BTC's; only the bech32 HRP and base58 version
// bytes differ, so one client serves both families and ltcd is never
// linked (it would drag cgo secp256k1 into the CGO_ENABLED=0
// distroless build).
package btc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/btcsuite/btcd/wire"
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/wallet/chain"
)

// Compile-time check: the RPC satisfies the watcher seam.
var _ chain.Watcher = (*RPC)(nil)

// Families (mirror chain constants; kept local so this package has
// no dependency direction beyond the seam types).
const (
	famBTC = "btc"
	famLTC = "ltc"
)

// Networks.
const (
	netMain = "mainnet"
	netSim  = "simnet"
)

// Sentinel errors surfaced to the wallet core.
var (
	// ErrNotFound maps RPC error -5: the tx/block is unknown to the
	// node (reorged out or never mined).
	ErrNotFound = errors.New("not found on chain")
	// ErrReorg is the ScanBlock guard: the height mapped to a
	// different hash between getblockhash and getblock, so the
	// caller must re-scan from its checkpoint.
	ErrReorg = errors.New("block hash changed under scan (reorg)")
	// ErrBadFamily is a construction with a non-UTXO family.
	ErrBadFamily = errors.New("unknown UTXO family")
)

// segwitHRP is the bech32 HRP per family/network.
var segwitHRP = map[[2]string]string{
	{famBTC, netMain}: "bc",
	{famBTC, netSim}:  "sb",
	{famLTC, netMain}: "ltc",
	{famLTC, netSim}:  "sltc",
}

// base58Versions is [family]net -> {p2pkh, p2sh} address version
// bytes. Legacy forms are best-effort (sweeping imported old funds);
// deposits themselves are always P2WPKH.
var base58Versions = map[[2]string][2]byte{
	{famBTC, netMain}: {0x00, 0x05},
	{famBTC, netSim}:  {0x3f, 0x7b},
	{famLTC, netMain}: {0x30, 0x32},
	{famLTC, netSim}:  {0x3f, 0x7b}, // simnet shares BTC's bytes
}

// hrpOf returns the family/network bech32 HRP.
func hrpOf(family, net string) (string, error) {
	h, ok := segwitHRP[[2]string{family, net}]
	if !ok {
		return "", fmt.Errorf("%w: %s/%s", ErrBadFamily, family, net)
	}
	return h, nil
}

// rpcError is a JSON-RPC error object; code -5 means "not found".
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// RPC is the JSON-RPC 1.0 client for one btcd/ltcd node. It is safe
// for concurrent use.
type RPC struct {
	family string
	url    string
	user   string
	pass   string
	http   *http.Client

	// net is the network the node reports ("mainnet"/"simnet"),
	// resolved lazily because NewRPC deliberately does no I/O; the
	// HRP and base58 version bytes for address decoding depend on it.
	netOnce sync.Once
	net     string
}

// NewRPC builds a client for family ("btc"|"ltc"). tlsSkipVerify
// accepts the simnet nodes' self-signed certificates.
func NewRPC(family, url, user, pass string, tlsSkipVerify bool) (*RPC, error) {
	if family != famBTC && family != famLTC {
		return nil, fmt.Errorf("%w: %s", ErrBadFamily, family)
	}
	transport := &http.Transport{}
	if tlsSkipVerify {
		// Simnet only: the compose stack ships self-signed certs.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &RPC{
		family: family,
		url:    url,
		user:   user,
		pass:   pass,
		http:   &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}, nil
}

// call performs one JSON-RPC 1.0 request, decoding .result into out.
// A nil out discards the result.
func (r *RPC) call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "1.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(r.user, r.pass)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rpc %s: http %d: %s", method, resp.StatusCode, truncate(raw))
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("rpc %s: bad envelope: %w", method, err)
	}
	if env.Error != nil {
		if env.Error.Code == -5 {
			return fmt.Errorf("%w: %s", ErrNotFound, env.Error.Message)
		}
		return env.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

func truncate(b []byte) string {
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}

// resolveNet pins the network once via getblockchaininfo. An
// unreachable node keeps the mainnet default: whichever RPC call
// actually needs the node surfaces the connectivity error itself,
// and BuildSign (which makes no node calls) works fully offline
// against mainnet address forms.
func (r *RPC) resolveNet(ctx context.Context) (string, error) {
	r.netOnce.Do(func() {
		r.net = netMain
		var info struct {
			Chain string `json:"chain"`
		}
		if err := r.call(ctx, "getblockchaininfo", nil, &info); err != nil {
			return
		}
		if info.Chain == "simnet" || info.Chain == "regtest" {
			r.net = netSim
		}
	})
	return r.net, nil
}

// Height is the current chain tip.
func (r *RPC) Height(ctx context.Context) (int64, error) {
	var h int64
	if err := r.call(ctx, "getblockcount", nil, &h); err != nil {
		return 0, err
	}
	return h, nil
}

// verboseTx is the subset of verbose transaction JSON consumed here.
// Amounts NEVER come from the float "value" fields — the raw tx hex
// is decoded for exact satoshis (spec #22 money rule).
type verboseTx struct {
	TxID string `json:"txid"`
	Hex  string `json:"hex"`
	Vin  []struct {
		TxID string `json:"txid"`
		Vout uint32 `json:"vout"`
	} `json:"vin"`
}

// verboseBlock is the subset of verbose block JSON consumed. btcd
// puts verbose tx objects under "rawtx" at verbosity 2, txid strings
// under "tx" at verbosity 1 — both shapes are accepted.
type verboseBlock struct {
	Hash  string      `json:"hash"`
	Tx    []string    `json:"tx"`
	RawTx []verboseTx `json:"rawtx"`
}

// ScanBlock returns deposits paying watched addresses plus every
// output consumed within the block (the caller's spend-index feed;
// still reported for an empty watch map).
func (r *RPC) ScanBlock(ctx context.Context, height int64, watch map[string]string) ([]chain.Deposit, []chain.Spend, error) {
	net, err := r.resolveNet(ctx)
	if err != nil {
		return nil, nil, err
	}
	var want string
	if err := r.call(ctx, "getblockhash", []any{height}, &want); err != nil {
		return nil, nil, err
	}
	var blk verboseBlock
	if err := r.call(ctx, "getblock", []any{want, 2}, &blk); err != nil {
		return nil, nil, err
	}
	// Reorg guard: the height now maps to a different block than
	// the hash fetched a moment ago — caller must re-scan.
	if blk.Hash != want {
		return nil, nil, fmt.Errorf("%w: height %d: %s != %s", ErrReorg, height, blk.Hash, want)
	}
	var (
		deps   []chain.Deposit
		spends []chain.Spend
	)
	// Normalize to verbose tx objects; verbosity-1 blocks fetch no
	// hex, so their entries carry txid only (hex stays empty and
	// decode of empty hex is a caller-visible error — ScanBlock
	// always requests verbosity 2).
	const zero = "0000000000000000000000000000000000000000000000000000000000000000"
	txs := blk.RawTx
	for ti := range txs {
		vtx := &txs[ti]
		tx, err := decodeRawTx(vtx.Hex)
		if err != nil {
			return nil, nil, fmt.Errorf("block %s tx %s: %w", blk.Hash, vtx.TxID, err)
		}
		// Deposits: outputs paying a watched address.
		for v, out := range tx.TxOut {
			addr, ok := DecodeScriptAddr(r.family, net, out.PkScript)
			if !ok {
				continue
			}
			if _, watched := watch[addr]; !watched {
				continue
			}
			deps = append(deps, chain.Deposit{
				TxID:    vtx.TxID,
				Vout:    int32(v),
				Address: addr,
				Amount:  decimal.New(out.Value, -8), // exact sat/1e8
				Height:  height,
			})
		}
		// Spends: every input prevout; the coinbase input's
		// all-zero prev txid is skipped.
		for _, vin := range vtx.Vin {
			if vin.TxID == "" || vin.TxID == zero {
				continue
			}
			spends = append(spends, chain.Spend{
				TxID:    vin.TxID,
				Vout:    int32(vin.Vout),
				SpentBy: vtx.TxID,
			})
		}
	}
	return deps, spends, nil
}

// decodeRawTx parses raw tx hex (integer satoshis, never floats).
func decodeRawTx(hexStr string) (*wire.MsgTx, error) {
	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("raw tx hex: %w", err)
	}
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("decode raw tx: %w", err)
	}
	return &tx, nil
}

// TxExists reports whether txid is embedded in the block at height
// under the CURRENT chain. A missing block (reorged out) is a plain
// false, not an error.
func (r *RPC) TxExists(ctx context.Context, txid string, height int64) (bool, error) {
	var hash string
	if err := r.call(ctx, "getblockhash", []any{height}, &hash); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	var blk struct {
		Tx []string `json:"tx"`
	}
	if err := r.call(ctx, "getblock", []any{hash, 1}, &blk); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	for _, id := range blk.Tx {
		if id == txid {
			return true, nil
		}
	}
	return false, nil
}

// Depth is the confirmation count of a known tx; -1 when the chain
// no longer knows it (reorged out / mempool-only).
func (r *RPC) Depth(ctx context.Context, txid string) (int64, error) {
	var verbose struct {
		Confirmations int64 `json:"confirmations"`
	}
	err := r.call(ctx, "getrawtransaction", []any{txid, 1}, &verbose)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return -1, nil
		}
		return -1, err
	}
	if verbose.Confirmations <= 0 {
		return -1, nil // 0 confirmations = mempool only
	}
	return verbose.Confirmations, nil
}

// Decimals is always 8 for BTC/LTC (contract arg unused).
func (r *RPC) Decimals(ctx context.Context, contract string) (int32, error) {
	return 8, nil
}
