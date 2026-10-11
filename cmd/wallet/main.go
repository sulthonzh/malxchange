// Command wallet is the sole Ledger writer: deposits, withdrawals,
// custody, sweeps (built out in issue #22).
package main

import "github.com/syaikhipin/malxchange/internal/service"

func main() { service.Main("wallet", nil) }
