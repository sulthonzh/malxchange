// @vitest-environment jsdom
import { flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { mountPage } from './page-mount'
import Help from '../src/pages/help.vue'

const contentApi = vi.hoisted(() => ({
  helpCategories: vi.fn(),
}))

vi.mock('~/composables/useContent', () => ({ useContent: () => contentApi }))

// The help center landing (ticket #50): the legacy Help.vue card grid —
// one section per category with its articles and a link to the full
// category listing. Empty categories render their own sentence; an
// absent admin-api answers [] (content contract), which renders the
// page-level empty state, never an error.

describe('help center page', () => {
  beforeEach(() => {
    contentApi.helpCategories.mockReset()
  })

  it('renders a card per category with article links and the category listing link', async () => {
    contentApi.helpCategories.mockResolvedValue([
      {
        cate: '0',
        title: 'Getting started',
        items: [
          { id: 'h-1', title: 'How to deposit', createTime: '2026-10-01T09:00:00Z' },
        ],
      },
      { cate: '1', title: 'Fees', items: [] },
    ])

    const wrapper = mountPage(Help)
    await flushPromises()

    expect(contentApi.helpCategories).toHaveBeenCalled()
    expect(wrapper.text()).toContain('Getting started')
    expect(wrapper.find('a[href="/helpdetail/h-1"]').text()).toContain('How to deposit')
    expect(wrapper.find('a[href="/helplist/0"]').text()).toContain('View all articles')
    expect(wrapper.text()).toContain('Fees')
    expect(wrapper.text()).toContain('There are no articles in this category yet.')
  })

  it('renders a complete-sentence empty state when no categories exist', async () => {
    contentApi.helpCategories.mockResolvedValue([])

    const wrapper = mountPage(Help)
    await flushPromises()

    expect(wrapper.text()).toContain('The help center has no articles yet.')
  })

  it('renders the empty state, never an error, when the api cannot be reached', async () => {
    contentApi.helpCategories.mockRejectedValue(new Error('The api could not be reached.'))

    const wrapper = mountPage(Help)
    await flushPromises()

    expect(wrapper.text()).toContain('The help center has no articles yet.')
  })
})
