// @vitest-environment jsdom
import { flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { mountPage } from './page-mount'
import HelpDetail from '../src/pages/helpdetail/[id].vue'

const contentApi = vi.hoisted(() => ({
  helpCategories: vi.fn(),
  helpArticle: vi.fn(),
}))

vi.mock('~/composables/useContent', () => ({ useContent: () => contentApi }))

// The help article page (ticket #50): the legacy HelpDetail.vue shape —
// the category sidebar beside the article. The legacy pinned-articles
// (置顶) sidebar is cut by the contract; the sidebar is the article's
// category siblings from the help overview. The body is CMS-stored HTML
// and renders sanitized; a 404 (including the degraded-admin-api case)
// renders a sentence, never an error.

const ARTICLE = {
  id: 'h-1',
  title: 'How to deposit',
  createTime: '2026-10-01T09:00:00Z',
  content: '<p>Deposits credit after the Coin\u2019s confirmation count.</p><script>alert(1)</script>',
  cate: '0',
}

const OVERVIEW = [
  {
    cate: '0',
    title: 'Getting started',
    items: [
      { id: 'h-1', title: 'How to deposit', createTime: '2026-10-01T09:00:00Z' },
      { id: 'h-2', title: 'How to withdraw', createTime: '2026-10-02T09:00:00Z' },
    ],
  },
  { cate: '1', title: 'Fees', items: [] },
]

describe('help article page', () => {
  beforeEach(() => {
    contentApi.helpCategories.mockReset()
    contentApi.helpArticle.mockReset()
  })

  it('renders the article sanitized with the sibling sidebar and breadcrumb', async () => {
    contentApi.helpArticle.mockResolvedValue(ARTICLE)
    contentApi.helpCategories.mockResolvedValue(OVERVIEW)

    const wrapper = mountPage(HelpDetail, { id: 'h-1' })
    await flushPromises()

    expect(contentApi.helpArticle).toHaveBeenCalledWith('h-1')
    expect(wrapper.text()).toContain('How to deposit')
    expect(wrapper.text()).toContain('Oct 1, 2026')
    // The body rendered through the sanitizer: paragraph kept, script dead.
    expect(wrapper.find('.content p').text()).toContain('confirmation count')
    expect(wrapper.find('.content script').exists()).toBe(false)
    // The sidebar lists the sibling articles of the same category.
    expect(wrapper.find('a[href="/helpdetail/h-2"]').text()).toContain('How to withdraw')
    // The breadcrumb names the owning category.
    expect(wrapper.find('.route-wrap a[href="/helplist/0"]').text()).toContain(
      'Getting started',
    )
  })

  it('renders a complete-sentence not-found state when the article is gone', async () => {
    contentApi.helpArticle.mockRejectedValue(new Error('help article not found'))
    contentApi.helpCategories.mockResolvedValue([])

    const wrapper = mountPage(HelpDetail, { id: 'h-404' })
    await flushPromises()

    expect(wrapper.text()).toContain(
      'This help article does not exist or is no longer available.',
    )
  })
})
