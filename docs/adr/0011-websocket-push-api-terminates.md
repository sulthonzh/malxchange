# ADR-0011: WebSocket push — the api service terminates the member socket

Legacy hosted the browser socket on the market service with **zero WS auth**: personal order topics carried the memberId in the topic string, unguarded, so any client could subscribe any Member's feed. We decided the **api service hosts the single member WebSocket endpoint** (through Caddy): public channels (`ticker`, `depth.{market}`, `trades.{market}`, `kline.{market}.{interval}`) and the personal `orders` channel on one connection, authenticated at connect with the Member session (ADR-0006); the socket is bound to a Member server-side and personal channels are filtered by the member IDs the events already carry — a memberId never appears in a channel name. Anonymous connections may subscribe public channels only. The market service stays headless (derived data over REST + Redpanda); no gateway container joins ADR-0007's topology.

Mechanics: plain WebSocket + JSON envelopes — no SockJS, no STOMP. api runs **one shared franz-go consumer per topic** (never per-connection) feeding an in-process channel→subscribers hub. `trades` is a projection of `trade.fill` with Member/Order IDs stripped; personal events filter `trade.fill` / `order.completed` / `cancel.rejected` by their carried member IDs (self-contained per ADR-0009/0010 — no cross-schema lookups). Depth relays `market.plate` as-is: the engine's once-per-batch publish already paces it, so legacy's extra 2-second plate throttle, its HashMap queues, and the scheduled flush all die; the ticker is paced by the market service's publish cadence (≤1s).

Protocol: **REST-first bootstrap** (depth, latest trades, ticker, kline history, open orders) then delta pushes — no snapshot-on-subscribe. During Warmup the REST depth answer comes from market's persisted last-known plate (derived, rebuildable; empty on a true cold start; refreshed by the engine's initial publish per ADR-0010). Each socket gets a bounded outbound buffer; overflow closes the socket and the client reconnects with backoff and re-fetches — full-snapshot Depth makes resync trivially consistent, and `trades` events carry the fill ID for client-side dedupe. No ack protocol.

## Considered options

- **market terminates public channels, api the personal ones**: rejected — two sockets per browser, market needs session checks via RPC, the chart datafeed splits across connections, and legacy's hole came from exactly this separation of auth from push.
- **Dedicated gateway container**: rejected — a new deploy unit and hop on a single-host install, for none of the scale it exists to solve; api already owns the auth a gateway would need.

## Consequences

- api deploys drop member sockets; clients reconnect and Redpanda buffered everything meanwhile.
- Legacy cut here: SockJS + STOMP; `/topic/market/trade-depth` (no web subscriber); the Netty TCP/protobuf path (mobile is out of scope); kline push restricted to 1-minute resolution (kline channels exist for every stored interval); the unguarded memberId topics.
- Market-data REST is served via api proxying market's internal endpoints — the context map keeps api the only public member surface.
- The admin app remains REST-only, as in legacy.
