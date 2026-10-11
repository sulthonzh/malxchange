# Auth & Identity Inventory — ucenter-api (Issue #8)

Inventory of every auth/identity feature implemented in `00_framework/ucenter-api/` (with `00_framework/core` shared code and `00_framework/admin` review endpoints). No secret values included.

## Registration
- Phone registration (PC): validates SMS code from Redis (`PHONE_REG_CODE_PREFIX`), Geetest防水 wall, uniqueness of phone/username, optional promotion code, salted-MD5 password, default `MemberLevelEnum.GENERAL`, "super partner" flag puts account in ILLEGAL status pending admin review → `RegisterController.loginByPhone` (`controller/RegisterController.java:280`).
- Phone registration (mobile variant, no Geetest): `/register/for_phone` → `RegisterController.loginByPhone4Mobile` (`RegisterController.java:379`).
- Username uniqueness pre-check: `/register/check/username` → `RegisterController.java:128`.
- Email registration: **commented out / disabled** (`// @RequestMapping("/register/email")`, `RegisterController.java:220`); email only bindable post-registration (`/bind/email/code` :456, ApproveController `/bind/email` :275).
- Supported-country list for registration: `/support/country` (`RegisterController.java:113`).
- On successful registration `MemberEvent.onRegisterSuccess` publishes Kafka topic **`member-register`** and creates promotion/reward records (`core/.../event/MemberEvent.java:62`).

## Login / Session
- Username+password login, optional Geetest captcha secondary validation; sets session attribute `SESSION_MEMBER`, issues token stored on `Member.token` with 7-day expiry, increments login count, returns `LoginInfo` with promotion prefix → `LoginController.login` / `getLoginInfo` (`controller/LoginController.java:57,102`).
- Logout clears session + nulls token: `/loginout` (`LoginController.java:145`).
- Login state check: `/check/login` (`LoginController.java:169`).
- Token-based re-auth: `MemberInterceptor.preHandle` falls back to `access-auth-token` header via `MemberService.loginWithToken` (finds member by token, extends expiry 7 days, re-fires `onLoginSuccess`) (`core/.../interceptor/MemberInterceptor.java:32-52`, `core/.../service/MemberService.java:76`).
- Session strategy (header/cookie hybrid): `core/.../ext/SmartHttpSessionStrategy.java`, `ucenter-api/.../config/HttpSessionConfig.java`. No JWT — plain opaque tokens in DB + servlet session.

## Passwords
- Two distinct passwords: login password (`Member.password`, salted MD5) and withdraw/"safe" transaction password (`Member.jyPassword`).
  - Set transaction password: `/approve/transaction/password` (`ApproveController.java:128`).
  - Change: `/approve/update/transaction/password` (:149); reset via SMS/email code: `/approve/reset/transaction/password` (:170).
- Change login password with old-password + code: `/approve/update/password` (`ApproveController.java:243`).
- Forgot-password: send email reset code `/reset/email/code` (`RegisterController.java:549`, 10-min Redis TTL, FreeMarker template); reset `/reset/login/password` mode 0=phone / 1=email (`RegisterController.java:598`).

## Google Authenticator (TOTP)
- Generate secret + QR barcode URL: `/google/sendgoogle` (`GoogleAuthenticationController.java:70`).
- Verify code: `/google/yzgoogle` (:46); bind/enable `/google/googleAuth` (:160); unbind requires login password + code, sets `googleState=0` `/google/jcgoogle` (:111).

## SMS codes (per-purpose, all Redis-backed, rate-limited)
`SmsController` (`/mobile/...`): transaction code :97, bind-phone code :127, update-password code :170, add-address code :202, activity code :225, withdraw code :248, ctc code :272, reset code :299, change-phone code :325.

## KYC / Real-name
- KYC application: `/approve/real/name` requires realName, idCard, idCardFront, idCardBack, handHeldIdCard image URLs; for China validates Chinese name + 18-digit ID checksum (`IdcardValidator`); one ID card per platform; creates `MemberApplication` with `AuditStatus.AUDIT_ING`, sets member `RealNameStatus.AUDITING` (`ApproveController.java:305`).
- Application status query: `/approve/real/detail` (`ApproveController.java:348`).
- Review states: `AuditStatus` = AUDIT_ING / AUDIT_DEFEATED / AUDIT_SUCCESS (`core/.../constant/AuditStatus.java`); member-level states `RealNameStatus` = NOT_CERTIFIED / AUDITING / VERIFIED (`core/.../constant/RealNameStatus.java`).
- Admin review: pass (`memberApplicationService.auditPass` + SMS notify) / not-pass (`auditNotPass` + SMS) in `admin/.../controller/member/MemberApplicationController.java:94,116`; KYC config `MemberApplicationConfigController.java`.
- Merchant ("certified business") application with JSON + deposit: `/approve/certified/business/apply` (`ApproveController.java:575`), status :532, cancel :693.

## Account binding
- Bind/change phone (login password + SMS code): `/approve/bind/phone` :198, `/approve/change/phone` :656.
- Bind email: `/approve/bind/email` :275.
- Payment bindings: bank `/approve/bind/bank` :398 / update :430; Alipay :445/:475; WeChat :490/:522.
- Avatar, security-settings overview (`MemberSecurity` aggregates phone/email/Google/real-name states): `/approve/change/avatar` :77, `/approve/security/setting` :91, `/approve/account/setting` :369.

## Member levels / geo
- `MemberLevelEnum`: GENERAL / REALNAME / IDENTIFICATION (`core/.../constant/MemberLevelEnum.java`); set at registration to GENERAL.
- Geo: `Member.location.country` + `Country` stored at registration from client-submitted country (`RegisterController.java:338-345`).

## Invitation / promotion
- Promotion code generated per member (`GeneratorUtil.getPromotionCode(memberId)`, `RegisterController.java:354`); referral code validated at signup; `MemberEvent.onRegisterSuccess` records sponsor chain and first/second-level commission rewards (`core/.../event/MemberEvent.java:50,62`).
- Promotion stats/ranking/cards: `PromotionController.java` (`/promotion/mypagination` etc.), nightly aggregation job `job/MemberInviteStasticJob.java`.

## API keys
- **Not implemented** in ucenter-api (no apiKey/API-key classes; grep found none).

## Anti-brute-force / anti-abuse
- Rate limit 1 req/min per session on SMS/email-send endpoints via Redis `ANTI_ATTACK_` key: `aspect/AntiAttackAspect.java:44-66`.
- Geetest captcha on login and registration: `aspect/GeeTestAspect.java`, `system/GeetestLib.java`, `LoginController.java:60-100`; Tencent防水墙 on phone registration (`RegisterController.java:305`, `controller/GeetestController.java`).
- NetEase captcha (NECaptchaVerifier) wired but its verify call is commented out (`RegisterController.java:104-106`).
- No failed-login lockout counter exists (only login-count increment).

## Kafka
- Producer only (`config/KafkaProducerConfiguration.java`). Auth-related topic produced: **`member-register`** on registration (`core/.../event/MemberEvent.java:62`). Other topics in ucenter-api are withdrawal/asset-related (`WithdrawController.java`, `AssetController.java`), not auth.

## Feature table

| Feature | Implemented | Evidence |
|---|---|---|
| Phone registration (PC + mobile) | Yes | `RegisterController.java:280,379` |
| Email registration | No (disabled) | `RegisterController.java:220` (commented) |
| Username uniqueness check | Yes | `RegisterController.java:128` |
| Username+password login + Geetest | Yes | `LoginController.java:57` |
| Token re-auth (opaque, 7-day, DB) | Yes | `MemberInterceptor.java:32`, `MemberService.java:76` |
| JWT | No | session/token only |
| Logout / session check | Yes | `LoginController.java:145,169` |
| Password reset (phone/email code) | Yes | `RegisterController.java:549,598` |
| Change login password | Yes | `ApproveController.java:243` |
| Withdraw (safe) password set/change/reset | Yes | `ApproveController.java:128,149,170` |
| Google Auth generate/verify/bind/unbind | Yes | `GoogleAuthenticationController.java:46,70,111,160` |
| Per-purpose SMS codes | Yes | `SmsController.java:97-325` |
| KYC application (name, ID, 3 photos) | Yes | `ApproveController.java:305` |
| KYC review states + admin audit | Yes | `AuditStatus.java`, `admin MemberApplicationController.java:94,116` |
| Real-name validation (CN name + 18-ID checksum, one ID per user) | Yes | `ApproveController.java:323-329` |
| Merchant (certified business) application | Yes | `ApproveController.java:532,575,693` |
| Bind/change phone & email | Yes | `ApproveController.java:198,656,275` |
| Bank/Alipay/WeChat binding | Yes | `ApproveController.java:398-522` |
| Member levels (GENERAL/REALNAME/IDENTIFICATION) | Yes | `MemberLevelEnum.java` |
| Geo (country/location) capture | Yes | `RegisterController.java:338` |
| Promotion/invite codes + multi-level rewards | Yes | `RegisterController.java:354`, `MemberEvent.java:50,62` |
| API keys | No | — (no code found) |
| SMS/email rate limiting (1/min/session) | Yes | `AntiAttackAspect.java:44` |
| Geetest / 防水墙 captcha | Yes | `GeeTestAspect.java`, `RegisterController.java:305` |
| Failed-login lockout | No | only login-count increment (`LoginController.java:117`) |
| Kafka auth topic | `member-register` produced | `MemberEvent.java:62` |
