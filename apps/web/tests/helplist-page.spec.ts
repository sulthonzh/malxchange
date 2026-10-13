// @vitest-environment jsdom
import { flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { mountPage } from './page-mount'
import HelpList from '../src/pages/helplist/[cate].vue'

const contentApi = vi.hoisted(() => ({
  helpCategories: vi.fn(),
  helpCategory: vi.fn(),
}))

vi.mock('~/composables/useContent', () => ({ useContent: () => contentApi }))

// The category article listing (ticket #50): the legacy HelpList.vue —
// a "Help center" breadcrumb into the category title, dated rows, and
// right-aligned pagination passed straight through the api contract.
// The category title comes from the help overview (the listing endpoint
// carries no title); an unknown or empty category renders a sentence,
// never an error.

describe('help category list page', () => {
  beforeEach(() => {
    contentApi.helpCategories.mockReset()
    contentApi.helpCategory.mockReset()
  })

  function mountList(cate = '0') {
    contentApi.helpCategories.mockResolvedValue([
      { cate: '0', title: 'Getting started', items: [] },
      { cate: '1', title: 'Fees', items: [] },
    ])
    return mountPage(HelpList, { cate })
  }

  it('renders the category title with dated rows and passes pagination through', async () => {
    contentApi.helpCategory
      .mockResolvedValueOnce({
        content: [{ id: 'h-1', title: 'How to deposit', createTime: '2026-10-01T09:00:00Z' }],
        totalElements: 12,
      })
      .mockResolvedValueOnce({
        content: [{ id: 'h-2', title: 'How to withdraw', createTime: '2026-10-02T09:00:00Z' }],
        totalElements: 12,
      })

    const wrapper = mountList()
    await flushPromises()

    expect(contentApi.helpCategory).toHaveBeenCalledWith('0', 1, 10)
    expect(wrapper.text()).toContain('Getting started')
    expect(wrapper.text()).toContain('How to deposit')
    expect(wrapper.text()).toContain('Oct 1, 2026')
    expect(wrapper.find('a[href="/helpdetail/h-1"]').exists()).toBe(true)

    wrapper.findComponent({ name: 'ElPagination' }).vm.$emit('current-change', 2)
    await flushPromises()

    expect(contentApi.helpCategory).toHaveBeenCalledWith('0', 2, 10)
    expect(wrapper.text()).toContain('How to withdraw')
  })

  it('renders a complete-sentence empty state when the category has no articles', async () => {
    contentApi.helpCategory.mockResolvedValue({ content: [], totalElements: 0 })

    const wrapper = mountList('1')
    await flushPromises()

    expect(contentApi.helpCategory).toHaveBeenCalledWith('1', 1, 10)
    expect(wrapper.text()).toContain('There are no articles in this category yet.')
  })

  it('renders the empty state, never an error, when the api cannot be reached', async () => {
    contentApi.helpCategory.mockRejectedValue(new Error('The api could not be reached.'))

    const wrapper = mountList('0')
    await flushPromises()

    expect(wrapper.text()).toContain('There are no articles in this category yet.')
  })
})
