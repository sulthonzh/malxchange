// Market editor + engine status (ticket #61): the admin-api front over
// the exchange's Market-config RPC (ADR-0009's write path — states,
// fees, collars, minimums, the market-order toggle, visibility/sort,
// and tick/lot/scale under the freeze guard) and the extended engine
// status view every admin Role may read. The exchange owns validation,
// the freeze guard, the 5s live-apply, and the market.updated publish;
// this surface adds RBAC + the Audit log and forwards verbatim.
package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// handleListMarkets serves GET /api/markets: the exchange's full
// Market config list, byte-verbatim.
func (s *Server) handleListMarkets(w http.ResponseWriter, r *http.Request) {
	status, raw, err := s.upstreams.exchange.call(r.Context(), http.MethodGet, "/markets", nil, "")
	if err != nil {
		s.relayUpstreamError(w, err, "The exchange service could not be reached.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(bytes.TrimSpace(raw))
}

// handleUpdateMarket serves PUT /api/markets/{market}: the before
// snapshot comes from the config list (the row's canonical JSON), the
// edit body is forwarded verbatim, and the exchange's answer — the
// merged row — is both the response and the audit After.
func (s *Server) handleUpdateMarket(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	market := r.PathValue("market")
	body, ok := readProxyBody(w, r)
	if !ok {
		return
	}
	actor, _ := employeeFrom(r.Context())

	before, err := s.marketRowSnapshot(r, market)
	if err != nil {
		s.relayUpstreamError(w, err, "The exchange service could not be reached.")
		return
	}
	if before == nil {
		WriteError(w, http.StatusNotFound, "Unknown Market \""+market+"\".")
		return
	}

	_, after, err := s.upstreams.exchange.call(r.Context(), http.MethodPut, "/markets/"+market, body, actor.Email)
	if err != nil {
		s.relayUpstreamError(w, err, "The exchange service could not be reached.")
		return
	}

	if entry != nil {
		entry.Target = market
		entry.Before = before
		entry.After = after
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(bytes.TrimSpace(after))
}

// marketRowSnapshot extracts one Market's row JSON from the config
// list — nil when the exchange does not know the Market.
func (s *Server) marketRowSnapshot(r *http.Request, market string) (json.RawMessage, error) {
	_, raw, err := s.upstreams.exchange.call(r.Context(), http.MethodGet, "/markets", nil, "")
	if err != nil {
		return nil, err
	}
	var listed []json.RawMessage
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, err
	}
	for _, row := range listed {
		var probe struct {
			Market string `json:"market"`
		}
		if err := json.Unmarshal(row, &probe); err != nil {
			continue
		}
		if probe.Market == market {
			return row, nil
		}
	}
	return nil, nil
}

// handleEngineStatus serves GET /api/engine: the exchange's engine
// status view (per-Market Ready/open Orders/Halted + consumer lag),
// byte-verbatim. Read-only; every Role's window into the engine.
func (s *Server) handleEngineStatus(w http.ResponseWriter, r *http.Request) {
	status, raw, err := s.upstreams.exchange.call(r.Context(), http.MethodGet, "/engine/status", nil, "")
	if err != nil {
		s.relayUpstreamError(w, err, "The exchange service could not be reached.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(bytes.TrimSpace(raw))
}
