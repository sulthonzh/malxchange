// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import Employees from '../src/pages/system/employees.vue'

// The Employee management page (issue #57): the Employee list, creation
// behind a Step-up prompt, enable/disable, password/TOTP reset behind
// Step-up, and Role assignment. The guards (self-disable, self-role
// change, last-super-admin lockout) are prevented client-side and any
// server refusal renders as a visible error — never a silent no-op.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const rootEmployee = {
  id: 'e-1',
  email: 'root@exchange.example.com',
  role_id: 'r-super',
  role: 'super-admin',
  enabled: true,
  totp_bound: true,
  must_rotate: false,
  created_at: '2026-01-01T00:00:00Z',
}

const supportEmployee = {
  id: 'e-2',
  email: 'employee@exchange.example.com',
  role_id: 'r-support',
  role: 'support',
  enabled: true,
  totp_bound: true,
  must_rotate: false,
  created_at: '2026-02-01T00:00:00Z',
}

const disabledEmployee = {
  ...supportEmployee,
  email: 'furloughed@exchange.example.com',
  enabled: false,
  totp_bound: false,
  must_rotate: true,
}

const roles = [
  { id: 'r-super', name: 'super-admin', system: true, in_use: true, permissions: [] },
  { id: 'r-fin', name: 'finance', system: true, in_use: false, permissions: [] },
  { id: 'r-support', name: 'support', system: true, in_use: false, permissions: [] },
]

describe('employees page', () => {
  const session = sessionRef({
    employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
    mustRotate: false,
    totpBound: true,
    permissions: ['view:employee', 'view:role'],
  })

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    session.value = {
      employee: { id: 'e-1', email: 'root@exchange.example.com', role: 'super-admin' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:employee', 'view:role'],
    }
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
    return Promise.resolve(mountPage(Employees))
  }

  function rowOf(wrapper: VueWrapper, email: string) {
    const row = wrapper.findAll('.el-table__row').find((row) => row.text().includes(email))
    expect(row, `no table row for ${email}`).toBeTruthy()
    return row!
  }

  function buttonIn(scope: VueWrapper, label: string) {
    const button = scope.findAll('button').find((button) => button.text().trim() === label)
    expect(button, `no ${label} button`).toBeTruthy()
    return button!
  }

  it('renders the Employee list with state tags', async () => {
    const wrapper = await mountWith({
      'GET /api/employees': () => [rootEmployee, disabledEmployee],
      'GET /api/roles': () => roles,
    })
    await flushPromises()
    await settle()

    expect(rowOf(wrapper, 'root@exchange.example.com').exists()).toBe(true)
    const disabledRow = rowOf(wrapper, 'furloughed@exchange.example.com')
    expect(disabledRow.text()).toContain('support')
    expect(disabledRow.text()).toContain('Disabled')
    expect(disabledRow.text()).toContain('One-time password')
    expect(disabledRow.text()).toContain('Two-factor not enrolled')
    expect(buttonIn(disabledRow, 'Enable').exists()).toBe(true)
  })

  it('creates an Employee behind the Step-up prompt and reloads', async () => {
    const create = vi.fn(() => ({ id: 'e-3', email: 'new@exchange.example.com', role: 'finance', must_rotate: true }))
    let listCalls = 0
    const wrapper = await mountWith({
      'GET /api/employees': () => {
        listCalls += 1
        return [rootEmployee, supportEmployee]
      },
      'GET /api/roles': () => roles,
      'POST /api/employees': create,
    })
    await flushPromises()
    await settle()

    await buttonIn(wrapper, 'Create Employee').trigger('click')
    await settle()
    // The open dialog carries its scoping class (the journeys select it).
    expect(wrapper.find('.dialog-create').exists()).toBe(true)

    await wrapper.find('.dialog-create input[name="email"]').setValue('new@exchange.example.com')
    await wrapper.find('.dialog-create input[name="password"]').setValue('One-time-Password-1')
    await wrapper.find('.el-select__wrapper').trigger('click')
    await settle()
    const option = wrapper.findAll('.el-select-dropdown__item').find((item) => item.text() === 'finance')
    expect(option, 'no finance option in the Role select').toBeTruthy()
    await option!.trigger('click')
    await settle()
    await wrapper.find('.dialog-create input[name="code"]').setValue('654321')
    await buttonIn(wrapper, 'Create').trigger('click')
    await flushPromises()
    await settle()

    expect(create).toHaveBeenCalledWith({
      email: 'new@exchange.example.com',
      password: 'One-time-Password-1',
      role_id: 'r-fin',
      code: '654321',
    })
    expect(listCalls).toBe(2) // initial load + reload after the create
  })

  it('prevents self-disable client-side without an api call', async () => {
    const wrapper = await mountWith({
      'GET /api/employees': () => [rootEmployee, supportEmployee],
      'GET /api/roles': () => roles,
    })
    await flushPromises()
    await settle()

    const disable = buttonIn(rowOf(wrapper, 'root@exchange.example.com'), 'Disable')
    expect(disable.attributes('disabled')).toBeDefined()
    await disable.trigger('click')
    await settle()
    expect(collaborators.apiFetch).not.toHaveBeenCalledWith('/api/employees/e-1/disable', expect.anything())
  })

  it('prevents disabling the last enabled super-admin client-side', async () => {
    const deputy = { ...rootEmployee, id: 'e-2', email: 'deputy@exchange.example.com' }
    const wrapper = await mountWith({
      // The actor (session e-1) is not in the list; the deputy is the
      // sole enabled super-admin in it.
      'GET /api/employees': () => [deputy],
      'GET /api/roles': () => roles,
    })
    await flushPromises()
    await settle()

    expect(buttonIn(rowOf(wrapper, 'deputy@exchange.example.com'), 'Disable').attributes('disabled')).toBeDefined()
    expect(collaborators.apiFetch).not.toHaveBeenCalledWith('/api/employees/e-2/disable', expect.anything())
  })

  it('renders a server refusal as a visible error, never silently', async () => {
    collaborators.apiFetch.mockImplementation((path: string, opts?: { method?: string }) => {
      if (path === '/api/employees/e-2/disable' && opts?.method === 'POST') {
        return Promise.reject(new Error('The last enabled super-admin cannot be disabled.'))
      }
      if (path === '/api/employees') return Promise.resolve([rootEmployee, supportEmployee])
      if (path === '/api/roles') return Promise.resolve(roles)
      return Promise.reject(new Error(`unexpected api call: ${opts?.method ?? 'GET'} ${path}`))
    })
    const wrapper = mountPage(Employees)
    await flushPromises()
    await settle()

    const targetRow = rowOf(wrapper, 'employee@exchange.example.com')
    // The stale client list says the guard does not apply; the button
    // is live and the server refusal must surface.
    await buttonIn(targetRow, 'Disable').trigger('click')
    await flushPromises()
    await settle()

    const error = wrapper.find('.action-error')
    expect(error.exists()).toBe(true)
    expect(error.text()).toContain('The last enabled super-admin cannot be disabled.')
  })

  it('demands the Step-up code on password reset and shows the one-time credential', async () => {
    const reset = vi.fn(() => ({ id: 'e-2', must_rotate: true, one_time_password: 'hex-one-time' }))
    const wrapper = await mountWith({
      'GET /api/employees': () => [rootEmployee, supportEmployee],
      'GET /api/roles': () => roles,
      'POST /api/employees/e-2/reset-password': reset,
    })
    await flushPromises()
    await settle()

    await buttonIn(rowOf(wrapper, 'employee@exchange.example.com'), 'Reset password').trigger('click')
    await settle()

    // No code: the client-side guard refuses with a visible sentence
    // and nothing is sent.
    await buttonIn(wrapper, 'Reset').trigger('click')
    await settle()
    expect(reset).not.toHaveBeenCalled()
    expect(wrapper.find('.dialog-reset-password .dialog-error').exists()).toBe(true)
    expect(wrapper.find('.dialog-reset-password .dialog-error').text()).toContain(
      'Enter a fresh authenticator code.',
    )

    await wrapper.find('.dialog-reset-password input[name="code"]').setValue('654321')
    await buttonIn(wrapper, 'Reset').trigger('click')
    await flushPromises()
    await settle()

    expect(reset).toHaveBeenCalledWith({ password: '', code: '654321' })
    const result = wrapper.find('.one-time-result')
    expect(result.exists()).toBe(true)
    expect(result.text()).toContain('hex-one-time')
  })

  it('resets TOTP behind the Step-up prompt', async () => {
    const reset = vi.fn(() => ({ id: 'e-2', totp_bound: false }))
    const wrapper = await mountWith({
      'GET /api/employees': () => [rootEmployee, supportEmployee],
      'GET /api/roles': () => roles,
      'POST /api/employees/e-2/reset-totp': reset,
    })
    await flushPromises()
    await settle()

    await buttonIn(rowOf(wrapper, 'employee@exchange.example.com'), 'Reset two-factor').trigger('click')
    await settle()
    await wrapper.find('input[name="code"]').setValue('654321')
    await buttonIn(wrapper, 'Reset').trigger('click')
    await flushPromises()
    await settle()

    expect(reset).toHaveBeenCalledWith({ code: '654321' })
  })

  it('blocks self-role change client-side and assigns roles otherwise', async () => {
    const assign = vi.fn(() => ({ id: 'e-2', role: 'finance' }))
    const wrapper = await mountWith({
      'GET /api/employees': () => [rootEmployee, supportEmployee],
      'GET /api/roles': () => roles,
      'POST /api/employees/e-2/role': assign,
    })
    await flushPromises()
    await settle()

    expect(buttonIn(rowOf(wrapper, 'root@exchange.example.com'), 'Change role').attributes('disabled')).toBeDefined()

    await buttonIn(rowOf(wrapper, 'employee@exchange.example.com'), 'Change role').trigger('click')
    await settle()
    await wrapper.find('.el-select__wrapper').trigger('click')
    await settle()
    const option = wrapper.findAll('.el-select-dropdown__item').find((item) => item.text() === 'finance')
    await option!.trigger('click')
    await settle()
    await buttonIn(wrapper, 'Confirm').trigger('click')
    await flushPromises()
    await settle()

    expect(assign).toHaveBeenCalledWith({ role_id: 'r-fin' })
  })
})
