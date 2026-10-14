// @vitest-environment jsdom
import { flushPromises } from '@vue/test-utils'
import { computed } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import OpsDashboard from '../src/pages/operations/index.vue'

// The Wallet Ops dashboard (issue #60): the /api/ops summary — failed
// operation counts, ceiling/floor alerts, hot balances per Coin, sweep
// pauses with a permission-gated resume/pause switch.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const summaryPayload = {
  alerts: [
    { coin: 'BTC', kind: 'hot_ceiling', detail: 'hot balance 11 above ceiling 10 — move excess to cold' },
    { coin: 'ETH', kind: 'hot_floor', detail: 'hot balance 2 below floor 5 — withdrawal liquidity at risk' },
  ],
  hotBalances: { BTC: '11', ETH: '2' },
  failed: { withdrawals: 1, sweeps: 0, hotTransfers: 2 },
  openIncidents: 3,
  sweepsPaused: ['LTC'],
  foldOK: true,
}

describe('ops dashboard page', () => {
  const session = sessionRef({
    employee: { id: 'e-1', email: 'finance@exchange.example.com', role: 'finance' },
    mustRotate: false,
    totpBound: true,
    permissions: ['view:ops', 'pause:sweep'],
  })

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    session.value = {
      employee: { id: 'e-1', email: 'finance@exchange.example.com', role: 'finance' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:ops', 'pause:sweep'],
    }
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch, apiBaseUrl: 'http://admin-api.test' }),
      useAdminSession: () => ({ session, loggedIn: computed(() => true) }),
    })
  })

  it('renders the failed-operation counts, alerts, and hot balances per Coin', async () => {
    collaborators.apiFetch.mockResolvedValue(summaryPayload)
    const wrapper = mountPage(OpsDashboard)
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/ops')
    expect(wrapper.find('[data-test="failed-withdrawals"]').text()).toBe('1')
    expect(wrapper.find('[data-test="failed-sweeps"]').text()).toBe('0')
    expect(wrapper.find('[data-test="failed-hot-transfers"]').text()).toBe('2')
    expect(wrapper.find('[data-test="open-incidents"]').text()).toBe('3')
    expect(wrapper.text()).toContain('BTC — above ceiling')
    expect(wrapper.text()).toContain('hot balance 11 above ceiling 10')
    expect(wrapper.text()).toContain('ETH — below floor')
    // BTC and ETH carry balances, LTC only the pause row.
    const rows = wrapper.find('[data-test="coin-rows"]')
    expect(rows.text()).toContain('BTC')
    expect(rows.text()).toContain('11')
    expect(rows.text()).toContain('LTC')
    expect(rows.text()).toContain('paused')
  })

  it('flags the fold failure on the summary banner', async () => {
    collaborators.apiFetch.mockResolvedValue({ ...summaryPayload, foldOK: false })
    const wrapper = mountPage(OpsDashboard)
    await flushPromises()
    await settle()

    expect(wrapper.text()).toContain('Ledger does not fold onto the Journal')
  })

  it('toggling a paused Coin resumes its sweeps over the pause endpoint', async () => {
    collaborators.apiFetch.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/api/coins/LTC/sweep-pause' && options?.method === 'POST') {
        return Promise.resolve({ ok: true })
      }
      return Promise.resolve(summaryPayload)
    })
    const wrapper = mountPage(OpsDashboard)
    await flushPromises()
    await settle()

    // The page's contract is the change event carrying the new value —
    // emit it on the switch component rather than simulating
    // element-plus's internal click handling.
    const switches = wrapper.find('[data-test="coin-rows"]').findAllComponents({ name: 'ElSwitch' })
    expect(switches.length).toBe(3)
    await switches[2]?.vm.$emit('change', false)
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/coins/LTC/sweep-pause', {
      method: 'POST',
      body: { paused: false },
    })
  })

  it('renders the api sentence when the summary fails', async () => {
    collaborators.apiFetch.mockRejectedValue(
      Object.assign(new Error('The wallet service is unreachable. Try again.'), { status: 503 }),
    )
    const wrapper = mountPage(OpsDashboard)
    await flushPromises()
    await settle()

    expect(wrapper.text()).toContain('The wallet service is unreachable. Try again.')
  })

  it('disables the pause switch without the pause:sweep permission', async () => {
    session.value = {
      employee: { id: 'e-2', email: 'viewer@exchange.example.com', role: 'finance' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:ops'],
    }
    collaborators.apiFetch.mockResolvedValue(summaryPayload)
    const wrapper = mountPage(OpsDashboard)
    await flushPromises()
    await settle()

    expect(wrapper.find('[data-test="coin-rows"] .el-switch').classes()).toContain('is-disabled')
  })
})
