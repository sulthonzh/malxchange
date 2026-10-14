// @vitest-environment jsdom
import { flushPromises } from '@vue/test-utils'
import { computed } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import Incidents from '../src/pages/operations/incidents.vue'

// The incidents page (issue #60): wallet's incidents filtered by
// status, with resolve — the audit row is the server's business, the
// page renders the list and the refusal/error sentences.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const openIncident = {
  id: 'inc-1',
  coin: 'BTC',
  kind: 'reorg',
  detail: 'depth 5 > window 3 on BTC — credited deposit invalidated',
  status: 'open',
  createdAt: '2026-01-02 03:04:05+00',
}

describe('incidents page', () => {
  const session = sessionRef({
    employee: { id: 'e-1', email: 'finance@exchange.example.com', role: 'finance' },
    mustRotate: false,
    totpBound: true,
    permissions: ['view:incident', 'resolve:incident'],
  })

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    session.value = {
      employee: { id: 'e-1', email: 'finance@exchange.example.com', role: 'finance' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:incident', 'resolve:incident'],
    }
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch, apiBaseUrl: 'http://admin-api.test' }),
      useAdminSession: () => ({ session, loggedIn: computed(() => true) }),
    })
    collaborators.apiFetch.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/api/incidents/inc-1/resolve' && options?.method === 'POST') {
        return Promise.resolve({ ok: true })
      }
      if (path.startsWith('/api/incidents')) {
        return Promise.resolve([openIncident])
      }
      return Promise.reject(new Error(`unexpected fetch ${path}`))
    })
  })

  it('lists open incidents with the default open filter', async () => {
    const wrapper = mountPage(Incidents)
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/incidents?status=open')
    const rows = wrapper.find('[data-test="incident-rows"]')
    expect(rows.text()).toContain('BTC')
    expect(rows.text()).toContain('reorg')
    expect(rows.text()).toContain('depth 5 > window 3')
    expect(rows.text()).toContain('open')
    expect(wrapper.find('[data-test="resolve-inc-1"]').exists()).toBe(true)
  })

  it('resolving an incident posts to the resolve endpoint and reloads', async () => {
    const wrapper = mountPage(Incidents)
    await flushPromises()
    await settle()

    await wrapper.find('[data-test="resolve-inc-1"]').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/incidents/inc-1/resolve', {
      method: 'POST',
      body: {},
    })
    // The reload after resolution re-queries the open list.
    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/incidents?status=open')
  })

  it('renders the server sentence when the resolve is refused', async () => {
    collaborators.apiFetch.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/api/incidents/inc-1/resolve' && options?.method === 'POST') {
        return Promise.reject(
          Object.assign(new Error('The wallet service is unreachable. Try again.'), { status: 503 }),
        )
      }
      return Promise.resolve([openIncident])
    })
    const wrapper = mountPage(Incidents)
    await flushPromises()
    await settle()

    await wrapper.find('[data-test="resolve-inc-1"]').trigger('click')
    await flushPromises()
    await settle()

    expect(wrapper.text()).toContain('The wallet service is unreachable. Try again.')
  })
})
