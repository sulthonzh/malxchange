// Coin editor (ticket #61): the admin-api front over the wallet's
// existing Coin config RPC. admin-api owns RBAC + the Audit log; the
// wallet stays the single source of truth for the Coin row's shape and
// validation, so this surface is a faithful proxy — the edit body is
// forwarded verbatim (one decode point of truth), and the audit
// envelope carries the wallet's own before/after row JSON (the detail
// wire never includes the xpub — a write-only custody field).
package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// maxProxyBody bounds a forwarded edit body (a Coin or Market row is
// orders of magnitude smaller; the cap is an abuse bound, not a shape).
const maxProxyBody = 1 << 16

// readProxyBody reads the request body within the cap and insists it
// is well-formed JSON (a syntax error is the admin client's bug, not
// the upstream's).
func readProxyBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxProxyBody))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "The request body is too large.")
		return nil, false
	}
	if !json.Valid(raw) {
		WriteError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return nil, false
	}
	return raw, true
}

// relayUpstreamError maps an upstream answer onto the public envelope:
// the upstream's 4xx sentence is the client's real answer (unknown
// Coin, invalid config, the freeze guard), transport failures are one
// honest 502, upstream 5xx become 502 without leaking internals.
func (s *Server) relayUpstreamError(w http.ResponseWriter, err error, downSentence string) {
	var ue *upstreamError
	if errors.As(err, &ue) {
		if ue.Status >= 400 && ue.Status < 500 {
			WriteError(w, ue.Status, ue.Message)
			return
		}
		s.log.Error("admin upstream error", "service", ue.Service, "status", ue.Status, "err", ue.Message)
	}
	WriteError(w, http.StatusBadGateway, downSentence)
}

// handleListCoins serves GET /api/coins: the wallet's full Coin config
// list, byte-verbatim.
func (s *Server) handleListCoins(w http.ResponseWriter, r *http.Request) {
	status, raw, err := s.upstreams.wallet.call(r.Context(), http.MethodGet, "/coins", nil, "")
	if err != nil {
		s.relayUpstreamError(w, err, "The wallet service could not be reached.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(bytes.TrimSpace(raw))
}

// handleUpdateCoin serves PUT /api/coins/{name}: snapshot → forward →
// snapshot, with the wallet's own before/after rows as the audit view.
func (s *Server) handleUpdateCoin(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	name := r.PathValue("name")
	body, ok := readProxyBody(w, r)
	if !ok {
		return
	}
	actor, _ := employeeFrom(r.Context())

	_, before, err := s.upstreams.wallet.call(r.Context(), http.MethodGet, "/coins/"+name, nil, "")
	if err != nil {
		s.relayUpstreamError(w, err, "The wallet service could not be reached.")
		return
	}

	if _, _, err := s.upstreams.wallet.call(r.Context(), http.MethodPut, "/coins/"+name, body, actor.Email); err != nil {
		s.relayUpstreamError(w, err, "The wallet service could not be reached.")
		return
	}

	_, after, err := s.upstreams.wallet.call(r.Context(), http.MethodGet, "/coins/"+name, nil, "")
	if err != nil {
		// The edit committed; the snapshot miss must not read as a
		// failed edit.
		s.log.Error("admin coin after-snapshot", "coin", name, "err", err)
		writeJSON(w, http.StatusOK, map[string]string{"name": name, "updated": "true"})
		return
	}

	if entry != nil {
		entry.Target = name
		entry.Before = before
		entry.After = after
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(bytes.TrimSpace(after))
}
