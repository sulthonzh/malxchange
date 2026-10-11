// Package migrations embeds every service's SQL migrations. Each service
// runs its own subset at boot (embedded golang-migrate, ADR-0007); there
// is no separate migrate job and single-replica deployment means no race.
package migrations

import "embed"

//go:embed */*.sql
var FS embed.FS

// Services lists the directories that exist — one per service binary.
var Services = []string{"api", "exchange", "market", "wallet", "admin"}
