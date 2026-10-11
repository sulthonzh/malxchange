// Command exchange wraps the matching engine: the order.command fold and
// per-batch transactional persistence (built out in issue #21).
package main

import "github.com/syaikhipin/malxchange/internal/service"

func main() { service.Main("exchange", nil) }
