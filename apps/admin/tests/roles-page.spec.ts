// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import Roles from '../src/pages/system/roles.vue'

// The Role grant editor (issue #57): the Roles × permission-catalog
// matrix where every toggle persists immediately through the grants
// endpoint, the catalog itself is display-only, and seeded/in-use
// Roles show undeletable — with server refusals rendered as visible
// errors.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

// Fresh Role objects per test: the page mutates `permissions` in place
// when a toggle lands, so shared fixtures would leak between tests.
function superRole() {
  return {
    id: 'r-super',
    name: 'super-admin',
    system: true,
    in_use: true,
    permissions: ['view:employee'],
  }
}
function supportRole() {
  return { id: 'r-support', name: 'support', system: true, in_use: false, permissions: [] }
}
function customRole() {
  return { id: 'r-custom', name: 'auditor', system: false, in_use: true, permissions: [] }
}
const catalog = ['view:employee', 'view:role']

describe('roles page', () => {
  const session = sessionRef({
    employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
    mustRotate: false,
    totpBound: true,
    permissions: ['view:role', 'view:permission', 'delete:role'],
  })

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
      useAdminSession: () => ({ session }),
    })
  })

  function router(handlers: Record<string, (body: unknown) => unknown>): void {
    collaborators.apiFetch.mockImplementation((path: string, opts?: { method?: string; body?: unknown }) => {
      const handler = handlers[`${opts?.method ?? 'GET'} ${path}`]
      if (!handler) {
        return Promise.reject(new Error(`unexpected api call: ${opts?.method ?? 'GET'} ${path}`))
      }
      return Promise.resolve(handler(opts?.body))
    })
  }

  function mountWith(handlers: Record<string, (body: unknown) => unknown>): Promise<VueWrapper> {
    router(handlers)
    return Promise.resolve(mountPage(Roles))
  }

  /** The switch cell at the intersection of a Role column and a catalog row. */
  function switchOf(wrapper: VueWrapper, permission: string, roleId: string) {
    const row = wrapper.findAll('.editor-table .el-table__row').find((row) => row.text().includes(permission))
    expect(row, `no editor row for ${permission}`).toBeTruthy()
    const cell = row!.findAll('.el-table__cell').find((cell) => cell.classes().includes(`role-${roleId}`))
    expect(cell, `no editor cell for ${roleId}`).toBeTruthy()
    return cell!.find('.el-switch')
  }

  it('renders the catalog matrix with live grant state', async () => {
    const wrapper = await mountWith({
      'GET /api/roles': () => [superRole(), supportRole()],
      'GET /api/permissions': () => catalog,
    })
    await flushPromises()
    await settle()

    expect(wrapper.findAll('.editor-table .el-table__row')).toHaveLength(catalog.length)
    const granted = switchOf(wrapper, 'view:employee', 'r-super')
    expect(granted.classes()).toContain('is-checked')
    expect(switchOf(wrapper, 'view:role', 'r-super').classes()).not.toContain('is-checked')
    expect(switchOf(wrapper, 'view:employee', 'r-support').classes()).not.toContain('is-checked')
  })

  it('keeps the catalog display-only', async () => {
    const wrapper = await mountWith({
      'GET /api/roles': () => [superRole(), supportRole()],
      'GET /api/permissions': () => catalog,
    })
    await flushPromises()
    await settle()

    for (const row of wrapper.findAll('.editor-table .el-table__row')) {
      const nameCell = row.find('.permission-name')
      expect(nameCell.exists()).toBe(true)
      expect(nameCell.find('button').exists()).toBe(false)
      expect(nameCell.find('.el-switch').exists()).toBe(false)
      expect(nameCell.text()).toMatch(/^[a-z]+:[a-z-]+$/)
    }
  })

  it('persists a toggle immediately through the grants endpoint', async () => {
    const grant = vi.fn(() => ({ role: 'support', permission: 'view:employee', granted: true }))
    const wrapper = await mountWith({
      'GET /api/roles': () => [superRole(), supportRole()],
      'GET /api/permissions': () => catalog,
      'POST /api/roles/r-support/grants': grant,
    })
    await flushPromises()
    await settle()

    const pending = switchOf(wrapper, 'view:employee', 'r-support')
    expect(pending.classes()).not.toContain('is-checked')
    await pending.find('input').setValue(true)
    await flushPromises()
    await settle()

    expect(grant).toHaveBeenCalledWith({ permission: 'view:employee', granted: true })
    expect(collaborators.apiFetch).toHaveBeenCalledTimes(3) // roles, catalog, one toggle — no save step
    expect(switchOf(wrapper, 'view:employee', 'r-support').classes()).toContain('is-checked')
  })

  it('reverts a refused toggle and renders the refusal', async () => {
    const wrapper = await mountWith({
      'GET /api/roles': () => [superRole(), supportRole()],
      'GET /api/permissions': () => catalog,
      'POST /api/roles/r-support/grants': () => {
        throw new Error('That permission does not exist.')
      },
    })
    await flushPromises()
    await settle()

    await switchOf(wrapper, 'view:employee', 'r-support').find('input').setValue(true)
    await flushPromises()
    await settle()

    expect(switchOf(wrapper, 'view:employee', 'r-support').classes()).not.toContain('is-checked')
    expect(wrapper.find('.action-error').text()).toContain('That permission does not exist.')
  })

  it('shows seeded and in-use Roles undeletable', async () => {
    const wrapper = await mountWith({
      'GET /api/roles': () => [superRole(), supportRole(), customRole()],
      'GET /api/permissions': () => catalog,
    })
    await flushPromises()
    await settle()

    const rowOf = (name: string) =>
      wrapper.findAll('.roles-table .el-table__row').find((row) => row.text().includes(name))!

    // Seeded Roles carry the Seeded tag; a held custom Role the In use
    // tag; both refuse deletion client-side.
    expect(rowOf('super-admin').text()).toContain('Seeded')
    expect(rowOf('auditor').text()).toContain('In use')
    expect(rowOf('super-admin').find('button').attributes('disabled')).toBeDefined()
    expect(rowOf('support').find('button').attributes('disabled')).toBeDefined()
    expect(rowOf('auditor').find('button').attributes('disabled')).toBeDefined()
  })

  it('renders a delete refusal as a visible error', async () => {
    const free = { ...customRole(), id: 'r-free', name: 'temp', in_use: false }
    const wrapper = await mountWith({
      'GET /api/roles': () => [superRole(), free],
      'GET /api/permissions': () => catalog,
      'DELETE /api/roles/r-free': () => {
        throw new Error('This Role is held by an Employee and cannot be deleted.')
      },
    })
    await flushPromises()
    await settle()

    const row = wrapper.findAll('.roles-table .el-table__row').find((candidate) => candidate.text().includes('temp'))!
    expect(row.find('button').attributes('disabled')).toBeUndefined()
    await row.find('button').trigger('click')
    await flushPromises()
    await settle()

    expect(wrapper.find('.action-error').text()).toContain(
      'This Role is held by an Employee and cannot be deleted.',
    )
  })
})
