// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import MembersPage from '../src/pages/members/index.vue'
import { ApiError } from '../src/composables/useApi'

// The member search screen (issue #58): api's member list through
// admin-api — email/id search, status filter, paged rows, and a click
// through to the detail page.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
  navigateTo: vi.fn(),
}))

const rows = [
  {
    id: 'm-1',
    email: 'alice@example.com',
    status: 'active',
    kycStatus: 'verified',
    createdAt: '2026-01-01T00:00:00Z',
  },
  {
    id: 'm-2',
    email: 'bob@example.com',
    status: 'locked',
    kycStatus: 'pending',
    createdAt: '2026-01-02T00:00:00Z',
  },
]

// jsdom has no ResizeObserver; element-plus tables observe their width.
class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

describe('members page', () => {
  let wrapper: VueWrapper

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    collaborators.navigateTo.mockReset()
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch, apiBaseUrl: 'http://admin-api.test' }),
      navigateTo: collaborators.navigateTo,
    })
    vi.stubGlobal('ResizeObserver', ResizeObserverStub)
  })

  it('loads the first page on entry and renders the member rows', async () => {
    collaborators.apiFetch.mockResolvedValue({ total: 2, members: rows })
    wrapper = mountPage(MembersPage)
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/members?pageNo=1&pageSize=20')
    expect(wrapper.find('[data-test="member-table"]').text()).toContain('alice@example.com')
    expect(wrapper.find('[data-test="member-table"]').text()).toContain('bob@example.com')
  })

  it('re-searches with the query from the first page', async () => {
    collaborators.apiFetch.mockResolvedValue({ total: 0, members: [] })
    wrapper = mountPage(MembersPage)
    await flushPromises()

    collaborators.apiFetch.mockClear()
    collaborators.apiFetch.mockResolvedValue({ total: 1, members: [rows[0]!] })
    await wrapper.find('[data-test="member-search"]').setValue('ali')
    await wrapper.find('[data-test="member-search-go"]').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenLastCalledWith('/api/members?pageNo=1&pageSize=20&q=ali')
  })

  it('opens the detail page when a row is clicked', async () => {
    collaborators.apiFetch.mockResolvedValue({ total: 2, members: rows })
    wrapper = mountPage(MembersPage)
    await flushPromises()
    await settle()

    await wrapper.find('[data-test="member-table"] .el-table__row').trigger('click')
    expect(collaborators.navigateTo).toHaveBeenCalledWith('/members/m-1')
  })

  it('renders the api sentence when the list cannot be loaded', async () => {
    collaborators.apiFetch.mockRejectedValue(
      new ApiError('The member service is unreachable. Try again.', 503),
    )
    wrapper = mountPage(MembersPage)
    await flushPromises()
    await settle()

    expect(wrapper.find('[data-test="member-error"]').text()).toContain(
      'The member service is unreachable. Try again.',
    )
  })
})
