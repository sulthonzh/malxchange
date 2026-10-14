// Command admin-api serves the back office: Employee auth core (issue
// #53 — TOTP login, absolute+idle sessions, lockout, the middleware
// audit log, the installer bootstrap RPC); RBAC, Step-up, and the
// permission catalog land with #54. The installer drives
// `-bootstrap-first-employee` (one-time credentials on stdin) instead
// of serving.
package main

import (
	"context"
	"os"

	"github.com/syaikhipin/malxchange/internal/admin"
	"github.com/syaikhipin/malxchange/internal/service"
)

func main() {
	if admin.BootstrapRequested() {
		os.Exit(admin.RunBootstrapClient(context.Background()))
		return
	}
	service.Main("admin", admin.Run)
}
