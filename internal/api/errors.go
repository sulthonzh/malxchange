package api

import (
	"encoding/json"
	"net/http"
)

// The public error envelope (spec #24): JSON {"message": "<English
// sentence>"} with the HTTP status. The web app's api client renders
// body.message verbatim (apps/web/src/composables/useApi.ts), and the
// backend carries zero locale state (ADR-0013) — every message is a
// complete English sentence. Internal service-to-service RPC keeps its
// own {"error": ...} shape; this one is member-facing only.
func WriteError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

// writeJSON emits a success body as plain JSON (the web api client
// returns the parsed body as-is; lists are arrays, never wrapped).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
