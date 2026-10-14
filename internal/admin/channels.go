// The Code channel surface (ticket #62, ADR-0006): the super-admin
// view over the api service's channel registry — the status list of
// the SMS/WhatsApp/Telegram channels, activation with fresh per-driver
// credentials behind Step-up, and deactivation. Email is always on and
// is not managed here. admin-api owns the permission gate, the Step-up
// on the credential save (ADR-0012), and the Audit rows; channel state
// and per-driver credential validation stay in api — the registry's
// single home, so a Member's delivery path flips without any code
// change. Credentials are write-only end to end: they pass through to
// api, never rest in admin-api, and never render back on any response.
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// errAPIDown marks an unreachable or slow api service; the surface
// answers one honest 503 sentence and logs the cause.
var errAPIDown = errors.New("api rpc unreachable")

// apiUpstreamError is one api error answer: its HTTP status and the
// message from the {"error": …} body. The message passes through to
// the client — api's per-driver credential refusal is exactly what the
// operator needs to fix the form.
type apiUpstreamError struct {
	Status  int
	Message string
}

func (e *apiUpstreamError) Error() string {
	return fmt.Sprintf("api %d: %s", e.Status, e.Message)
}

// apiClient is the typed client for the api service's internal RPCs
// (internal/api/channels.go RegisterChannelAdmin). Paths are
// prefix-free — api's boot harness strips /internal — so the base URL
// carries the /internal suffix, like every other service-to-service
// base URL in deploy/env.example.
type apiClient struct {
	base string
	http *http.Client
}

func newAPIClient(env *AdminEnv) *apiClient {
	return &apiClient{
		base: strings.TrimRight(env.APIBaseURL, "/"),
		http: &http.Client{Timeout: env.APITimeout},
	}
}

// call performs one JSON call against api's internal RPC, attributing
// the acting Employee on the X-Mal-Actor header (the compose network's
// convention on every mutating internal call). Non-2xx answers come
// back as *apiUpstreamError, transport failures as errAPIDown.
func (c *apiClient) call(ctx context.Context, method, path, actor string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return errAPIDown
	}
	if actor != "" {
		req.Header.Set("X-Mal-Actor", actor)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errAPIDown
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return errAPIDown
	}
	if resp.StatusCode >= 400 {
		var env struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &env) == nil && env.Error != "" {
			msg = env.Error
		}
		return &apiUpstreamError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("admin: decode api answer: %w", err)
		}
	}
	return nil
}

// channelStatus is one registry row as the admin surface serves it:
// status only — credentials never render back on any response (ticket
// #62 acceptance; api's own list serves the same shape).
type channelStatus struct {
	Channel     string  `json:"channel"`
	Active      bool    `json:"active"`
	ActivatedAt *string `json:"activatedAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

// handleListChannels answers the channel status list through api.
func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	var states []channelStatus
	if err := s.api.call(r.Context(), http.MethodGet, "/channels", "", nil, &states); err != nil {
		s.writeChannelsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, states)
}

type activateChannelRequest struct {
	Credentials json.RawMessage `json:"credentials"`
	Code        string          `json:"code"`
}

// handleActivateChannel flips a channel on with fresh per-driver
// credentials. The credential save demands Step-up (ADR-0012: a fresh
// authenticator code of the caller at action time); a missing or stale
// code is refused before api is ever called. Invalid credentials are
// api's refusal to surface — status and sentence pass through.
func (s *Server) handleActivateChannel(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req activateChannelRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.requireStepUp(w, r, req.Code) {
		return
	}
	actor, _ := employeeFrom(r.Context())
	name := r.PathValue("name")
	in := struct {
		Credentials json.RawMessage `json:"credentials"`
	}{Credentials: req.Credentials}
	if err := s.api.call(r.Context(), http.MethodPost, "/channels/"+name+"/activate", actor.Email, in, nil); err != nil {
		s.writeChannelsErr(w, err)
		return
	}
	if entry != nil {
		entry.Target = name
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeactivateChannel flips a channel off. No Step-up — no
// credential moves — but the flip is audited by construction: the
// registry middleware wraps every mutating endpoint, and the handler
// names the channel as the target. Deactivating clears the stored
// credentials in api, so an off channel keeps no secrets.
func (s *Server) handleDeactivateChannel(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	actor, _ := employeeFrom(r.Context())
	name := r.PathValue("name")
	if err := s.api.call(r.Context(), http.MethodPost, "/channels/"+name+"/deactivate", actor.Email, nil, nil); err != nil {
		s.writeChannelsErr(w, err)
		return
	}
	if entry != nil {
		entry.Target = name
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeChannelsErr maps a channel call outcome onto the public
// envelope: api's refusal keeps its status and message (a bad
// credential set arrives as a 400 with the driver-specific sentence),
// and a dead api service is one honest 503.
func (s *Server) writeChannelsErr(w http.ResponseWriter, err error) {
	var upstream *apiUpstreamError
	if errors.As(err, &upstream) {
		WriteError(w, upstream.Status, upstream.Message)
		return
	}
	if s.log != nil {
		s.log.Error("admin channel rpc call failed", "err", err)
	}
	WriteError(w, http.StatusServiceUnavailable, "The channel service is unreachable right now. Try again shortly.")
}
