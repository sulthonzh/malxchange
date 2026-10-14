// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import AuditLog from '../src/pages/audit/log.vue'

// The audit log search (issue #57): filter by actor, action, target,
// and time range; rows render actor, action (verb:resource), target,
// origin IP, and the before/after JSON. A server refusal renders as a
// visible error.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const createRow = {
  id: 14,
  actor: 'root@exchange.example.com',
  action: 'create:employee',
  target: 'e-2',
  before: null,
  after: { id: 'e-2', email: 'employee@exchange.example.com', enabled: true },
  origin_ip: '203.0.113.7',
  created_at: '2026-02-01T12:30:00Z',
}
const grantRow = {
  id: 15,
  actor: 'root@exchange.example.com',
  action: 'grant:role',
  target: 'r-support',
  before: { permission: 'view:employee', granted: false },
  after: { permission: 'view:employee', granted: true },
  origin_ip: '203.0.113.7',
  created_at: '2026-02-01T12:31:00Z',
}

describe('audit log page', () => {
  const session = sessionRef({
    employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
    mustRotate: false,
    totpBound: true,
    permissions: ['view:audit-log'],
  })

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    session.value = {
      employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:audit-log'],
    }
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
      useAdminSession: () => ({ session }),
    })
  })

  function searchAnswer(): unknown {
    return { rows: [createRow, grantRow], total: 2, page: 1, page_size: 50 }
  }

  function mountWith(handlers: Record<string, (body: unknown) => unknown>): VueWrapper {
    collaborators.apiFetch.mockImplementation((path: string, opts?: { method?: string; body?: unknown }) => {
      const handler = handlers[path]
      if (!handler) {
        return Promise.reject(new Error(`unexpected api call: ${opts?.method ?? 'GET'} ${path}`))
      }
      return Promise.resolve(handler(opts?.body))
    })
    return mountPage(AuditLog)
  }

  it('loads on mount and renders every row field', async () => {
    const wrapper = mountWith({ '/api/audit-log': searchAnswer })
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/audit-log')
    const rows = wrapper.findAll('.audit-table .el-table__row')
    expect(rows).toHaveLength(2)
    const create = rows[0]!
    expect(create.text()).toContain('root@exchange.example.com')
    expect(create.text()).toContain('create:employee')
    expect(create.text()).toContain('e-2')
    expect(create.text()).toContain('203.0.113.7')
    // Null halves render as an em dash, not the string "null".
    expect(create.find('.before-json').text()).toContain('—')
    expect(create.find('.after-json').text()).toContain('"email":"employee@exchange.example.com"')
    const grant = rows[1]!
    expect(grant.find('.before-json').text()).toContain('"granted":false')
    expect(grant.find('.after-json').text()).toContain('"granted":true')
  })

  it('searches with the filled filters as one query', async () => {
    const wrapper = mountWith({
      '/api/audit-log': searchAnswer,
      '/api/audit-log?actor=root%40exchange.example.com&action=create%3Aemployee&target=e-2': searchAnswer,
    })
    await flushPromises()
    await settle()

    await wrapper.find('input[name="actor"]').setValue('root@exchange.example.com')
    await wrapper.find('input[name="action"]').setValue('create:employee')
    await wrapper.find('input[name="target"]').setValue('e-2')
    await wrapper.findAll('button').find((button) => button.text() === 'Search')!.trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith(
      '/api/audit-log?actor=root%40exchange.example.com&action=create%3Aemployee&target=e-2',
    )
  })

  it('renders a server refusal as a visible error', async () => {
    const wrapper = mountWith({
      '/api/audit-log': () => {
        throw new Error('The from filter must be an RFC 3339 timestamp.')
      },
    })
    await flushPromises()
    await settle()

    expect(wrapper.find('.action-error').exists()).toBe(true)
    expect(wrapper.find('.action-error').text()).toContain(
      'The from filter must be an RFC 3339 timestamp.',
    )
  })

  it('pages through results keeping the filters', async () => {
    // Sixty hits over a 50-row page: a second page exists and the next
    // button is live.
    const paged = (): unknown => ({ rows: [createRow, grantRow], total: 60, page: 1, page_size: 50 })
    const wrapper = mountWith({
      '/api/audit-log': paged,
      '/api/audit-log?actor=root%40exchange.example.com': paged,
      '/api/audit-log?actor=root%40exchange.example.com&page=2': paged,
    })
    await flushPromises()
    await settle()

    await wrapper.find('input[name="actor"]').setValue('root@exchange.example.com')
    await wrapper.findAll('button').find((button) => button.text() === 'Search')!.trigger('click')
    await flushPromises()
    await settle()

    const next = wrapper.find('.el-pagination .btn-next')
    expect(next.exists()).toBe(true)
    await next.trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith(
      '/api/audit-log?actor=root%40exchange.example.com&page=2',
    )
  })
})
