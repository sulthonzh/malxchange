// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import Markets from '../src/pages/config/markets.vue'

// The Market editor (issue #61): the exchange's Market config list with
// an inline editor for super-admin. Null collars are blank inputs and
// a cleared collar travels as JSON null (unbounded); the freeze guard's
// 409 sentence renders as-is; only changed fields travel.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const btcUsdt = {
  market: 'BTC/USDT', baseCoin: 'BTC', quoteCoin: 'USDT',
  baseScale: 8, quoteScale: 2, tickSize: '0.10', lotSize: '0.0001',
  makerFeeRate: '0.001', takerFeeRate: '0.001',
  minPrice: null, maxPrice: null, minAmount: '0', maxAmount: null,
  minTurnover: '5', marketOrdersEnabled: true,
  enabled: true, visible: true, sort: 1, zone: 'main', featured: false,
}

const permissions = ['view:market', 'edit:market']

describe('markets page', () => {
  const session = sessionRef(null)

  function superSession() {
    session.value = {
      employee: { id: 'e-1', email: 'root@x.test', role: 'super-admin' },
      mustRotate: false,
      totpBound: true,
      permissions: [...permissions],
    }
  }

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    collaborators.apiFetch.mockImplementation(async (path: string) => {
      if (path === '/api/markets') {
        return [{ ...btcUsdt }]
      }
      return {}
    })
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
      useAdminSession: () => ({ session }),
    })
  })

  async function mountMarkets(): Promise<VueWrapper> {
    const wrapper = mountPage(Markets)
    await flushPromises()
    await settle()
    return wrapper
  }

  async function openEditor(wrapper: VueWrapper): Promise<void> {
    await wrapper.findAll('button').find((b) => b.text() === 'Edit')!.trigger('click')
    await settle()
  }

  it('lists the Market config with state, fees, and collars', async () => {
    superSession()
    const wrapper = await mountMarkets()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/markets')
    expect(wrapper.text()).toContain('BTC/USDT')
    expect(wrapper.text()).toContain('0.001')
    expect(wrapper.text()).toContain('0.10')
    // enabled + visible, unbounded price collar rendered.
    expect(wrapper.text()).toContain('enabled')
    expect(wrapper.text()).toContain('visible')
    expect(wrapper.text()).toContain('∞')
  })

  it('sends a fee and flag diff and re-lists after the save', async () => {
    superSession()
    const wrapper = await mountMarkets()
    await openEditor(wrapper)

    await wrapper.find('input[name="makerFeeRate"]').setValue('0.002')
    await wrapper.find('input[name="sort"]').setValue('7')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/markets/BTC/USDT', {
      method: 'PUT',
      body: { makerFeeRate: '0.002', sort: 7 },
    })
    // Initial list + the PUT + the post-save re-list.
    expect(collaborators.apiFetch).toHaveBeenCalledTimes(3)
  })

  it('sends a cleared collar as null and keeps an untouched null quiet', async () => {
    superSession()
    collaborators.apiFetch.mockImplementation(async (path: string) => {
      if (path === '/api/markets') {
        return [{ ...btcUsdt, maxAmount: '50' }]
      }
      return {}
    })
    const wrapper = await mountMarkets()
    await openEditor(wrapper)

    // minPrice was null and stays untouched → absent from the body.
    // maxAmount was 50 and is cleared → explicit null (unbounded).
    await wrapper.find('input[name="maxAmount"]').setValue('')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/markets/BTC/USDT', {
      method: 'PUT',
      body: { maxAmount: null },
    })
  })

  it('renders the freeze-guard refusal sentence and keeps the editor open', async () => {
    superSession()
    collaborators.apiFetch.mockImplementation(async (path: string) => {
      if (path === '/api/markets') {
        return [{ ...btcUsdt }]
      }
      throw new Error('market config frozen while orders rest: BTC/USDT has 2 open orders')
    })
    const wrapper = await mountMarkets()
    await openEditor(wrapper)

    await wrapper.find('input[name="tickSize"]').setValue('0.01')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(wrapper.find('.el-alert__title').text()).toContain(
      'market config frozen while orders rest: BTC/USDT has 2 open orders',
    )
    expect(wrapper.find('.editor').exists()).toBe(true)
  })

  it('is read-only without the edit permission', async () => {
    session.value = {
      employee: { id: 'e-2', email: 'view@x.test', role: 'support' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:market'],
    }
    const wrapper = await mountMarkets()

    expect(wrapper.text()).toContain('BTC/USDT')
    expect(wrapper.findAll('button').find((b) => b.text() === 'Edit')).toBeUndefined()
  })
})
