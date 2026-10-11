// Command admin-api serves the back office: Employee auth, RBAC, Step-up,
// the middleware audit log, and internal admin RPCs (built out in #25).
package main

import "github.com/syaikhipin/malxchange/internal/service"

func main() { service.Main("admin", nil) }
