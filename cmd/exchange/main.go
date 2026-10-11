// Command exchange runs the matching engine: the order.command fold with
// per-batch transactional persistence, recovery from Postgres, the TTL
// sweeper, and the internal status RPC (issue #21, ADR-0009/0010).
package main

import (
	"github.com/syaikhipin/malxchange/internal/exchange"
	"github.com/syaikhipin/malxchange/internal/service"
)

func main() { service.Main("exchange", exchange.Run) }
