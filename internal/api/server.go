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

	channels := &channelAPI{pool: d.Pool, env: env, log: d.Log}
	channels.RegisterChannels(d.Public)
	channels.RegisterChannelAdmin(d.Internal)

	RegisterContent(d.Public, LoadContentConfig())

	newOrderAPI(d.Pool, env, d.Outbox, d.Log, LoadOrderConfig()).RegisterOrders(d.Public)

	// Anonymous market data (ticket #43): market's internal read surface
	// proxied one to one under /api/market — the exchange is explorable
	// before signup.
	RegisterMarketProxy(d.Public, LoadMarketProxyConfig())

	// Parallel-ticket registrars mount here as they land (WebSocket hub,
	// funds).

	<-ctx.Done()
	return nil
}
