// @vitest-environment jsdom
import { flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { mountPage } from './page-mount'
import Notice from '../src/pages/notice.vue'

const contentApi = vi.hoisted(() => ({
  announcements: vi.fn(),
}))

vi.mock('~/composables/useContent', () => ({ useContent: () => contentApi }))

// The announcements list page (ticket #50): pagination passes straight
// through to the api contract (pageNo/pageSize), and the not-yet-admin-api
// case renders a complete-sentence empty state — an unreachable or
// degraded api is indistinguishable from empty content, so no error UI
// exists on this page.

describe('announcements list page', () => {
  beforeEach(() => {
    contentApi.announcements.mockReset()
  })

  it('renders the page with dated rows and passes pagination through to the api', async () => {
    contentApi.announcements
      .mockResolvedValueOnce({
        content: [
          {
            id: 'a-2',
            title: 'Second maintenance window',
            createTime: '2026-10-12T08:00:00Z',
          },
        ],
        totalElements: 12,
      })
      .mockResolvedValueOnce({
        content: [
          { id: 'a-9', title: 'Listing announced', createTime: '2026-10-11T12:30:00Z' },
        ],
        totalElements: 12,
      })

    const wrapper = mountPage(Notice)
    await flushPromises()

    expect(contentApi.announcements).toHaveBeenCalledWith(1, 10)
    expect(wrapper.text()).toContain('Second maintenance window')
    expect(wrapper.text()).toContain('Oct 12, 2026')
    expect(wrapper.find('a[href="/announcement/a-2"]').exists()).toBe(true)

    wrapper.findComponent({ name: 'ElPagination' }).vm.$emit('current-change', 2)
    await flushPromises()

    expect(contentApi.announcements).toHaveBeenCalledWith(2, 10)
    expect(wrapper.text()).toContain('Listing announced')
  })

  it('renders a complete-sentence empty state when no announcements exist', async () => {
    contentApi.announcements.mockResolvedValue({ content: [], totalElements: 0 })

    const wrapper = mountPage(Notice)
    await flushPromises()

    expect(wrapper.text()).toContain('No announcements have been published yet.')
  })

  it('renders the empty state, never an error, when the api cannot be reached', async () => {
    contentApi.announcements.mockRejectedValue(new Error('The api could not be reached.'))

    const wrapper = mountPage(Notice)
    await flushPromises()

    expect(wrapper.text()).toContain('No announcements have been published yet.')
  })
})
