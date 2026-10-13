// Admin-activated Code channels (ticket #38, ADR-0006): the registry in
// the api schema, the internal admin RPC that flips channels on and off
// with credentials, the Telegram start-the-bot binding flow, and the
// phone-bind support hook the sensitive-flow matrix (ticket #39)
// completes. Email stays always on (mailer.go); channel codes never
// gate funds — nothing in the funds surface consumes them.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Code purposes this ticket adds to the purpose-scoped machinery
// (codes.go): the phone-bind hook and the Telegram possession check.
// A code only ever validates against the purpose it was issued for.
const (
	purposePhoneBind      = "phone-bind"      // target: the member's phone (E.164)
	purposeTelegramVerify = "telegram-verify" // target: the member id
)

// The webhook update header Telegram's servers echo back on every call
// (the setWebhook secret token).
const telegramWebhookHeader = "X-Telegram-Bot-Api-Secret-Token"

var errChatBoundElsewhere = errors.New("telegram chat is already bound to another member")

// channelAPI carries the collaborators of the Code-channel routes.
type channelAPI struct {
	pool *pgxpool.Pool
	env  *APIEnv
	log  *slog.Logger
}

// rowQuerier is the one-query surface both the pool and a transaction
// give the registry.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ---- the registry ----

// channelState is the registry row as any list serves it: status only.
// Channel credentials are server-side secrets — they never travel on a
// response, member-facing or internal (ticket #38 acceptance).
type channelState struct {
	Channel     string  `json:"channel"`
	Active      bool    `json:"active"`
	ActivatedAt *string `json:"activatedAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

func listChannelStates(ctx context.Context, db *pgxpool.Pool) ([]channelState, error) {
	rows, err := db.Query(ctx,
		`SELECT name, active, to_char(activated_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		        to_char(updated_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		   FROM code_channels ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("api: list channels: %w", err)
	}
	defer rows.Close()
	states := make([]channelState, 0, 3)
	for rows.Next() {
		var st channelState
		if err := rows.Scan(&st.Channel, &st.Active, &st.ActivatedAt, &st.UpdatedAt); err != nil {
			return nil, fmt.Errorf("api: list channels: %w", err)
		}
		states = append(states, st)
	}
	return states, rows.Err()
}

// setChannel flips one channel's activation state. Activating stores
// the credentials; deactivating clears them — an off channel keeps no
// secrets it does not need.
func setChannel(ctx context.Context, db *pgxpool.Pool, name string, active bool, creds json.RawMessage) error {
	tag, err := db.Exec(ctx,
		`UPDATE code_channels
		  SET active = $2,
		      credentials = $3,
		      activated_at = CASE WHEN $2 THEN now() ELSE NULL END,
		      updated_at = now()
		 WHERE name = $1`, name, active, creds)
	if err != nil {
		return fmt.Errorf("api: update channel %s: %w", name, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %q", errUnknownChannel, name)
	}
	return nil
}

// loadChannel reads one registry row and parses its credentials. The
// second return is the activation state: false means the driver stays a
// dead path (ADR-0006 dormant) and no delivery may run.
func loadChannel(ctx context.Context, q rowQuerier, name string) (ChannelCredentials, bool, error) {
	var raw []byte
	var active bool
	err := q.QueryRow(ctx,
		`SELECT credentials, active FROM code_channels WHERE name = $1`, name).
		Scan(&raw, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChannelCredentials{}, false, fmt.Errorf("%w: %q", errUnknownChannel, name)
	}
	if err != nil {
		return ChannelCredentials{}, false, fmt.Errorf("api: load channel %s: %w", name, err)
	}
	if !active {
		return ChannelCredentials{}, false, nil
	}
	creds, err := parseChannelCredentials(name, raw)
	if err != nil {
		return ChannelCredentials{}, false, err
	}
	return creds, true, nil
}

// ---- admin internal RPC (admin-api fronts it later, spec #25) ----

// RegisterChannelAdmin mounts the admin channel RPC on the INTERNAL mux
// (patterns land under /internal/… after the boot harness strips the
// prefix). The compose network is the auth boundary, like every
// internal RPC.
func (a *channelAPI) RegisterChannelAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /channels", a.handleListChannels)
	mux.HandleFunc("POST /channels/{name}/activate", a.handleActivateChannel)
	mux.HandleFunc("POST /channels/{name}/deactivate", a.handleDeactivateChannel)
}

func (a *channelAPI) handleListChannels(w http.ResponseWriter, r *http.Request) {
	states, err := listChannelStates(r.Context(), a.pool)
	if err != nil {
		writeInternalErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, states)
}

func (a *channelAPI) handleActivateChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Credentials json.RawMessage `json:"credentials"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeInternalErr(w, http.StatusBadRequest, "activate needs a JSON body with credentials")
		return
	}
	// Refuse activation unless the credentials parse for this driver.
	if _, err := parseChannelCredentials(name, req.Credentials); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errUnknownChannel) {
			status = http.StatusNotFound
		}
		writeInternalErr(w, status, err.Error())
		return
	}
	if err := setChannel(r.Context(), a.pool, name, true, req.Credentials); err != nil {
		writeInternalErr(w, channelErrStatus(err), err.Error())
		return
	}
	a.log.Info("code channel activated", "channel", name, "actor", r.Header.Get("X-Mal-Actor"))
	w.WriteHeader(http.StatusNoContent)
}

func (a *channelAPI) handleDeactivateChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := setChannel(r.Context(), a.pool, name, false, json.RawMessage(`{}`)); err != nil {
		writeInternalErr(w, channelErrStatus(err), err.Error())
		return
	}
	a.log.Info("code channel deactivated", "channel", name, "actor", r.Header.Get("X-Mal-Actor"))
	w.WriteHeader(http.StatusNoContent)
}

// ---- member routes ----

// RegisterChannels mounts the member-facing channel routes on the
// PUBLIC mux (Caddy proxies /api/* without stripping).
func (a *channelAPI) RegisterChannels(mux *http.ServeMux) {
	// Telegram's servers call the webhook (set up out-of-band); the
	// webhook secret is its auth.
	mux.HandleFunc("POST /api/uc/telegram/webhook", a.handleTelegramWebhook)
	mux.HandleFunc("POST /api/uc/channels/telegram/link", withMemberSession(a.pool, a.env, a.handleTelegramLink))
	mux.HandleFunc("POST /api/uc/channels/telegram/code", withMemberSession(a.pool, a.env, a.handleTelegramCode))
	mux.HandleFunc("POST /api/uc/channels/telegram/verify", withMemberSession(a.pool, a.env, a.handleTelegramVerify))
	mux.HandleFunc("POST /api/uc/phone/code", withMemberSession(a.pool, a.env, a.handlePhoneCode))
}

// channelErrStatus maps the channel registry's errors onto admin-RPC
// statuses: an unknown channel is a 404, everything else is our fault.
func channelErrStatus(err error) int {
	if errors.Is(err, errUnknownChannel) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// telegramInactive refuses a member request while the Telegram channel
// is dormant.
func telegramInactive(w http.ResponseWriter) {
	WriteError(w, http.StatusConflict, "Telegram codes are unavailable right now. Try again later.")
}

// handleTelegramLink hands the member their start-the-bot deep link:
// the bot username resolved live from Telegram plus a fresh per-member
// binding token (single-use, TTL'd like a code).
func (a *channelAPI) handleTelegramLink(w http.ResponseWriter, r *http.Request, s Session) {
	creds, active, err := loadChannel(r.Context(), a.pool, ChannelTelegram)
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	if !active {
		telegramInactive(w)
		return
	}
	bot, err := (telegramSender{}).BotUsername(r.Context(), creds)
	if err != nil {
		a.log.Error("telegram getMe failed", "err", err)
		WriteError(w, http.StatusServiceUnavailable, "We could not reach Telegram right now. Try again shortly.")
		return
	}
	token, err := newSecret()
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	_, err = a.pool.Exec(r.Context(),
		`INSERT INTO channel_binding_tokens (token_hash, member_id, channel, expires_at)
		 VALUES ($1, $2, $3, now() + make_interval(secs => $4::double precision))`,
		tokenDigest(token), s.MemberID, ChannelTelegram, a.env.CodeTTL.Seconds())
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"link":      "https://t.me/" + bot + "?start=" + token,
		"expiresIn": ttlWords(a.env.CodeTTL),
	})
}

// telegramUpdate is the slice of the Bot API update object the binding
// flow reads: a message's chat id and text. Every other update shape is
// acknowledged and ignored.
type telegramUpdate struct {
	Message *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

// handleTelegramWebhook is the webhook-style binding endpoint: a
// /start <binding token> message consumes the token and records the
// chat↔member binding. Telegram only reads the status — every handled,
// ignored, or refused-but-expected update answers 200 so Telegram does
// not retry; only a wrong secret or dormant channel answers otherwise.
func (a *channelAPI) handleTelegramWebhook(w http.ResponseWriter, r *http.Request) {
	creds, active, err := loadChannel(r.Context(), a.pool, ChannelTelegram)
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	if !active {
		// Dormant channel: a dead path answers not found.
		WriteError(w, http.StatusNotFound, "Not found.")
		return
	}
	if subtle.ConstantTimeCompare(
		[]byte(r.Header.Get(telegramWebhookHeader)), []byte(creds.WebhookSecret)) != 1 {
		WriteError(w, http.StatusForbidden, "Webhook secret rejected.")
		return
	}
	var upd telegramUpdate
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&upd); err != nil {
		WriteError(w, http.StatusBadRequest, "Malformed update.")
		return
	}
	ack := func() { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) }
	if upd.Message == nil {
		ack() // channel posts, edits, …: nothing to bind
		return
	}
	fields := strings.Fields(upd.Message.Text)
	if len(fields) != 2 || fields[0] != "/start" {
		ack() // ordinary bot chat: acknowledge, never bind
		return
	}
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		var memberID string
		err := tx.QueryRow(r.Context(),
			`UPDATE channel_binding_tokens SET used_at = now()
			  WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
			  RETURNING member_id`, tokenDigest(fields[1])).Scan(&memberID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // stale, expired, or forged token: acknowledge so Telegram stops retrying
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO member_telegram_chats (member_id, chat_id) VALUES ($1, $2)
			 ON CONFLICT (member_id) DO UPDATE SET chat_id = EXCLUDED.chat_id, bound_at = now()`,
			memberID, upd.Message.Chat.ID); err != nil {
			if isUniqueViolation(err) {
				// The chat belongs to another member; roll back so this
				// member's token stays usable from their own chat.
				return errChatBoundElsewhere
			}
			return err
		}
		return nil
	})
	switch {
	case errors.Is(err, errChatBoundElsewhere):
		a.log.Warn("telegram chat already bound to another member", "chat_id", upd.Message.Chat.ID)
		ack()
	case err != nil:
		writeErr(w, a.log, err)
	default:
		ack()
	}
}

// handleTelegramCode issues a Telegram Verification code — but only for
// a member whose chat the webhook bound; codes to unbound members are
// rejected before anything is sent.
func (a *channelAPI) handleTelegramCode(w http.ResponseWriter, r *http.Request, s Session) {
	creds, active, err := loadChannel(r.Context(), a.pool, ChannelTelegram)
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	if !active {
		telegramInactive(w)
		return
	}
	var chatID int64
	err = a.pool.QueryRow(r.Context(),
		`SELECT chat_id FROM member_telegram_chats WHERE member_id = $1`, s.MemberID).Scan(&chatID)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteError(w, http.StatusBadRequest,
			"Start the Telegram bot from your binding link before requesting Telegram codes.")
		return
	}
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	var code string
	var tooSoon bool
	err = pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		recent, err := recentCode(r.Context(), tx, purposeTelegramVerify, s.MemberID, a.env.CodeCooldown)
		if err != nil {
			return err
		}
		tooSoon = recent
		if recent {
			return nil
		}
		code, err = issueCode(r.Context(), tx, purposeTelegramVerify, s.MemberID, a.env.CodeTTL)
		return err
	})
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	if tooSoon {
		WriteError(w, http.StatusTooManyRequests,
			"A verification code was just sent. Check your Telegram before requesting another.")
		return
	}
	message, err := TelegramCodeMessage(code, a.env.CodeTTL)
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	if err := (telegramSender{}).Send(r.Context(), creds, fmt.Sprintf("%d", chatID), message); err != nil {
		a.log.Error("telegram code delivery failed", "err", err)
		WriteError(w, http.StatusServiceUnavailable,
			"We could not send the Telegram code right now. Try again shortly.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "code sent"})
}

// handleTelegramVerify consumes a delivered Telegram code, proving the
// member reads the bound chat. It gates nothing (ADR-0006: channel
// codes never gate funds) — later sensitive flows build on the check.
func (a *channelAPI) handleTelegramVerify(w http.ResponseWriter, r *http.Request, s Session) {
	var req struct {
		Code string `json:"code"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.Code) != 6 {
		WriteError(w, http.StatusBadRequest, "Enter the 6-digit code from your Telegram message.")
		return
	}
	if err := verifyCode(r.Context(), a.pool, purposeTelegramVerify, s.MemberID, req.Code, a.env.MaxCodeAttempts); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "telegram verified"})
}

// handlePhoneCode is the phone-bind support hook: the member supplies a
// phone, and the code goes to the activated phone channel (sms
// preferred over whatsapp). The sensitive-flow matrix (ticket #39)
// consumes these purpose-scoped codes to complete the binding.
func (a *channelAPI) handlePhoneCode(w http.ResponseWriter, r *http.Request, s Session) {
	var req struct {
		Phone string `json:"phone"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !validPhone(req.Phone) {
		WriteError(w, http.StatusBadRequest,
			"Enter the phone number in international format, for example +15551234567.")
		return
	}
	var channel string
	var creds ChannelCredentials
	var code string
	var tooSoon bool
	err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		for _, name := range []string{ChannelSMS, ChannelWhatsApp} {
			c, active, err := loadChannel(r.Context(), tx, name)
			if err != nil {
				return err
			}
			if !active {
				continue
			}
			channel, creds = name, c
			break
		}
		if channel == "" {
			return nil // no phone channel active: nothing to deliver on
		}
		recent, err := recentCode(r.Context(), tx, purposePhoneBind, req.Phone, a.env.CodeCooldown)
		if err != nil {
			return err
		}
		tooSoon = recent
		if recent {
			return nil
		}
		code, err = issueCode(r.Context(), tx, purposePhoneBind, req.Phone, a.env.CodeTTL)
		return err
	})
	if err != nil {
		writeErr(w, a.log, err)
		return
	}
	switch {
	case channel == "":
		WriteError(w, http.StatusConflict, "Phone codes are unavailable right now. Try again later.")
	case tooSoon:
		WriteError(w, http.StatusTooManyRequests,
			"A verification code was just sent. Check your phone before requesting another.")
	default:
		message, err := PhoneCodeMessage(code, a.env.CodeTTL)
		if err != nil {
			writeErr(w, a.log, err)
			return
		}
		if err := channelSenders[channel].Send(r.Context(), creds, req.Phone, message); err != nil {
			a.log.Error("phone code delivery failed", "channel", channel, "err", err)
			WriteError(w, http.StatusServiceUnavailable,
				"We could not send the code right now. Try again shortly.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "code sent"})
	}
}

// validPhone accepts E.164: a plus followed by 7 to 15 digits.
func validPhone(p string) bool {
	if len(p) < 8 || len(p) > 16 || p[0] != '+' {
		return false
	}
	for _, c := range p[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
