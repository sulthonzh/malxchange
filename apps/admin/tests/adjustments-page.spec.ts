// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import Adjustments from '../src/pages/finance/adjustments/index.vue'

// The manual balance adjustment form (issue #59): signed amount +
// mandatory reason, Step-up confirmation dialog, per-submission
// idempotency key minted client-side so a retried request can never
// move money twice. Element-plus's own validation markup is inert
// under jsdom, so the specs pin the server-facing contract: invalid
// input never reaches the api, the confirmation posts exactly
// member/coin/amount/reason/key/code.
//
// NOTE: the zero-amount leg relies on the amount field carrying a
// value — element-plus validates populated custom validators in this
// environment but skips empty-required checks entirely.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

async function fillForm(wrapper: VueWrapper, amount = '25.50'): Promise<void> {
  await wrapper.find('input[name="member"]').setValue('petra')
  await wrapper.find('input[name="coin"]').setValue('USDT')
  await wrapper.find('input[name="amount"]').setValue(amount)
  await wrapper.find('textarea[name="reason"]').setValue('support case #4182')
}

describe('adjustments page', () => {
  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    collaborators.apiFetch.mockResolvedValue({ ok: true, applied: true })
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
    })
  })

  it('sends nothing when the operator submits the bare form', async () => {
    const wrapper = mountPage(Adjustments)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).not.toHaveBeenCalled()
  })

  it('sends nothing for a zero amount', async () => {
    const wrapper = mountPage(Adjustments)
    await fillForm(wrapper, '0')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).not.toHaveBeenCalled()
  })

  it('opens the confirmation dialog on a valid form', async () => {
    const wrapper = mountPage(Adjustments)
    await fillForm(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(wrapper.find('.el-dialog').isVisible()).toBe(true)
    expect(collaborators.apiFetch).not.toHaveBeenCalled()
  })

  it('refuses to confirm without entering a code', async () => {
    const wrapper = mountPage(Adjustments)
    await fillForm(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()
    collaborators.apiFetch.mockClear()

    await wrapper.findAll('button').find((b) => b.text() === 'Confirm')!.trigger('click')
    await settle()

    expect(collaborators.apiFetch).not.toHaveBeenCalled()
  })

  it('confirms with the code, posts member/coin/amount/reason/key/code, and reports success', async () => {
    const wrapper = mountPage(Adjustments)
    await fillForm(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    await wrapper.find('.el-dialog input[name="code"]').setValue('654321')
    await wrapper.findAll('button').find((b) => b.text() === 'Confirm')!.trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledTimes(1)
    const [path, options] = collaborators.apiFetch.mock.calls[0] as [string, { method: string; body: Record<string, unknown> }]
    expect(path).toBe('/api/adjustments')
    expect(options.method).toBe('POST')
    expect(options.body.member).toBe('petra')
    expect(options.body.coin).toBe('USDT')
    expect(options.body.amount).toBe('25.50')
    expect(options.body.reason).toBe('support case #4182')
    expect(typeof options.body.key).toBe('string')
    expect((options.body.key as string).length).toBeGreaterThan(0)
    expect(options.body.code).toBe('654321')
    expect(wrapper.text()).toContain('Adjustment applied')
  })

  it('reports an already-applied replay distinctly', async () => {
    collaborators.apiFetch.mockResolvedValue({ ok: true, applied: false })
    const wrapper = mountPage(Adjustments)
    await fillForm(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()
    await wrapper.find('.el-dialog input[name="code"]').setValue('654321')
    await wrapper.findAll('button').find((b) => b.text() === 'Confirm')!.trigger('click')
    await flushPromises()
    await settle()

    expect(wrapper.text()).toContain('already applied')
  })

  it('renders the api sentence when the adjustment is refused', async () => {
    collaborators.apiFetch.mockRejectedValue(new Error('Enter a non-zero signed amount.'))
    const wrapper = mountPage(Adjustments)
    await fillForm(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()
    await wrapper.find('.el-dialog input[name="code"]').setValue('654321')
    await wrapper.findAll('button').find((b) => b.text() === 'Confirm')!.trigger('click')
    await flushPromises()
    await settle()

    expect(wrapper.find('.error').text()).toContain('Enter a non-zero signed amount.')
  })
})
