// @vitest-environment jsdom
import { flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { mountPage } from './page-mount'
import AnnouncementDetail from '../src/pages/announcement/[id].vue'

const contentApi = vi.hoisted(() => ({
  announcements: vi.fn(),
  announcement: vi.fn(),
}))

vi.mock('~/composables/useContent', () => ({ useContent: () => contentApi }))

// The announcement detail page (ticket #50): the legacy NoticeItem two
// column shape — recent announcements beside the article — with the
// contract's back/next adjacent summaries. The body is CMS-stored HTML
// and must render sanitized; a missing or degraded detail answers 404,
// which renders as a sentence, never an error.

const DETAIL = {
  info: {
    id: 'a-2',
    title: 'Second maintenance window',
    createTime: '2026-10-12T08:00:00Z',
    content:
      '<p>Read-only mode on Sunday.</p><script>alert("x")</script>',
  },
  back: { id: 'a-1', title: 'Listing announced', createTime: '2026-10-10T12:30:00Z' },
  next: null,
}

describe('announcement detail page', () => {
  beforeEach(() => {
    contentApi.announcements.mockReset()
    contentApi.announcement.mockReset()
  })

  it('renders the article sanitized, with the adjacent and recent links', async () => {
    contentApi.announcement.mockResolvedValue(DETAIL)
    contentApi.announcements.mockResolvedValue({
      content: [
        { id: 'a-2', title: 'Second maintenance window', createTime: '2026-10-12T08:00:00Z' },
        { id: 'a-1', title: 'Listing announced', createTime: '2026-10-10T12:30:00Z' },
      ],
      totalElements: 2,
    })

    const wrapper = mountPage(AnnouncementDetail, { id: 'a-2' })
    await flushPromises()

    expect(contentApi.announcement).toHaveBeenCalledWith('a-2')
    expect(wrapper.text()).toContain('Second maintenance window')
    expect(wrapper.text()).toContain('Oct 12, 2026')
    // The body rendered through the sanitizer: paragraph kept, script dead.
    expect(wrapper.find('.content p').text()).toBe('Read-only mode on Sunday.')
    expect(wrapper.find('.content script').exists()).toBe(false)
    // Adjacent summary links to the sibling announcement (the sidebar
    // carries the same href, so scope the selector to the nav row).
    expect(wrapper.find('.nav-bottom a[href="/announcement/a-1"]').text()).toContain(
      'Listing announced',
    )
    expect(wrapper.find('.nav-bottom a[href="/announcement/a-1"]').text()).toContain(
      'Previous announcement',
    )
  })

  it('renders a complete-sentence not-found state when the announcement is gone', async () => {
    contentApi.announcement.mockRejectedValue(new Error('announcement not found'))
    contentApi.announcements.mockResolvedValue({ content: [], totalElements: 0 })

    const wrapper = mountPage(AnnouncementDetail, { id: 'a-404' })
    await flushPromises()

    expect(wrapper.text()).toContain(
      'This announcement does not exist or is no longer available.',
    )
  })
})
