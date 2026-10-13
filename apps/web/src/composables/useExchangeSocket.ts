// The member WebSocket client (ADR-0011): one connection carrying the
// public channels and the personal orders channel. REST-first bootstrap
// lives with the caller — this socket only moves deltas, and after any
// reconnect it resubscribes everything and asks the caller to resync
// from REST, so a dropped socket never leaves stale prices on screen.
//
// The socket factory and scheduler are injected seams so the state
// machine (backoff, resubscribe, dispose) is unit-testable without a
// network or timers.
import { ref, type Ref } from 'vue'

export interface SocketEnvelope {
  channel: string
  data?: unknown
  error?: string
}

export interface SocketLike {
  send(data: string): void
  close(): void
  onopen: (() => void) | null
  onmessage: ((event: { data: unknown }) => void) | null
  onclose: (() => void) | null
  onerror: (() => void) | null
}

export interface ExchangeSocketOptions {
  url: string
  token?: string
  onEnvelope: (envelope: SocketEnvelope) => void
  onResync: () => void
  createSocket?: (url: string, protocols: string[]) => SocketLike
  schedule?: (fn: () => void, ms: number) => unknown
  clearSchedule?: (handle: unknown) => void
}

export interface ExchangeSocket {
  connect: () => void
  disconnect: () => void
  subscribe: (channel: string) => void
  unsubscribe: (channel: string) => void
  connected: Ref<boolean>
}

const BACKOFF_BASE_MS = 1000
const BACKOFF_MAX_MS = 30000

export function createExchangeSocket(options: ExchangeSocketOptions): ExchangeSocket {
  // A signed-in socket carries the session token as a two-element
  // Sec-WebSocket-Protocol list: browsers cannot set handshake
  // headers, and the subprotocol keeps the token out of URLs, where
  // it would ride logs and proxies. The factory seam receives the
  // protocols so tests can assert them.
  const protocols = options.token ? ['access-auth-token', options.token] : []
  const createSocket =
    options.createSocket ?? ((url: string, protocols: string[]) => new WebSocket(url, protocols) as unknown as SocketLike)
  const schedule = options.schedule ?? ((fn: () => void, ms: number) => setTimeout(fn, ms))
  const clearSchedule =
    options.clearSchedule ?? ((handle: unknown) => clearTimeout(handle as ReturnType<typeof setTimeout>))

  const connected = ref(false)
  const subscriptions = new Set<string>()
  let socket: SocketLike | null = null
  let disposed = false
  let attempt = 0
  let pendingTimer: unknown = null


  function send(op: 'subscribe' | 'unsubscribe', channel: string): void {
    socket?.send(JSON.stringify({ op, channel }))
  }

  function connect(): void {
    if (disposed || socket !== null) {
      return
    }
    const current = createSocket(options.url, protocols)
    socket = current
    current.onopen = () => {
      if (socket !== current) {
        return
      }
      attempt = 0
      connected.value = true
      for (const channel of subscriptions) {
        send('subscribe', channel)
      }
      // Fresh connection: deltas from here, history from REST.
      options.onResync()
    }
    current.onmessage = (event) => {
      if (socket !== current) {
        return
      }
      try {
        const envelope = JSON.parse(String(event.data)) as SocketEnvelope
        if (typeof envelope.channel === 'string') {
          options.onEnvelope(envelope)
        }
      } catch {
        // A malformed frame is dropped, never fatal (ADR-0011 has no
        // ack protocol to answer with).
      }
    }
    current.onclose = () => {
      if (socket !== current) {
        return
      }
      socket = null
      connected.value = false
      if (disposed) {
        return
      }
      const delay = Math.min(BACKOFF_BASE_MS * 2 ** attempt, BACKOFF_MAX_MS)
      attempt += 1
      pendingTimer = schedule(() => {
        pendingTimer = null
        connect()
      }, delay)
    }
    current.onerror = () => {
      // The close event follows; reconnect is driven from there.
    }
  }

  return {
    connect,
    disconnect() {
      disposed = true
      if (pendingTimer !== null) {
        clearSchedule(pendingTimer)
        pendingTimer = null
      }
      socket?.close()
      socket = null
      connected.value = false
    },
    subscribe(channel: string) {
      const fresh = !subscriptions.has(channel)
      subscriptions.add(channel)
      if (fresh && connected.value) {
        send('subscribe', channel)
      }
    },
    unsubscribe(channel: string) {
      if (!subscriptions.delete(channel)) {
        return
      }
      if (connected.value) {
        send('unsubscribe', channel)
      }
    },
    connected,
  }
}

/** The composable the trading page uses, wired to runtime config. */
export function useExchangeSocket(
  onEnvelope: (envelope: SocketEnvelope) => void,
  onResync: () => void,
): ExchangeSocket {
  const config = useRuntimeConfig()
  const { token } = useSession()
  return createExchangeSocket({
    url: typeof config.public.wsUrl === 'string' ? config.public.wsUrl : '',
    token: token.value ?? undefined,
    onEnvelope,
    onResync,
  })
}
