# Nuxt 3 for both frontends

The two Vue 2 frontends (`04_Web_Admin`, `05_Web_Front`) are parity-ported to **Nuxt 3**, not Next.js/React. The source is Vue, so component structure, state patterns, and template logic carry over near-mechanically, and `element-plus` admin templates closely match the original `element-ui` look. Both apps are mostly authenticated screens where SSR matters little, so the framework choice was dominated by porting cost, not web-platform capability.

## Considered options

- **Next.js (React)**: larger ecosystem and hiring pool, but every screen would be re-expressed in React idioms — a redesign, not a port. Rejected absent a team preference for React.

## Consequences

- All new frontend code is Vue 3 / Nuxt 3; UI library is `element-plus` (admin) with TradingView lightweight-charts for the trading front.
- The `09_DOC` screenshots are the visual reference for the port; all strings become English.
- A future visual redesign is a separate effort and does not reopen this choice.
