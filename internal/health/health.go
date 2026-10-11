// Package health provides the uniform liveness/readiness endpoints every
// service exposes on its internal HTTP address (platform spec #20).
//
//	GET /healthz  liveness: process is up
//	GET /readyz   readiness: Postgres reachable (+ optional extra checks)
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Check is a readiness dependency check.
type Check func(ctx context.Context) error

// Handler returns the /healthz and /readyz mux. Checks run with a short
// timeout each; the first failure fails readiness and is reported by name.
func Handler(checks map[string]Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		failed := map[string]string{}
		for name, check := range checks {
			cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
			if err := check(cctx); err != nil {
				failed[name] = err.Error()
			}
			ccancel()
		}
		w.Header().Set("Content-Type", "application/json")
		if len(failed) > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "failed": failed})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	return mux
}
