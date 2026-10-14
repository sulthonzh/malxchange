// Run is the admin-api Extra: it mounts the Employee auth surface and
// the installer bootstrap RPC on the boot harness's muxes, then blocks
// until shutdown. RBAC enforcement, the permission catalog, role
// seeding, and Step-up register on these same muxes in #54.
package admin

import (
	"context"

	"github.com/syaikhipin/malxchange/internal/service"
)

func Run(ctx context.Context, d service.Deps) error {
	s := NewServer(d.Pool, LoadAdminEnv(), d.Log)
	s.RegisterPublic(d.Public)
	s.RegisterInternal(d.Internal)
	<-ctx.Done()
	return nil
}
