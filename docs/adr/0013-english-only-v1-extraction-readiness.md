# ADR-0013: English-only v1 — no i18n infrastructure, extraction-ready strings

The legacy web front shipped vue-i18n with `zh`/`en` dictionaries (~1,360 keys per locale, default `zh`); the legacy admin had no app-level i18n at all (hardcoded Chinese chrome, only iView UI-kit locales auto-picked from `navigator.language`). ADR-0003 already translated both Nuxt 3 ports to English. The open question was whether v1 should carry i18n infrastructure anyway (locale files, switcher, `@nuxtjs/i18n` wired with a single `en` locale). It should not: the expensive part of i18n is translation and its maintenance, which infrastructure does not buy; a second locale is only commissioned when real users need it, and adding the module then is routine. What v1 owes instead is an **extraction-readiness contract** so that retrofit stays cheap.

## Decision

- **English-only v1, both apps.** No i18n module, no locale files, no language switcher. Plain English strings in code.
- **Extraction-readiness contract** (binding on the port, to be encoded by `/to-spec`):
  1. User-visible copy is written as complete sentences at single interpolation points, with named parameters — never concatenated fragments or word-order assumptions between separate strings.
  2. No user-visible copy baked into images, charts, or config constants.
  3. Dates, numbers, and money render through small formatting helpers that pin `en-US` / market conventions explicitly, so a future locale switch is a helper change, not a template sweep.
  4. Backend-generated member-facing text (verification emails, notifications) lives as one centralized English template set per message.
  5. The admin app is under the same rules — it loses nothing, as legacy admin never had a working switcher.
- **Backend carries zero locale state**: no `Member.locale` column, no `Accept-Language` handling anywhere, English error messages (per the map's standing preference).

## Considered options (rejected)

- **`@nuxtjs/i18n` wired now with a single `en` locale**: rejected — locale-prefixed routing/SEO/detection config surface for zero served locales, plus ~1,400 keys of English-code-string → English-locale-file busywork during the port itself.
- **Full `zh`/`en` parity port** (legacy front's shape): rejected — no v1 user base needs Chinese; the legacy default does not bind the rewrite.
- **`Member.locale` column now**: rejected — speculative schema for a decision not yet made; a nullable column added later is a trivial migration.

## Consequences

- Commissioning a second locale later means wiring the module plus a string-extraction pass; the contract keeps that pass bounded (no concatenated-sentence archaeology). The fallback chain at that point is trivially `en`.
- The contract is a port-time discipline, not runtime machinery — nothing to install, configure, or carry in the compose stack.
- Second-locale i18n itself (files, switcher, translation) is out of scope for this map's destination.
