// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import EngineStatus from '../src/pages/ops/engine.vue'

// The engine status page (issue #61): per-Market Book Ready / open
// Orders / Halted plus the consumer lag — visible to all four Roles,
// refreshing on the engine's five-second apply cadence.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const statusBody = {
  ok: true,
  markets: [
    { market: 'BTC/USDT', ready: true, enabled: true, halted: false, openOrders: 0, lastSeq: 7 },
    { market: 'ETH/USDT', ready: true, enabled: false, halted: true, openOrders: 3, lastSeq: 41 },
  ],
  consumerLag: 2,
  at: '2026-10-14T12:00:00Z',
}

describe('engine status page', () => {
  const session = sessionRef(null)

  beforeEach(() => {
    vi.useFakeTimers()
    collaborators.apiFetch.mockReset()
    collaborators.apiFetch.mockResolvedValue({ ...statusBody, markets: statusBody.markets.map((m) => ({ ...m })) })
    session.value = {
      employee: { id: 'e-3', email: 'view@x.test', role: 'compliance' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:engine'],
    }
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
      useAdminSession: () => ({ session }),
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  async function mountEngine(): Promise<VueWrapper> {
    const wrapper = mountPage(EngineStatus)
    await flushPromises()
    await settle()
    return wrapper
  }

  it('surfaces per-Market Ready/open Orders/Halted and the lag', async () => {
    const wrapper = await mountEngine()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/engine')
    expect(wrapper.text()).toContain('BTC/USDT')
    expect(wrapper.text()).toContain('ready')
    expect(wrapper.text()).toContain('ETH/USDT')
    expect(wrapper.text()).toContain('halted')
    expect(wrapper.text()).toContain('2 behind')
  })

  it('shows an honest unknown while the broker cannot answer', async () => {
    collaborators.apiFetch.mockResolvedValue({ ok: true, markets: [], consumerLag: null, at: '2026-10-14T12:00:00Z' })
    const wrapper = await mountEngine()

    expect(wrapper.text()).toContain('unknown')
    expect(wrapper.text()).not.toContain('behind')
  })

  it('refreshes every five seconds and stops on unmount', async () => {
    const wrapper = await mountEngine()
    expect(collaborators.apiFetch).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(5000)
    expect(collaborators.apiFetch).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(5000)
    expect(collaborators.apiFetch).toHaveBeenCalledTimes(3)

    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(20000)
    expect(collaborators.apiFetch).toHaveBeenCalledTimes(3)
  })
})
