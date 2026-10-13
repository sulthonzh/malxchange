// The exchange contract: every api shape the trading page consumes
// (spec #24 story 16–25), named once so the chart, book, form, and order
// panels cannot drift from each other or from the api wire. Money is
// decimal strings throughout, verbatim from the server — only the
// decimal helpers in ~/utils/money.ts interpret them, never parseFloat.
export interface MarketInfo {
  market: string
  baseCoin: string
  quoteCoin: string
  zone: string
  sort: number
  featured: boolean
  baseScale: number
  quoteScale: number
  tickSize: string
  lotSize: string
}

export interface Ticker {
  market: string
  last: string
  high: string
  low: string
  turnover: string
}

export interface BookLevel {
  price: string
  amount: string
}

export interface Depth {
  market: string
  bids: BookLevel[]
  asks: BookLevel[]
}

export interface PublicTrade {
  id: string
  price: string
  amount: string
  turnover: string
  takerSide: string
  time: string
}

export interface KlineBar {
  interval: string
  openTime: string
  open: string
  high: string
  low: string
  close: string
  amount: string
  turnover: string
  fillCount: number
}

/** Every interval the api folds Klines at (stored, so channels exist). */
export const KLINE_INTERVALS = ['1m', '5m', '15m', '30m', '1h', '4h', '1d', '1w'] as const

export type KlineInterval = (typeof KLINE_INTERVALS)[number]

export interface MemberOrder {
  orderId: string
  market: string
  side: 'buy' | 'sell'
  kind: 'limit' | 'market'
  price?: string
  amount: string
  filledAmount: string
  turnover: string
  status: string
  createdAt: string
  canceledAt?: string
  fills?: OrderFill[]
}

export interface OrderFill {
  seq: number
  price: string
  amount: string
  turnover: string
  fee: string
  time: string
}

export interface PlaceOrderResponse {
  orderId: string
}

/** Pair from the route (btcusdt) to the api Market symbol (BTC/USDT). */
export function marketSymbol(base: string, quote: string): string {
  return `${base.toUpperCase()}/${quote.toUpperCase()}`
}

/**
 * The legacy pair vocabulary was `BTCUSDT` in the route; the port takes
 * `base_quote` (see guide) and resolves the symbol against the market
 * list, so a renamed or delisted pair degrades to the first listed
 * Market instead of a dead page.
 */
export function symbolFromRoutePair(pair: string, markets: MarketInfo[]): MarketInfo | undefined {
  const normalized = pair.trim().toLowerCase()
  const bySlug = markets.find(
    (m) => `${m.baseCoin}_${m.quoteCoin}`.toLowerCase() === normalized,
  )
  if (bySlug !== undefined) {
    return bySlug
  }
  const squashed = markets.find((m) => `${m.baseCoin}${m.quoteCoin}`.toLowerCase() === normalized)
  return squashed
}

/** The pair slug a Market renders as in /exchange/:pair routes. */
export function routePairSlug(market: MarketInfo): string {
  return `${market.baseCoin}_${market.quoteCoin}`.toLowerCase()
}

/** The member-visible sentence for a named server order error. */
export function orderErrorMessage(message: string): string {
  // The api already answers complete sentences (ADR-0013); the page
  // passes them through, with a fallback for anything unexpected.
  if (message !== '') {
    return message
  }
  return 'The order could not be placed. Try again.'
}
