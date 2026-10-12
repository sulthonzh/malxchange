// Command wallet is the sole Ledger writer (issue #22): an append-only
// Journal backs every balance movement, fills settle from the
// self-contained trade.fill inbox, deposits credit at per-Coin
// confirmation counts via reorg-aware watchers, and sweeps and
// withdrawals run explicit state machines under encrypted-key custody.
package main

import (
	"github.com/syaikhipin/malxchange/internal/service"
	"github.com/syaikhipin/malxchange/internal/wallet"
)

func main() { service.Main("wallet", wallet.Run) }
