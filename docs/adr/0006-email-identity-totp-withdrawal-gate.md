# ADR-0006: v1 auth scope — email-only identity, admin-activated code channels, TOTP withdrawal gate

The legacy ucenter is phone-first (SMS-code registration, SMS providers as a hard dependency), carries two parallel member secrets (login password + withdraw password), makes TOTP optional, and gates abuse with third-party Chinese captcha services. The rewrite starts fresh on a single-host install, so identity can be chosen for reachability and operational simplicity rather than ported. We decided on **email as the sole registration and login identifier**, **verification codes delivered over admin-activated channels**, and **a mandatory TOTP authenticator as the only withdrawal second factor**.

## Considered options

- **Port phone-first registration**: rejected — drags an SMS-provider dependency (account, cost, regional reachability) through the front door of a fresh single-host install before the first member exists.
- **Phone as a second identifier** (login by email *or* phone): rejected — a dual-namespace identity model doubles uniqueness/recovery surface for no v1 benefit; phone stays a secondary contact, usable only once an admin activates a messenger/SMS channel.
- **Messenger codes as the withdrawal second factor** (admin picks TOTP *or* channel code): rejected — a fund-moving gate that depends on WhatsApp/Meta/gateway uptime freezes withdrawals during a provider outage, and Telegram/SIM hijack becomes a funds-loss vector.
- **Keep the withdraw password** (`jyPassword`): rejected — one strong second factor replaces two weak ones and deletes an entire password lifecycle (set/change/reset flows, admin support load).
- **Legacy salted-MD5 + no failed-login lockout + Geetest/防水墙 captcha**: rejected — fresh start makes hashing a free choice (argon2id); lockout + rate limits replace third-party captcha in v1.

## Consequences

- Registration = email + 6-digit **verification code** (code verified at signup, so every identifier is verified by construction); no username namespace. Login = email + password, opaque per-device **session** rows (sliding 7-day expiry, list + revoke-all; `access-auth-token` header mechanic for the Nuxt app).
- **Code channels**: email always on; SMS-gateway, WhatsApp Cloud API, and Telegram drivers ship dormant and are activated at runtime by an admin with provider credentials — no code change to flip a channel on. Telegram requires a start-the-bot deep link binding chat ↔ member before codes flow.
- **Withdrawals** require KYC *and* a bound TOTP authenticator; channel codes never gate funds. Sensitive flows: TOTP bind = session + email code; TOTP unbind = login password + TOTP; email change = login password + codes to both old and new email; phone bind = login password + code to the new phone; password change = old password + email code + revoke sessions; forgot password = email code + revoke sessions.
- **TOTP lost-device recovery is admin-mediated** (KYC-verified members only, audit-logged, notify all active channels) — the gate stays strong because breaking it needs a human comparing a face against KYC documents.
- **KYC** = full name + document number + front/back images, one document per platform, admin-approved; no local document-format validation and no handheld photo. Cuts from legacy: promotion/invite/commission system (the `member-register` event is still emitted so wallet opens Ledger accounts), merchant applications, bank/Alipay/WeChat bindings, member levels, super-partner review, supported-country endpoint, Geetest/Tencent/NetEase captcha, API keys (absent in legacy too).
