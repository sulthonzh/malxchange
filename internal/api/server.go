// The api service Extra: mounts every member-surface registrar on the
// public mux (Caddy /api/* and /ws) and internal seams on d.Internal.
// The boot harness owns the listener, health, migrations, and the outbox
// relay; this file only wires route registrars together.
package api

import (
	"context"

	"github.com/syaikhipin/malxchange/internal/service"
)

// Run is the api service's Extra. It never returns while the service is
// healthy — the harness's ctx cancel is the only exit.
func Run(ctx context.Context, d service.Deps) error {
	env := LoadAPIEnv()
	mailer := NewMailer(env, d.Log)

	member := &memberAPI{pool: d.Pool, env: env, mailer: mailer, ob: d.Outbox, log: d.Log}
	member.RegisterMember(d.Public)

	// Parallel-ticket registrars mount here as they land (market-data
	// proxy, content proxy, WebSocket hub, funds, orders).

	<-ctx.Done()
	return nil
}
