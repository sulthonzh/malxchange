// Run is the admin-api Extra: it seeds the RBAC catalog and matrix
// grants (idempotent, from the endpoint registry), mounts the Employee
// auth surface and the installer bootstrap RPC on the boot harness's
// muxes, then blocks until shutdown.
package admin

import (
	"context"
	"fmt"

	"github.com/syaikhipin/malxchange/internal/service"
)

func Run(ctx context.Context, d service.Deps) error {
	s := NewServer(d.Pool, LoadAdminEnv(), d.Log)
	if err := s.SeedRBAC(ctx); err != nil {
		return fmt.Errorf("admin: seed rbac: %w", err)
	}
	s.RegisterPublic(d.Public)
	s.RegisterInternal(d.Internal)
	<-ctx.Done()
	return nil
}
