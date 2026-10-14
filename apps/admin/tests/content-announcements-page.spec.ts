// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import Announcements from '../src/pages/content/announcements.vue'

// The announcements page (issue #63): support's news feed — list with
// drafts visible, create/edit through one dialog (publishing is the
// form's switch), an in-place publish toggle, and delete behind a
// confirm. Every action mirrors the admin-api endpoint exactly; api
// failures render as sentences.

const collaborators = vi.hoisted(() => ({ apiFetch: vi.fn() }))

const rows = [
  {
    id: 'a-2',
    title: 'Second maintenance window',
    content: '<p>Read-only mode on Sunday.</p>',
    published: true,
    created_at: '2026-10-12T08:00:00Z',
    updated_at: '2026-10-12T09:00:00Z',
  },
  {
    id: 'a-1',
    title: 'Listing announced',
    content: '<p>BTC/USDT is live.</p>',
    published: false,
    created_at: '2026-10-10T12:30:00Z',
    updated_at: '2026-10-10T12:30:00Z',
  },
]

// el-dialog renders its slot lazily behind teleporting; the stub keeps
// the form in the wrapper tree so specs drive it like plain markup.
const DialogStub = {
  props: ['modelValue', 'title'],
  template: '<div v-if="modelValue" class="dialog-stub"><h2>{{ title }}</h2><slot /><slot name="footer" /></div>',
}

// The delete confirm: the reference slot plus a button that emits the
// confirm the real popconfirm asks for.
const PopconfirmStub = {
  template:
    '<div><slot name="reference" /><button data-test="popconfirm-confirm" @click="$emit(\'confirm\')">Confirm</button></div>',
}

async function mountAnnouncements(): Promise<VueWrapper> {
  const wrapper = mountPage(Announcements, {
    global: { stubs: { 'el-dialog': DialogStub, 'el-popconfirm': PopconfirmStub } },
  })
  await flushPromises()
  await settle()
  return wrapper
}

function button(wrapper: VueWrapper, text: string) {
  const found = wrapper.findAll('button').find((b) => b.text() === text)
  if (!found) {
    throw new Error(`no button ${text}`)
  }
  return found
}

describe('announcements page', () => {
  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    stubNuxtGlobals(Vue, { useApi: () => ({ apiFetch: collaborators.apiFetch }) })
    // element-plus' table measures itself; jsdom has no ResizeObserver.
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    )
  })

  it('lists announcements with drafts visible and requests the first page', async () => {
    collaborators.apiFetch.mockResolvedValue({ content: rows, total: 2 })
    const wrapper = await mountAnnouncements()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/announcements?pageNo=1&pageSize=10')
    const text = wrapper.find('[data-test="announcement-rows"]').text()
    expect(text).toContain('Second maintenance window')
    expect(text).toContain('Listing announced')
    expect(text).toContain('Published')
    expect(text).toContain('Draft')
    expect(text).toContain('2026-10-12 08:00 UTC')
  })

  it('creates an announcement from the dialog with the publish switch', async () => {
    collaborators.apiFetch.mockResolvedValue({ content: rows, total: 2 })
    const wrapper = await mountAnnouncements()

    await button(wrapper, 'New announcement').trigger('click')
    await settle()
    await wrapper.find('input[name="title"]').setValue('Maintenance window')
    await wrapper.find('textarea[name="content"]').setValue('<p>Read-only.</p>')
    await wrapper.find('input[name="published"]').setValue(true)
    await button(wrapper, 'Create announcement').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/announcements', {
      method: 'POST',
      body: { title: 'Maintenance window', content: '<p>Read-only.</p>', published: true },
    })
  })

  it('edits the prefilled announcement with a PUT', async () => {
    collaborators.apiFetch.mockResolvedValue({ content: rows, total: 2 })
    const wrapper = await mountAnnouncements()

    await button(wrapper, 'Edit').trigger('click')
    await settle()
    expect((wrapper.find('input[name="title"]').element as HTMLInputElement).value).toBe('Second maintenance window')
    expect((wrapper.find('textarea[name="content"]').element as HTMLTextAreaElement).value).toBe(
      '<p>Read-only mode on Sunday.</p>',
    )
    await wrapper.find('input[name="title"]').setValue('Renamed window')
    await button(wrapper, 'Save changes').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/announcements/a-2', {
      method: 'PUT',
      body: { title: 'Renamed window', content: '<p>Read-only mode on Sunday.</p>', published: true },
    })
  })

  it('publishes a draft in place with the inverted flag', async () => {
    collaborators.apiFetch.mockResolvedValue({ content: rows, total: 2 })
    const wrapper = await mountAnnouncements()

    await button(wrapper, 'Publish').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/announcements/a-1', {
      method: 'PUT',
      body: {
        title: 'Listing announced',
        content: '<p>BTC/USDT is live.</p>',
        published: true,
      },
    })
  })

  it('unpublishes a published announcement in place', async () => {
    collaborators.apiFetch.mockResolvedValue({ content: rows, total: 2 })
    const wrapper = await mountAnnouncements()

    await button(wrapper, 'Unpublish').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/announcements/a-2', {
      method: 'PUT',
      body: {
        title: 'Second maintenance window',
        content: '<p>Read-only mode on Sunday.</p>',
        published: false,
      },
    })
  })

  it('deletes only after the confirm', async () => {
    collaborators.apiFetch.mockResolvedValue({ content: rows, total: 2 })
    const wrapper = await mountAnnouncements()

    await wrapper.find('[data-test="delete-a-2"]').trigger('click')
    expect(collaborators.apiFetch).not.toHaveBeenCalledWith('/api/announcements/a-2', { method: 'DELETE' })
    await wrapper.find('[data-test="popconfirm-confirm"]').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/announcements/a-2', { method: 'DELETE' })
  })

  it('renders a load failure as a sentence', async () => {
    collaborators.apiFetch.mockRejectedValue(new Error('You do not have permission to do that.'))
    const wrapper = await mountAnnouncements()

    const alert = wrapper.find('[role="alert"]')
    expect(alert.text()).toBe('You do not have permission to do that.')
  })

  it('refuses to submit a bodyless announcement', async () => {
    collaborators.apiFetch.mockResolvedValue({ content: rows, total: 2 })
    const wrapper = await mountAnnouncements()

    await button(wrapper, 'New announcement').trigger('click')
    await settle()
    await wrapper.find('input[name="title"]').setValue('No body')
    await button(wrapper, 'Create announcement').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).not.toHaveBeenCalledWith(
      '/api/announcements',
      expect.objectContaining({ method: 'POST' }),
    )
    expect(wrapper.text()).toContain('Enter a title and a body before saving.')
  })
})
