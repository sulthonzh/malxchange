// Package chain defines the seam between the wallet service and the
// chain nodes (issue #22): what watchers scan and what senders
// broadcast. Per-family implementations live in subpackages; the
// interfaces here are the only contracts the wallet core depends on.
package chain

import (
	"context"

	"github.com/shopspring/decimal"
)

// Families.
const (
	BTC  = "btc"
	LTC  = "ltc"
	EVM  = "evm"
	TRON = "tron"
)

// Deposit is one chain-side movement paying a watched address: an
// output on UTXO chains, a log index on EVM/TRON.
type Deposit struct {
	TxID    string
	Vout    int32  // output index (UTXO) or log index (EVM/TRON)
	Address string // the watched deposit address it pays
	Amount  decimal.Decimal
	Height  int64
}

// Spend marks a previously-seen output consumed by a later transaction
// (UTXO families maintain their spend index from these).
type Spend struct {
	TxID    string // the spent output
	Vout    int32
	SpentBy string // the consuming transaction
}

// Watcher is the read-only chain access a deposit watcher drives. The
// watch map keys are watched addresses; implementations match outputs
// (UTXO), transfers (ERC-20/TRC-20 logs), or native value sends
// against it. A nil/empty map still reports spends (UTXO families) so
// the balance index stays exact.
type Watcher interface {
	// Height is the current chain tip.
	Height(ctx context.Context) (int64, error)
	// ScanBlock returns deposits paying watched addresses and the
	// outputs spent within [height]. It is called monotonically
	// (cursor+1..tip) and again during reorg re-scans.
	ScanBlock(ctx context.Context, height int64, watch map[string]string) ([]Deposit, []Spend, error)
	// TxExists reports whether txid is embedded in the block at height
	// under the CURRENT chain (false after a reorg evicted it).
	TxExists(ctx context.Context, txid string, height int64) (bool, error)
	// Depth is the confirmation depth of a known transaction (tip -
	// inclusion_height + 1); -1 when the chain no longer knows it.
	Depth(ctx context.Context, txid string) (int64, error)
	// Decimals returns the on-chain dp for the native coin (contract
	// empty) or the token at contract — the scale check of spec #22.
	Decimals(ctx context.Context, contract string) (int32, error)
}

// FeeQuote is a sender's fee estimate for building transactions.
type FeeQuote struct {
	// PerKByte is the chain fee rate for UTXO families (coin units per
	// 1000 vbytes); unused elsewhere.
	PerKByte decimal.Decimal
}
