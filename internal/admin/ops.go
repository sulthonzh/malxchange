// The Wallet Ops surface (ticket #60): admin-api's front over wallet's
// internal custody RPCs (ADR-0008) — the ops dashboard (failed
// withdrawal/sweep/hot-transfer counts, ceiling/floor alert states,
// sweep pauses), open incidents with resolve, hot→cold transfers with
// retry, the cold→hot paper trail, and the reconcile view. Permission
// and Step-up are enforced HERE (ADR-0012 matrix: finance +
// super-admin only); wallet trusts the compose network boundary and
// only sees the X-Mal-Actor attribution header on mutating calls.
// Every endpoint fronts exactly one wallet internal RPC; money values
// stay decimal strings end to end (ADR-0005).
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/syaikhipin/malxchange/internal/money"
)

// ---- wallet internal-RPC client (the internal/api/funds.go pattern) ----

// errWalletDown marks an unreachable or slow wallet; Employees get one
// honest 503 sentence, operators get the cause in the log.
var errWalletDown = errors.New("wallet rpc unreachable")

// walletUpstreamError is one wallet error answer: its HTTP status and
// the message from the {"error": …} body (or plain text).
type walletUpstreamError struct {
	Status  int
	Message string
}

func (e *walletUpstreamError) Error() string {
	return fmt.Sprintf("wallet %d: %s", e.Status, e.Message)
}

// walletClient calls the wallet internal RPC. Paths are prefix-free
// (the boot harness strips /internal); the base URL carries the
// /internal suffix, like api's order-path upstreams.
type walletClient struct {
	base string
	http *http.Client
}

func newWalletClient(base string, timeout time.Duration) *walletClient {
	return &walletClient{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: timeout},
	}
}

// call performs one JSON call; non-2xx answers come back as
// *walletUpstreamError, transport failures as errWalletDown. Mutating
// calls go through callAs, which adds the X-Mal-Actor attribution
// header wallet records on custody rows.
func (c *walletClient) call(ctx context.Context, method, path string, in, out any) error {
	return c.do(ctx, method, path, "", in, out)
}

func (c *walletClient) callAs(ctx context.Context, method, path, actor string, in, out any) error {
	return c.do(ctx, method, path, actor, in, out)
}

func (c *walletClient) do(ctx context.Context, method, path, actor string, in, out any) error {
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
		return errWalletDown
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if actor != "" {
		req.Header.Set("X-Mal-Actor", actor)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errWalletDown
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return errWalletDown
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		var env struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Error != "" {
			msg = env.Error
		}
		return &walletUpstreamError{Status: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// writeWalletErr maps one wallet-client failure onto the public error
// envelope: the wallet's own 4xx refusals pass through with their
// message (honest English sentences — unknown Coin, illegal state
// transition), anything else is one 5xx — the wallet, not the caller,
// failed.
func (s *Server) writeWalletErr(w http.ResponseWriter, err error) {
	var ue *walletUpstreamError
	if errors.As(err, &ue) {
		switch ue.Status {
		case http.StatusBadRequest:
			WriteError(w, http.StatusBadRequest, ue.Message)
		case http.StatusNotFound:
			WriteError(w, http.StatusNotFound, ue.Message)
		case http.StatusConflict:
			WriteError(w, http.StatusConflict, ue.Message)
		default:
			if s.log != nil {
				s.log.Error("wallet ops rpc failed", "status", ue.Status, "msg", ue.Message)
			}
			WriteError(w, http.StatusBadGateway, "The wallet service could not complete this request. Try again.")
		}
		return
	}
	if s.log != nil {
		s.log.Error("wallet ops rpc unreachable", "err", err)
	}
	WriteError(w, http.StatusServiceUnavailable, "The wallet service is unreachable. Try again.")
}

// actorOf is the Employee attribution wallet records on custody rows;
// the session gate guarantees an authenticated caller.
func actorOf(r *http.Request) string {
	emp, _ := employeeFrom(r.Context())
	return emp.Email
}

// proxyGet forwards one read-only wallet RPC answer verbatim.
func (s *Server) proxyGet(w http.ResponseWriter, r *http.Request, path string) {
	var out json.RawMessage
	if err := s.opsWallet.call(r.Context(), http.MethodGet, path, nil, &out); err != nil {
		s.writeWalletErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// withFilters appends the non-empty query params the wallet list RPCs
// accept; keys are a fixed allowlist, values are Coin names / states /
// limits and ride url.Values' encoding.
func listPath(base string, filters map[string]string) string {
	q := url.Values{}
	for key, val := range filters {
		if val != "" {
			q.Set(key, val)
		}
	}
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}

// ---- the dashboard: summary + reconcile (read-only) ----

// handleOpsSummary fronts GET /ops/summary: failed
// withdrawals/sweeps/hot-transfers counts, per-Coin hot balances with
// ceiling/floor alert states, open incidents, and sweep-paused Coins.
func (s *Server) handleOpsSummary(w http.ResponseWriter, r *http.Request) {
	s.proxyGet(w, r, "/ops/summary")
}

// handleOpsReconcile fronts GET /reconcile: the Ledger==Journal fold
// invariant plus per-Coin expected-vs-chain hot reconciliation.
func (s *Server) handleOpsReconcile(w http.ResponseWriter, r *http.Request) {
	s.proxyGet(w, r, "/reconcile")
}

// ---- incidents ----

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	s.proxyGet(w, r, listPath("/incidents", map[string]string{"status": r.URL.Query().Get("status")}))
}

// handleResolveIncident closes one open incident — ADR-0008's manual
// adjudication path (a deep-reorg invalidation is never clawed back
// automatically; resolving the incident records the human decision).
func (s *Server) handleResolveIncident(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	id := r.PathValue("id")
	if err := s.opsWallet.callAs(r.Context(), http.MethodPost, "/incidents/"+id+"/resolve",
		actorOf(r), struct{}{}, nil); err != nil {
		s.writeWalletErr(w, err)
		return
	}
	if entry != nil {
		entry.Target = id
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- sweep pause / sweeps ----

// handleSweepPause toggles one Coin's automated sweeps (ADR-0008:
// pausable per Coin by an admin). Body {"paused": bool}.
func (s *Server) handleSweepPause(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req struct {
		Paused *bool `json:"paused"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Paused == nil {
		WriteError(w, http.StatusBadRequest, "A paused value is required.")
		return
	}
	coin := r.PathValue("name")
	if err := s.opsWallet.callAs(r.Context(), http.MethodPost, "/coins/"+coin+"/sweep-pause",
		actorOf(r), map[string]bool{"paused": *req.Paused}, nil); err != nil {
		s.writeWalletErr(w, err)
		return
	}
	if entry != nil {
		entry.Target = coin
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleListSweeps(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.proxyGet(w, r, listPath("/sweeps", map[string]string{
		"coin": q.Get("coin"), "state": q.Get("state"), "limit": q.Get("limit"),
	}))
}

// handleSweepRetry re-arms a failed sweep — Step-up (ADR-0012: Wallet
// Ops retry).
func (s *Server) handleSweepRetry(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req codeOnlyRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.requireStepUp(w, r, req.Code) {
		return
	}
	id := r.PathValue("id")
	if err := s.opsWallet.callAs(r.Context(), http.MethodPost, "/sweeps/"+id+"/retry",
		actorOf(r), struct{}{}, nil); err != nil {
		s.writeWalletErr(w, err)
		return
	}
	if entry != nil {
		entry.Target = id
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- hot→cold transfers ----

func (s *Server) handleListHotTransfers(w http.ResponseWriter, r *http.Request) {
	s.proxyGet(w, r, listPath("/hot-transfers", map[string]string{"coin": r.URL.Query().Get("coin")}))
}

// handleCreateHotTransfer opens a hot→cold movement — Step-up
// (ADR-0012: hot→cold). The wallet signs it like a withdrawal to the
// Coin's configured cold address; the suggested trigger is the
// dashboard's hot_ceiling alert.
func (s *Server) handleCreateHotTransfer(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req struct {
		Coin   string `json:"coin"`
		Amount string `json:"amount"`
		Code   string `json:"code"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.requireStepUp(w, r, req.Code) {
		return
	}
	if req.Coin == "" || req.Amount == "" {
		WriteError(w, http.StatusBadRequest, "A Coin and an amount are required.")
		return
	}
	amount, err := money.Parse(req.Amount)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "amount: "+err.Error())
		return
	}
	if amount.Sign() <= 0 {
		WriteError(w, http.StatusBadRequest, "The amount must be positive.")
		return
	}
	var out struct {
		ID    string `json:"id"`
		Coin  string `json:"coin"`
		State string `json:"state"`
	}
	if err := s.opsWallet.callAs(r.Context(), http.MethodPost, "/hot-transfers", actorOf(r),
		map[string]string{"coin": req.Coin, "amount": amount.String()}, &out); err != nil {
		s.writeWalletErr(w, err)
		return
	}
	if entry != nil {
		entry.Target = out.ID
	}
	writeJSON(w, http.StatusOK, out)
}

// handleHotTransferRetry re-arms a failed hot→cold transfer — Step-up
// (ADR-0012: retry). Wallet-side this fronts the previously unrouted
// RetryHotTransfer.
func (s *Server) handleHotTransferRetry(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req codeOnlyRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.requireStepUp(w, r, req.Code) {
		return
	}
	id := r.PathValue("id")
	if err := s.opsWallet.callAs(r.Context(), http.MethodPost, "/hot-transfers/"+id+"/retry",
		actorOf(r), struct{}{}, nil); err != nil {
		s.writeWalletErr(w, err)
		return
	}
	if entry != nil {
		entry.Target = id
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- cold→hot records ----

func (s *Server) handleListColdTransfers(w http.ResponseWriter, r *http.Request) {
	s.proxyGet(w, r, listPath("/cold-transfers", map[string]string{"coin": r.URL.Query().Get("coin")}))
}

// handleRecordColdTransfer appends the offline cold→hot procedure's
// paper trail: the Employee performed the transfer offline and records
// its chain txid (ADR-0008).
func (s *Server) handleRecordColdTransfer(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req struct {
		Coin   string `json:"coin"`
		Amount string `json:"amount"`
		TxID   string `json:"txid"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Coin == "" || req.Amount == "" || req.TxID == "" {
		WriteError(w, http.StatusBadRequest, "A Coin, an amount, and the chain txid are required.")
		return
	}
	amount, err := money.Parse(req.Amount)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "amount: "+err.Error())
		return
	}
	if amount.Sign() <= 0 {
		WriteError(w, http.StatusBadRequest, "The amount must be positive.")
		return
	}
	if err := s.opsWallet.callAs(r.Context(), http.MethodPost, "/cold-transfers", actorOf(r),
		map[string]string{"coin": req.Coin, "amount": amount.String(), "txid": req.TxID}, nil); err != nil {
		s.writeWalletErr(w, err)
		return
	}
	if entry != nil {
		entry.Target = req.TxID
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
