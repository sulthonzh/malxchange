// Command api is the only public member surface: auth, the member REST
// under /api/*, the single authenticated WebSocket at /ws, the
// market-data proxy, and the order-submission path (spec #24).
package main

import (
	"github.com/syaikhipin/malxchange/internal/api"
	"github.com/syaikhipin/malxchange/internal/service"
)

func main() { service.Main("api", api.Run) }
