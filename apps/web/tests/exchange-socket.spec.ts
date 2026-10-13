import { describe, expect, it, vi } from 'vitest'

import {
  createExchangeSocket,
  type SocketEnvelope,
  type SocketLike,
} from '../src/composables/useExchangeSocket'

// The WS state machine at its injected seams: subscribe-before-connect
// replays on open, every (re)connect triggers a REST resync, drops back
// off exponentially to a cap, and dispose stops everything.

function fakeSocket(): SocketLike & { sent: string[] } {
  const sent: string[] = []
  return {
    sent,
    send(data: string): void {
      sent.push(data)
    },
    close(): void {},
    onopen: null,
    onmessage: null,
    onclose: null,
    onerror: null,
  }
}

function setup() {
  const sockets: SocketLike[] = []
  const timers: Array<{ fn: () => void; ms: number }> = []
  const envelopes: SocketEnvelope[] = []
  let resyncs = 0
  const socket = createExchangeSocket({
    url: 'wss://example.test/ws',
    token: 'tok',
    onEnvelope: (envelope) => envelopes.push(envelope),
    onResync: () => {
      resyncs += 1
    },
    createSocket: (url: string, protocols: string[]) => {
      expect(url).toBe('wss://example.test/ws')
      expect(protocols).toEqual(['access-auth-token', 'tok'])
      const fake = fakeSocket()
      sockets.push(fake)
      return fake
    },
    schedule: (fn, ms) => {
      timers.push({ fn, ms })
      return timers.length - 1
    },
    clearSchedule: () => {},
  })
  return { sockets, timers, envelopes, socket, resyncCount: () => resyncs }
}

function open(socket: SocketLike): void {
  socket.onopen?.()
}

function drop(socket: SocketLike): void {
  socket.onclose?.()
}

describe('exchange socket', () => {
  it('replays subscriptions on open and resyncs', () => {
    const harness = setup()
    harness.socket.subscribe('ticker')
    harness.socket.connect()
    expect(harness.sockets).toHaveLength(1)
    open(harness.sockets[0])
    const sent = JSON.parse((harness.sockets[0] as { sent: string[] }).sent[0]) as {
      op: string
      channel: string
    }
    expect(sent).toEqual({ op: 'subscribe', channel: 'ticker' })
    expect(harness.resyncCount()).toBe(1)
    expect(harness.socket.connected.value).toBe(true)
  })

  it('delivers envelopes and drops malformed frames', () => {
    const harness = setup()
    harness.socket.connect()
    open(harness.sockets[0])
    harness.sockets[0].onmessage?.({ data: '{"channel":"ticker","data":{"market":"BTC/USDT"}}' })
    harness.sockets[0].onmessage?.({ data: 'not json' })
    expect(harness.envelopes).toEqual([{ channel: 'ticker', data: { market: 'BTC/USDT' } }])
  })

  it('reconnects with capped exponential backoff and resubscribes', () => {
    const harness = setup()
    harness.socket.connect()
    open(harness.sockets[0])
    // attempt resets on every open: drops without any open double the
    // delay each time (1s, 2s, 4s), then one open proves the reset to
    // 1s. Every open resubscribes and resyncs.
    drop(harness.sockets[harness.sockets.length - 1])
    expect(harness.timers[harness.timers.length - 1].ms).toBe(1000)
    for (const expected of [2000, 4000]) {
      harness.timers[harness.timers.length - 1].fn()
      drop(harness.sockets[harness.sockets.length - 1])
      expect(harness.timers[harness.timers.length - 1].ms).toBe(expected)
    }
    harness.timers[harness.timers.length - 1].fn()
    open(harness.sockets[harness.sockets.length - 1])
    drop(harness.sockets[harness.sockets.length - 1])
    expect(harness.timers[harness.timers.length - 1].ms).toBe(1000)
    harness.timers[harness.timers.length - 1].fn()
    open(harness.sockets[harness.sockets.length - 1])
    expect(harness.resyncCount()).toBe(3)
  })

  it('caps the backoff at thirty seconds and stops after disconnect', () => {
    const harness = setup()
    harness.socket.connect()
    open(harness.sockets[0])
    // attempt grows on every drop without an open: 1s, 2s, 4s, 8s,
    // 16s, then the 30s cap.
    for (let i = 0; i < 5; i += 1) {
      drop(harness.sockets[harness.sockets.length - 1])
      harness.timers[harness.timers.length - 1].fn()
    }
    drop(harness.sockets[harness.sockets.length - 1])
    expect(harness.timers[harness.timers.length - 1].ms).toBe(30000)
    harness.timers[harness.timers.length - 1].fn()
    // The reconnected socket is live: disconnect must close it, and a
    // stale timer firing afterwards must not reconnect.
    const lastSocket = harness.sockets[harness.sockets.length - 1]
    const close = vi.fn()
    lastSocket.close = close
    harness.socket.disconnect()
    expect(close).toHaveBeenCalled()
    expect(harness.socket.connected.value).toBe(false)
    const sockets = harness.sockets.length
    const lastTimer = harness.timers[harness.timers.length - 1]
    lastTimer.fn()
    expect(harness.sockets).toHaveLength(sockets)
  })
})
