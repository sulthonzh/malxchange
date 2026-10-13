// Member funds surface (spec #24 ticket #40): the REST routes the web
// app's member center drives — Ledger balances per Coin, deposit
// addresses, detected→credited deposits with confirmation counts, the
// Journal records list, the withdrawal address book (api-owned), the
// withdrawal fee preview, and the gated withdrawal itself. The wallet
// service is the only funds source of truth (ADR-0008): api proxies
// its internal RPC and holds no Ledger copy. Every money value is a
// decimal string end to end (ADR-0005) — parsed for validation, never
// re-serialized.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/money"
)

// FundsConfig is the operator-tunable wallet wiring (the LoadAPIEnv
// pattern): where the wallet's internal RPC lives and how long one
// proxied call may take.
type FundsConfig struct {
	WalletBaseURL string
	Timeout       time.Duration
}

// LoadFundsConfig reads MAL_API_WALLET_* once per process (listed in
// deploy/env.example; the compose network is the auth boundary, so the
// URL never crosses Caddy).
func LoadFundsConfig() FundsConfig {
	timeout := 5 * time.Second
	if raw := config.EnvDefault("MAL_API_WALLET_TIMEOUT", ""); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			timeout = d
		}
	}
	return FundsConfig{
		WalletBaseURL: config.EnvDefault("MAL_API_WALLET_BASE_URL", "http://wallet:8080"),
		Timeout:       timeout,
	}
}

// ---- the wallet internal-RPC client ----

// errWalletDown marks an unreachable or slow wallet; members get one
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

// walletClient is the typed client for the member-facing seams of the
// wallet's internal RPC (backend-wires.md §1). Paths carry the
// documented /internal prefix; the base URL is the service origin.
type walletClient struct {
	base string
	http *http.Client
}

func newWalletClient(cfg FundsConfig, log *slog.Logger) *walletClient {
	return &walletClient{
		base: strings.TrimRight(cfg.WalletBaseURL, "/"),
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// call performs one JSON call; non-2xx answers come back as
// *walletUpstreamError, transport failures as errWalletDown.
func (c *walletClient) call(ctx context.Context, method, path string, in, out any) error {
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

// walletAccount is one Ledger account row (wire keys as the wallet
// serves them; also the member-facing shape).
type walletAccount struct {
	Coin      string `json:"coin"`
	Available string `json:"available"`
	Frozen    string `json:"frozen"`
}

// walletAddress is the deposit-address answer.
type walletAddress struct {
	Coin    string `json:"coin"`
	Address string `json:"address"`
}

// walletDeposit is one member-visible deposit with the live
// confirmation counter (wire keys as the wallet serves them).
type walletDeposit struct {
	Coin          string `json:"coin"`
	TxID          string `json:"txid"`
	Vout          int32  `json:"vout"`
	Amount        string `json:"amount"`
	Confirmations int    `json:"confirmations"`
	Needed        int    `json:"needed"`
	Status        string `json:"status"`
	DetectedAt    string `json:"detectedAt"`
}

// walletJournalLine mirrors the wallet's Journal JSON, whose struct
// carries no json tags — the exported field names ARE the wire keys.
type walletJournalLine struct {
	Coin        string
	Kind        string
	Amount      string
	AvailDelta  string
	FrozenDelta string
	Reason      string
	Ref         string
	At          string
}

// walletCoin is the Coin configuration the withdrawal path needs
// (subset of GET /internal/coins/{name}).
type walletCoin struct {
	Name         string `json:"name"`
	Scale        int32  `json:"scale"`
	WithdrawFee  string `json:"withdrawFee"`
	MinWithdraw  string `json:"minWithdraw"`
	Withdrawable bool   `json:"withdrawable"`
}

// walletWithdrawal is the wallet's withdrawal-create answer (wire keys
// as the wallet serves them; also the member-facing shape).
type walletWithdrawal struct {
	ID     string `json:"id"`
	Coin   string `json:"coin"`
	Amount string `json:"amount"`
	Fee    string `json:"fee"`
	Net    string `json:"net"`
	State  string `json:"state"`
}

func (c *walletClient) accounts(ctx context.Context, member string) ([]walletAccount, error) {
	var out []walletAccount
	err := c.call(ctx, http.MethodGet, "/internal/accounts/"+url.PathEscape(member), nil, &out)
	return out, err
}

func (c *walletClient) depositAddress(ctx context.Context, member, coin string) (walletAddress, error) {
	var out walletAddress
	err := c.call(ctx, http.MethodPost, "/internal/deposit-addresses",
		map[string]string{"member": member, "coin": coin}, &out)
	return out, err
}

func (c *walletClient) deposits(ctx context.Context, member string) ([]walletDeposit, error) {
	var out []walletDeposit
	err := c.call(ctx, http.MethodGet, "/internal/deposits?member="+url.QueryEscape(member), nil, &out)
	return out, err
}

func (c *walletClient) journal(ctx context.Context, member, coin string) ([]walletJournalLine, error) {
	path := "/internal/journal/" + url.PathEscape(member)
	if coin != "" {
		path += "?coin=" + url.QueryEscape(coin)
	}
	var out []walletJournalLine
	err := c.call(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *walletClient) coin(ctx context.Context, name string) (walletCoin, error) {
	var out walletCoin
	err := c.call(ctx, http.MethodGet, "/internal/coins/"+url.PathEscape(name), nil, &out)
	return out, err
}

func (c *walletClient) withdraw(ctx context.Context, member, coin, address, amount string) (walletWithdrawal, error) {
	var out walletWithdrawal
	err := c.call(ctx, http.MethodPost, "/internal/withdrawals", struct {
		Member  string `json:"member"`
		Coin    string `json:"coin"`
		Address string `json:"address"`
		Amount  string `json:"amount"`
	}{Member: member, Coin: coin, Address: address, Amount: amount}, &out)
	return out, err
}

// ---- the withdrawal gate seam (ticket #39) ----

// memberGate is the member-identity half of the withdrawal gate:
// KYC state, Authenticator binding, and live Authenticator code
// verification (GLOSSARY terms; avoid "2FA"). Ticket #39 owns the
// implementation against the KYC applications and TOTP bindings; until
// it lands, closedGate answers no to everything, so the withdrawal
// path is fully wired and tested but stays closed in production.
type memberGate interface {
	KYCVerified(ctx context.Context, memberID string) (bool, error)
	AuthenticatorBound(ctx context.Context, memberID string) (bool, error)
	VerifyAuthenticatorCode(ctx context.Context, memberID, code string) (bool, error)
}

// closedGate is the pre-#39 gate: every answer is no.
type closedGate struct{}

func (closedGate) KYCVerified(context.Context, string) (bool, error)        { return false, nil }
func (closedGate) AuthenticatorBound(context.Context, string) (bool, error) { return false, nil }
func (closedGate) VerifyAuthenticatorCode(context.Context, string, string) (bool, error) {
	return false, nil
}

// ---- the funds routes ----

// sessionAuth is the member-only wrapper members.go owns; the funds
// routes ride the same session seam as the identity surface.
type sessionAuth func(next func(w http.ResponseWriter, r *http.Request, s Session)) http.HandlerFunc

// fundsAPI carries the collaborators of the funds routes.
type fundsAPI struct {
	pool     *pgxpool.Pool
	wallet   *walletClient
	gate     memberGate
	sessions sessionAuth
	log      *slog.Logger
}

func newFundsAPI(pool *pgxpool.Pool, wallet *walletClient, gate memberGate, sessions sessionAuth, log *slog.Logger) *fundsAPI {
	return &fundsAPI{pool: pool, wallet: wallet, gate: gate, sessions: sessions, log: log}
}

// Register mounts the funds routes on the PUBLIC mux (Caddy proxies
// /api/* without stripping). The /api/uc/wallet segment keeps the
// funds surfaces beside the identity routes the web app's member
// center already drives.
func (f *fundsAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/uc/wallet/accounts", f.sessions(f.handleAccounts))
	mux.HandleFunc("POST /api/uc/wallet/deposit-address", f.sessions(f.handleDepositAddress))
	mux.HandleFunc("GET /api/uc/wallet/deposits", f.sessions(f.handleDeposits))
	mux.HandleFunc("GET /api/uc/wallet/records", f.sessions(f.handleRecords))
	mux.HandleFunc("GET /api/uc/wallet/withdraw-fee", f.sessions(f.handleWithdrawFee))
	mux.HandleFunc("POST /api/uc/wallet/withdraw", f.sessions(f.handleWithdraw))
	mux.HandleFunc("GET /api/uc/withdraw-addresses", f.sessions(f.handleListWithdrawAddresses))
	mux.HandleFunc("POST /api/uc/withdraw-addresses", f.sessions(f.handleAddWithdrawAddress))
	mux.HandleFunc("PUT /api/uc/withdraw-addresses/{id}", f.sessions(f.handleUpdateWithdrawAddress))
	mux.HandleFunc("DELETE /api/uc/withdraw-addresses/{id}", f.sessions(f.handleDeleteWithdrawAddress))
}

// handleAccounts answers the member's Ledger balances per Coin: the
// wallet's array verbatim (every configured Coin, zeros included,
// decimal strings at the Coin's scale).
func (f *fundsAPI) handleAccounts(w http.ResponseWriter, r *http.Request, s Session) {
	accts, err := f.wallet.accounts(r.Context(), s.MemberID)
	if err != nil {
		writeWalletErr(w, f.log, err)
		return
	}
	if accts == nil {
		accts = []walletAccount{}
	}
	writeJSON(w, http.StatusOK, accts)
}

// handleDepositAddress allocates (or returns) the member's one deposit
// address for a Coin.
func (f *fundsAPI) handleDepositAddress(w http.ResponseWriter, r *http.Request, s Session) {
	var req struct {
		Coin string `json:"coin"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Coin == "" {
		WriteError(w, http.StatusBadRequest, "Choose a Coin for the deposit address.")
		return
	}
	addr, err := f.wallet.depositAddress(r.Context(), s.MemberID, req.Coin)
	if err != nil {
		writeWalletErr(w, f.log, err)
		return
	}
	writeJSON(w, http.StatusOK, addr)
}

// handleDeposits answers the member's deposits with state and the live
// confirmation count (the wallet's array verbatim, [] never null).
func (f *fundsAPI) handleDeposits(w http.ResponseWriter, r *http.Request, s Session) {
	list, err := f.wallet.deposits(r.Context(), s.MemberID)
	if err != nil {
		writeWalletErr(w, f.log, err)
		return
	}
	if list == nil {
		list = []walletDeposit{}
	}
	writeJSON(w, http.StatusOK, list)
}

// journalEntry is one Journal line in the member-facing shape: the
// wallet's internal keys re-titled (its struct has none), money values
// verbatim.
type journalEntry struct {
	Coin        string `json:"coin"`
	Kind        string `json:"kind"`
	Amount      string `json:"amount"`
	AvailDelta  string `json:"availDelta"`
	FrozenDelta string `json:"frozenDelta"`
	Reason      string `json:"reason"`
	Ref         string `json:"ref"`
	At          string `json:"at"`
}

// pgTimestampLayout parses the raw timestamptz text the wallet's
// Journal surface emits ("2026-10-13 09:00:00.123456+00").
const pgTimestampLayout = "2006-01-02 15:04:05.999999999-07"

// handleRecords answers the member's Journal (GLOSSARY: Journal, not
// statement) — every Ledger movement with its reason, optional Coin
// filter.
func (f *fundsAPI) handleRecords(w http.ResponseWriter, r *http.Request, s Session) {
	lines, err := f.wallet.journal(r.Context(), s.MemberID, r.URL.Query().Get("coin"))
	if err != nil {
		writeWalletErr(w, f.log, err)
		return
	}
	out := make([]journalEntry, 0, len(lines))
	for _, l := range lines {
		at, err := time.Parse(pgTimestampLayout, l.At)
		if err != nil {
			writeErr(w, f.log, fmt.Errorf("records: wallet journal timestamp %q: %w", l.At, err))
			return
		}
		out = append(out, journalEntry{
			Coin: l.Coin, Kind: l.Kind, Amount: l.Amount,
			AvailDelta: l.AvailDelta, FrozenDelta: l.FrozenDelta,
			Reason: l.Reason, Ref: l.Ref, At: formatTime(at),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- error mapping ----

// writeWalletErr maps one wallet failure to the public envelope. The
// wallet's named domain errors (its writeErr sentinels, wrapped with
// detail in the message) become complete English sentences; anything
// unexpected is one honest 502, never a leaked internal string.
func writeWalletErr(w http.ResponseWriter, log *slog.Logger, err error) {
	var up *walletUpstreamError
	if !errors.As(err, &up) {
		log.Error("api funds: wallet unreachable", "err", err)
		WriteError(w, http.StatusServiceUnavailable,
			"The funds service is unavailable. Try again shortly.")
		return
	}
	switch {
	case up.Status == http.StatusConflict:
		WriteError(w, http.StatusConflict, "There are not enough available funds for this withdrawal.")
	case strings.HasPrefix(up.Message, "below minimum withdrawal"):
		WriteError(w, http.StatusBadRequest, "The amount is below the minimum withdrawal for this Coin.")
	case strings.HasPrefix(up.Message, "coin is not withdrawable"):
		WriteError(w, http.StatusBadRequest, "This Coin cannot be withdrawn right now.")
	case strings.HasPrefix(up.Message, "withdrawal fee exceeds amount"):
		WriteError(w, http.StatusBadRequest, "The withdrawal fee meets or exceeds the amount.")
	case strings.HasPrefix(up.Message, "unknown coin"):
		WriteError(w, http.StatusBadRequest, "That Coin is not available.")
	case up.Status == http.StatusBadRequest:
		WriteError(w, http.StatusBadRequest, "That request could not be completed. Check the amount and address.")
	default:
		log.Error("api funds: wallet error", "status", up.Status, "message", up.Message)
		WriteError(w, http.StatusBadGateway, "The funds service reported a problem. Try again shortly.")
	}
}

// ---- amount validation (ADR-0005) ----

// publicError is a member-facing rejection: status + one English
// sentence, so validation failures map to the public envelope exactly
// like upstream errors.
type publicError struct {
	status  int
	message string
}

func (e *publicError) Error() string { return e.message }

func writePublicErr(w http.ResponseWriter, log *slog.Logger, err error) {
	var pe *publicError
	if errors.As(err, &pe) {
		WriteError(w, pe.status, pe.message)
		return
	}
	writeWalletErr(w, log, err)
}

// checkWithdrawAmount validates a decimal-string amount against the
// Coin's rules — the same ones the wallet enforces authoritatively
// (scale before proxying, per ticket #40; withdrawable, minimum, fee
// so the preview can never promise a submit the wallet would refuse).
// No floats anywhere in the path (ADR-0005).
func checkWithdrawAmount(coin walletCoin, raw string) (decimal.Decimal, error) {
	amount, err := money.Parse(raw)
	if err != nil {
		return decimal.Decimal{}, &publicError{http.StatusBadRequest,
			"Enter the amount as an exact decimal number, like 10.123456."}
	}
	if !coin.Withdrawable {
		return decimal.Decimal{}, &publicError{http.StatusBadRequest, "This Coin cannot be withdrawn right now."}
	}
	if !money.WithinScale(amount, coin.Scale) {
		return decimal.Decimal{}, &publicError{http.StatusBadRequest, fmt.Sprintf(
			"%s accepts at most %d decimal places.", coin.Name, coin.Scale)}
	}
	fee, err := money.Parse(coin.WithdrawFee)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("coin %s fee %q: %w", coin.Name, coin.WithdrawFee, err)
	}
	minimum, err := money.Parse(coin.MinWithdraw)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("coin %s min %q: %w", coin.Name, coin.MinWithdraw, err)
	}
	if amount.IsZero() || amount.LessThan(minimum) {
		return decimal.Decimal{}, &publicError{http.StatusBadRequest, "The amount is below the minimum withdrawal for this Coin."}
	}
	if !amount.GreaterThan(fee) {
		return decimal.Decimal{}, &publicError{http.StatusBadRequest, "The withdrawal fee meets or exceeds the amount."}
	}
	return amount, nil
}

// ---- fee preview + gated withdrawal (ticket #40 stories 14-15) ----

// feePreview is the withdrawal fee preview: exactly what the wallet
// will deduct for this amount — same fee string, same net, so the
// member signs off on the real numbers (acceptance: fee preview equals
// deducted fee).
type feePreview struct {
	Coin   string `json:"coin"`
	Amount string `json:"amount"`
	Fee    string `json:"fee"`
	Net    string `json:"net"`
}

// handleWithdrawFee shows the fee and net for a prospective withdrawal
// before the member submits it. The amount rides back verbatim.
func (f *fundsAPI) handleWithdrawFee(w http.ResponseWriter, r *http.Request, s Session) {
	preview, err := f.previewWithdrawal(r.Context(), r.URL.Query().Get("coin"), r.URL.Query().Get("amount"))
	if err != nil {
		writePublicErr(w, f.log, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (f *fundsAPI) previewWithdrawal(ctx context.Context, coinName, rawAmount string) (feePreview, error) {
	coin, amount, fee, err := f.loadCoinAndAmount(ctx, coinName, rawAmount)
	if err != nil {
		return feePreview{}, err
	}
	return feePreview{
		Coin:   coin.Name,
		Amount: rawAmount,
		Fee:    money.Format(fee, coin.Scale),
		Net:    money.Format(amount.Sub(fee), coin.Scale),
	}, nil
}

// loadCoinAndAmount fetches the Coin config and validates the amount
// against it — the shared front half of preview and submit.
func (f *fundsAPI) loadCoinAndAmount(ctx context.Context, coinName, rawAmount string) (walletCoin, decimal.Decimal, decimal.Decimal, error) {
	if coinName == "" {
		return walletCoin{}, decimal.Decimal{}, decimal.Decimal{},
			&publicError{http.StatusBadRequest, "Choose a Coin."}
	}
	coin, err := f.wallet.coin(ctx, coinName)
	if err != nil {
		return walletCoin{}, decimal.Decimal{}, decimal.Decimal{}, err
	}
	amount, err := checkWithdrawAmount(coin, rawAmount)
	if err != nil {
		return walletCoin{}, decimal.Decimal{}, decimal.Decimal{}, err
	}
	fee, err := money.Parse(coin.WithdrawFee)
	if err != nil {
		return walletCoin{}, decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("coin %s fee: %w", coin.Name, err)
	}
	return coin, amount, fee, nil
}

// handleWithdraw submits a gated withdrawal: KYC verified, an
// Authenticator bound, and a live code presented — only then is the
// amount validated against the Coin's scale and proxied to the wallet,
// which debits and answers with the fee it deducted.
func (f *fundsAPI) handleWithdraw(w http.ResponseWriter, r *http.Request, s Session) {
	var req struct {
		Coin    string `json:"coin"`
		Address string `json:"address"`
		Amount  string `json:"amount"`
		Code    string `json:"code"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Address) == "" {
		WriteError(w, http.StatusBadRequest, "Enter the withdrawal address.")
		return
	}
	// The gate runs before any wallet call, so a rejected request
	// cannot move funds (story 15).
	ctx := r.Context()
	kyc, err := f.gate.KYCVerified(ctx, s.MemberID)
	if err != nil {
		writeErr(w, f.log, err)
		return
	}
	if !kyc {
		WriteError(w, http.StatusForbidden, "Your identity verification must be approved before you can withdraw.")
		return
	}
	bound, err := f.gate.AuthenticatorBound(ctx, s.MemberID)
	if err != nil {
		writeErr(w, f.log, err)
		return
	}
	if !bound {
		WriteError(w, http.StatusForbidden, "Bind an authenticator before you can withdraw.")
		return
	}
	if strings.TrimSpace(req.Code) == "" {
		WriteError(w, http.StatusBadRequest, "Enter the 6-digit code from your authenticator.")
		return
	}
	valid, err := f.gate.VerifyAuthenticatorCode(ctx, s.MemberID, req.Code)
	if err != nil {
		writeErr(w, f.log, err)
		return
	}
	if !valid {
		WriteError(w, http.StatusBadRequest, "That authenticator code is not valid.")
		return
	}
	_, _, _, err = f.loadCoinAndAmount(ctx, req.Coin, req.Amount)
	if err != nil {
		writePublicErr(w, f.log, err)
		return
	}
	wd, err := f.wallet.withdraw(ctx, s.MemberID, req.Coin, strings.TrimSpace(req.Address), req.Amount)
	if err != nil {
		writeWalletErr(w, f.log, err)
		return
	}
	writeJSON(w, http.StatusOK, wd)
}

// ---- the withdrawal address book (api-owned) ----

// withdrawAddress is one saved withdrawal address with its label.
type withdrawAddress struct {
	ID        string `json:"id"`
	Coin      string `json:"coin"`
	Address   string `json:"address"`
	Label     string `json:"label"`
	CreatedAt string `json:"createdAt"`
}

func (f *fundsAPI) handleListWithdrawAddresses(w http.ResponseWriter, r *http.Request, s Session) {
	rows, err := f.pool.Query(r.Context(), `
		SELECT id, coin, address, label, created_at
		FROM withdraw_addresses WHERE member_id = $1
		ORDER BY created_at, id`, s.MemberID)
	if err != nil {
		writeErr(w, f.log, err)
		return
	}
	defer rows.Close()
	out := []withdrawAddress{}
	for rows.Next() {
		var a withdrawAddress
		var createdAt time.Time
		if err := rows.Scan(&a.ID, &a.Coin, &a.Address, &a.Label, &createdAt); err != nil {
			writeErr(w, f.log, err)
			return
		}
		a.CreatedAt = formatTime(createdAt)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, f.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (f *fundsAPI) handleAddWithdrawAddress(w http.ResponseWriter, r *http.Request, s Session) {
	var req struct {
		Coin    string `json:"coin"`
		Address string `json:"address"`
		Label   string `json:"label"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	req.Address = strings.TrimSpace(req.Address)
	req.Label = strings.TrimSpace(req.Label)
	if req.Coin == "" || req.Address == "" {
		WriteError(w, http.StatusBadRequest, "Enter the Coin and the withdrawal address.")
		return
	}
	if len(req.Label) > 100 {
		WriteError(w, http.StatusBadRequest, "The label is too long (at most 100 characters).")
		return
	}
	// The Coin must be real and withdrawable — the book only holds
	// addresses a withdrawal could target.
	coin, err := f.wallet.coin(r.Context(), req.Coin)
	if err != nil {
		writePublicErr(w, f.log, err)
		return
	}
	if !coin.Withdrawable {
		WriteError(w, http.StatusBadRequest, "This Coin cannot be withdrawn right now.")
		return
	}
	id := uuid.NewString()
	var createdAt time.Time
	err = f.pool.QueryRow(r.Context(), `
		INSERT INTO withdraw_addresses (id, member_id, coin, address, label)
		VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
		id, s.MemberID, req.Coin, req.Address, req.Label).Scan(&createdAt)
	if err != nil {
		if isUniqueViolation(err) {
			WriteError(w, http.StatusConflict, "This address is already in your withdrawal address book.")
			return
		}
		writeErr(w, f.log, err)
		return
	}
	writeJSON(w, http.StatusOK, withdrawAddress{
		ID: id, Coin: req.Coin, Address: req.Address, Label: req.Label,
		CreatedAt: formatTime(createdAt),
	})
}

func (f *fundsAPI) handleUpdateWithdrawAddress(w http.ResponseWriter, r *http.Request, s Session) {
	var req struct {
		Label string `json:"label"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if len(req.Label) > 100 {
		WriteError(w, http.StatusBadRequest, "The label is too long (at most 100 characters).")
		return
	}
	tag, err := f.pool.Exec(r.Context(), `
		UPDATE withdraw_addresses SET label = $3
		WHERE id = $1 AND member_id = $2`,
		r.PathValue("id"), s.MemberID, req.Label)
	if err != nil {
		writeErr(w, f.log, err)
		return
	}
	if tag.RowsAffected() == 0 {
		WriteError(w, http.StatusNotFound, "That withdrawal address is not in your book.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (f *fundsAPI) handleDeleteWithdrawAddress(w http.ResponseWriter, r *http.Request, s Session) {
	tag, err := f.pool.Exec(r.Context(), `
		DELETE FROM withdraw_addresses WHERE id = $1 AND member_id = $2`,
		r.PathValue("id"), s.MemberID)
	if err != nil {
		writeErr(w, f.log, err)
		return
	}
	if tag.RowsAffected() == 0 {
		WriteError(w, http.StatusNotFound, "That withdrawal address is not in your book.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
