// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import ChannelsPage from '../src/pages/config/channels.vue'

// The Code channel surface (ticket #62): status list, activation with
// the per-driver credential form + fresh authenticator code, and
// deactivation. The server is the real gate and api validates the
// credentials — these specs pin what the page sends and, above all,
// the write-only rule: credential inputs never outlive a save attempt
// and the page never fetches credentials back.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const channelRows = [
  { channel: 'sms', active: false, activatedAt: null, updatedAt: '2026-01-02T03:04:05Z' },
  {
    channel: 'telegram',
    active: true,
    activatedAt: '2026-01-02T03:04:05Z',
    updatedAt: '2026-01-02T03:04:05Z',
  },
  { channel: 'whatsapp', active: false, activatedAt: null, updatedAt: '2026-01-02T03:04:05Z' },
]

// element-plus's table observes its container; jsdom has no
// ResizeObserver.
vi.stubGlobal(
  'ResizeObserver',
  class {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  },
)

describe('code channels page', () => {
  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
    })
  })

  // element-plus renders a throwaway copy of the cell slots inside
  // .hidden-columns whose clicks drive nothing — select within the
  // real table body.
  function findButton(wrapper: VueWrapper, label: string) {
    const body = wrapper.find('.el-table__body-wrapper')
    const scope = body.exists() ? body : wrapper
    const button = scope.findAll('button').find((b) => b.text() === label)
    expect(button, `button ${label}`).toBeDefined()
    return button!
  }

  async function openActivateForm(wrapper: VueWrapper, label = 'Activate'): Promise<void> {
    await findButton(wrapper, label).trigger('click')
    await settle()
  }

  it('renders the channel status list and never asks api for credentials', async () => {
    collaborators.apiFetch.mockResolvedValue(channelRows)
    const wrapper = mountPage(ChannelsPage)
    await flushPromises()

    expect(collaborators.apiFetch).toHaveBeenCalledTimes(1)
    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/channels')
    const text = wrapper.text()
    expect(text).toContain('sms')
    expect(text).toContain('telegram')
    expect(text).toContain('whatsapp')
    expect(text).toContain('Inactive')
    expect(text).toContain('Active')
  })

  it('shows the driver fields of the channel being activated and posts credentials with the code', async () => {
    collaborators.apiFetch.mockResolvedValue(channelRows)
    const wrapper = mountPage(ChannelsPage)
    await flushPromises()
    await openActivateForm(wrapper)

    // sms fields only — the driver shapes the form.
    expect(wrapper.find('input[name="credentials-endpoint"]').exists()).toBe(true)
    expect(wrapper.find('input[name="credentials-apiKey"]').exists()).toBe(true)
    expect(wrapper.find('input[name="credentials-botToken"]').exists()).toBe(false)
    expect(wrapper.find('input[name="credentials-phoneNumberId"]').exists()).toBe(false)

    collaborators.apiFetch.mockClear()
    collaborators.apiFetch
      .mockResolvedValueOnce(undefined) // the activate call
      .mockResolvedValueOnce(channelRows) // the refresh after it
    await wrapper.find('input[name="credentials-endpoint"]').setValue('http://sms.local/send')
    await wrapper.find('input[name="credentials-apiKey"]').setValue('k1')
    await wrapper.find('input[name="code"]').setValue('123456')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenNthCalledWith(1, '/api/channels/sms/activate', {
      method: 'POST',
      body: { credentials: { endpoint: 'http://sms.local/send', apiKey: 'k1' }, code: '123456' },
    })
    // The write-only rule: the credential inputs never outlive the save.
    expect(wrapper.find('input[name="credentials-endpoint"]').exists()).toBe(false)
    expect(wrapper.find('input[name="credentials-apiKey"]').exists()).toBe(false)
    expect(wrapper.find('input[name="code"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('k1')
  })

  it('surfaces the api refusal of invalid credentials in the form', async () => {
    collaborators.apiFetch.mockResolvedValue(channelRows)
    const wrapper = mountPage(ChannelsPage)
    await flushPromises()
    await openActivateForm(wrapper)

    collaborators.apiFetch.mockRejectedValueOnce(
      new Error('channel sms needs credentials endpoint and apiKey'),
    )
    await wrapper.find('input[name="credentials-endpoint"]').setValue('http://sms.local/send')
    await wrapper.find('input[name="credentials-apiKey"]').setValue('wrong-driver-key')
    await wrapper.find('input[name="code"]').setValue('123456')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(wrapper.find('[role="alert"]').text()).toContain(
      'channel sms needs credentials endpoint and apiKey',
    )
    // A failed save keeps the form open so the operator can fix it.
    expect(wrapper.find('input[name="credentials-endpoint"]').exists()).toBe(true)
  })

  it('blocks an incomplete credential save before any request', async () => {
    collaborators.apiFetch.mockResolvedValue(channelRows)
    const wrapper = mountPage(ChannelsPage)
    await flushPromises()
    await openActivateForm(wrapper)

    await wrapper.find('input[name="credentials-endpoint"]').setValue('http://sms.local/send')
    await wrapper.find('form').trigger('submit')
    await settle()

    const activations = collaborators.apiFetch.mock.calls.filter((call) =>
      String(call[0]).includes('/activate'),
    )
    expect(activations).toHaveLength(0)
    expect(wrapper.find('[role="alert"]').text()).toContain('API key')
  })

  it('deactivates an active channel behind an inline confirmation', async () => {
    collaborators.apiFetch.mockResolvedValue(channelRows)
    const wrapper = mountPage(ChannelsPage)
    await flushPromises()

    collaborators.apiFetch.mockClear()
    collaborators.apiFetch
      .mockResolvedValueOnce(undefined) // the deactivate call
      .mockResolvedValueOnce(channelRows) // the refresh after it
    await findButton(wrapper, 'Deactivate').trigger('click')
    await settle()
    await findButton(wrapper, 'Yes, deactivate').trigger('click')
    await flushPromises()

    expect(collaborators.apiFetch).toHaveBeenCalledTimes(2)
    expect(collaborators.apiFetch).toHaveBeenNthCalledWith(1, '/api/channels/telegram/deactivate', {
      method: 'POST',
      body: {},
    })
  })

  it('renders the loading failure instead of a broken table', async () => {
    collaborators.apiFetch.mockRejectedValue(new Error('The channel service is unreachable right now. Try again shortly.'))
    const wrapper = mountPage(ChannelsPage)
    await flushPromises()

    expect(wrapper.find('[role="alert"]').text()).toContain(
      'The channel service is unreachable right now.',
    )
  })
})
