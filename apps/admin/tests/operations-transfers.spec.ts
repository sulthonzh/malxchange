// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { computed } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import Transfers from '../src/pages/operations/transfers.vue'

// The custody transfers page (issue #60): hot→cold creation and retry
// (both Step-up — the code field rides the request), the cold→hot
// paper-trail form, and the live state lists.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const hotRows = [
  {
    id: 'ht-1',
    coin: 'BTC',
    amount: '1.5',
    address: 'cold-btc-custody',
    state: 'failed',
    txid: '',
    error: 'broadcast: timeout',
    actor: 'finance@exchange.example.com',
    createdAt: '2026-01-02 03:04:05+00',
  },
]

const coldRows = [
  {
    id: 'ct-1',
    coin: 'BTC',
    amount: '2',
    txid: 'abc123',
    actor: 'finance@exchange.example.com',
    notedAt: '2026-01-02 03:04:05+00',
  },
]

describe('transfers page', () => {
  const session = sessionRef({
    employee: { id: 'e-1', email: 'finance@exchange.example.com', role: 'finance' },
    mustRotate: false,
    totpBound: true,
    permissions: ['view:hot-transfer', 'view:cold-transfer', 'create:hot-transfer', 'retry:hot-transfer', 'record:cold-transfer'],
  })

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    session.value = {
      employee: { id: 'e-1', email: 'finance@exchange.example.com', role: 'finance' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:hot-transfer', 'view:cold-transfer', 'create:hot-transfer', 'retry:hot-transfer', 'record:cold-transfer'],
    }
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch, apiBaseUrl: 'http://admin-api.test' }),
      useAdminSession: () => ({ session, loggedIn: computed(() => true) }),
    })
    collaborators.apiFetch.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/api/hot-transfers' && options?.method === 'POST') {
        return Promise.resolve({ id: 'ht-new', coin: 'BTC', state: 'pending' })
      }
      if (path === '/api/hot-transfers/ht-1/retry') {
        return Promise.resolve({ ok: true })
      }
      if (path === '/api/cold-transfers' && options?.method === 'POST') {
        return Promise.resolve({ ok: true })
      }
      if (path.startsWith('/api/hot-transfers')) {
        return Promise.resolve(hotRows)
      }
      if (path.startsWith('/api/cold-transfers')) {
        return Promise.resolve(coldRows)
      }
      return Promise.reject(new Error(`unexpected fetch ${path}`))
    })
  })

  async function mountFilled(): Promise<VueWrapper> {
    const wrapper = mountPage(Transfers)
    await flushPromises()
    await settle()
    return wrapper
  }

  it('lists hot transfers with live state and a retry on failed rows', async () => {
    const wrapper = await mountFilled()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/hot-transfers')
    const rows = wrapper.find('[data-test="hot-rows"]')
    expect(rows.text()).toContain('BTC')
    expect(rows.text()).toContain('1.5')
    expect(rows.text()).toContain('failed')
    expect(rows.text()).toContain('broadcast: timeout')
    expect(wrapper.find('[data-test="hot-retry"]').exists()).toBe(true)
  })

  it('creates a hot→cold transfer with the step-up code and reloads the list', async () => {
    const wrapper = await mountFilled()

    const inputs = wrapper.findAll('[data-test="create-hot"] input')
    await inputs[0]?.setValue('BTC')
    await inputs[1]?.setValue('0.5')
    await inputs[2]?.setValue('123456')
    await wrapper.find('[data-test="hot-submit"]').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/hot-transfers', {
      method: 'POST',
      body: { coin: 'BTC', amount: '0.5', code: '123456' },
    })
    expect(wrapper.text()).toContain('pending signature')
  })

  it('retries a failed transfer with the step-up code from the form', async () => {
    const wrapper = await mountFilled()
    const inputs = wrapper.findAll('[data-test="create-hot"] input')
    await inputs[2]?.setValue('654321')

    await wrapper.find('[data-test="hot-retry"]').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/hot-transfers/ht-1/retry', {
      method: 'POST',
      body: { code: '654321' },
    })
    expect(wrapper.text()).toContain('re-armed')
  })

  it('records a cold→hot transfer with its txid', async () => {
    const wrapper = await mountFilled()

    const inputs = wrapper.findAll('[data-test="record-cold"] input')
    await inputs[0]?.setValue('BTC')
    await inputs[1]?.setValue('2')
    await inputs[2]?.setValue('abc123')
    await wrapper.find('[data-test="cold-submit"]').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/cold-transfers', {
      method: 'POST',
      body: { coin: 'BTC', amount: '2', txid: 'abc123' },
    })
    const rows = wrapper.find('[data-test="cold-rows"]')
    expect(rows.text()).toContain('abc123')
  })

  it('renders the server sentence when the creation is refused', async () => {
    collaborators.apiFetch.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/api/hot-transfers' && options?.method === 'POST') {
        return Promise.reject(
          Object.assign(new Error('A fresh authenticator code is required for this action.'), { status: 401 }),
        )
      }
      if (path.startsWith('/api/hot-transfers')) {
        return Promise.resolve(hotRows)
      }
      return Promise.resolve(coldRows)
    })
    const wrapper = await mountFilled()
    const inputs = wrapper.findAll('[data-test="create-hot"] input')
    await inputs[0]?.setValue('BTC')
    await inputs[1]?.setValue('0.5')
    await wrapper.find('[data-test="hot-submit"]').trigger('click')
    await flushPromises()
    await settle()

    expect(wrapper.text()).toContain('A fresh authenticator code is required for this action.')
  })
})
