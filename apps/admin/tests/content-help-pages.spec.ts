// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import HelpCategories from '../src/pages/content/help-categories.vue'
import HelpArticles from '../src/pages/content/help-articles.vue'

// The help center pages (issue #63): categories (create, rename,
// delete-when-empty) and articles (category filter, one dialog for
// create/edit with the publish switch, in-place publish toggle,
// delete). The articles page's create button stays disabled until a
// category exists — the api refuses unknown categories.

const collaborators = vi.hoisted(() => ({ apiFetch: vi.fn() }))

const categories = [
  { id: 'c-1', title: 'Getting started', articles: 2, created_at: '2026-10-01T09:00:00Z', updated_at: '2026-10-01T09:00:00Z' },
  { id: 'c-2', title: 'Fees', articles: 0, created_at: '2026-10-02T09:00:00Z', updated_at: '2026-10-02T09:00:00Z' },
]

const articles = [
  {
    id: 'h-1',
    category_id: 'c-1',
    category: 'Getting started',
    title: 'How to deposit',
    content: '<p>Deposits credit after confirmations.</p>',
    published: true,
    created_at: '2026-10-03T09:00:00Z',
    updated_at: '2026-10-03T09:00:00Z',
  },
]

const DialogStub = {
  props: ['modelValue', 'title'],
  template: '<div v-if="modelValue" class="dialog-stub"><h2>{{ title }}</h2><slot /><slot name="footer" /></div>',
}

const PopconfirmStub = {
  template:
    '<div><slot name="reference" /><button data-test="popconfirm-confirm" @click="$emit(\'confirm\')">Confirm</button></div>',
}

// el-select under jsdom: a plain select bound to update:modelValue and
// change, with el-option as real options.
const SelectStub = {
  props: ['modelValue'],
  template: '<select @change="$emit(\'update:modelValue\', $event.target.value); $emit(\'change\', $event.target.value)"><slot /></select>',
}

const OptionStub = {
  props: ['label', 'value'],
  template: '<option :value="value">{{ label }}</option>',
}

function stubs() {
  return {
    'el-dialog': DialogStub,
    'el-popconfirm': PopconfirmStub,
    'el-select': SelectStub,
    'el-option': OptionStub,
  }
}

async function mountContent(component: typeof HelpCategories | typeof HelpArticles): Promise<VueWrapper> {
  const wrapper = mountPage(component, { global: { stubs: stubs() } })
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

describe('help categories page', () => {
  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    stubNuxtGlobals(Vue, { useApi: () => ({ apiFetch: collaborators.apiFetch }) })
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    )
  })

  it('lists categories with their article counts', async () => {
    collaborators.apiFetch.mockResolvedValue(categories)
    const wrapper = await mountContent(HelpCategories)

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/help/categories')
    const text = wrapper.find('[data-test="category-rows"]').text()
    expect(text).toContain('Getting started')
    expect(text).toContain('Fees')
    expect(wrapper.findAll('[data-test="category-rows"] .el-tag').map((t) => t.text())).toEqual(['2', '0'])
  })

  it('creates and renames categories', async () => {
    collaborators.apiFetch.mockResolvedValue(categories)
    const wrapper = await mountContent(HelpCategories)

    await button(wrapper, 'New category').trigger('click')
    await settle()
    await wrapper.find('input[name="title"]').setValue('Trading')
    await button(wrapper, 'Create category').trigger('click')
    await flushPromises()
    await settle()
    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/help/categories', {
      method: 'POST',
      body: { title: 'Trading' },
    })

    await button(wrapper, 'Edit').trigger('click')
    await settle()
    expect((wrapper.find('input[name="title"]').element as HTMLInputElement).value).toBe('Getting started')
    await wrapper.find('input[name="title"]').setValue('First steps')
    await button(wrapper, 'Save changes').trigger('click')
    await flushPromises()
    await settle()
    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/help/categories/c-1', {
      method: 'PUT',
      body: { title: 'First steps' },
    })
  })

  it('deletes a category after the confirm and surfaces a refusal', async () => {
    collaborators.apiFetch
      .mockResolvedValueOnce(categories)
      .mockRejectedValueOnce(new Error('The category still has articles. Delete or move them first.'))
      .mockResolvedValue(categories)
    const wrapper = await mountContent(HelpCategories)

    await wrapper.findAll('[data-test="popconfirm-confirm"]')[0].trigger('click')
    await flushPromises()
    await settle()
    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/help/categories/c-1', { method: 'DELETE' })
    expect(wrapper.find('[role="alert"]').text()).toContain('still has articles')
  })
})

describe('help articles page', () => {
  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    stubNuxtGlobals(Vue, { useApi: () => ({ apiFetch: collaborators.apiFetch }) })
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    )
  })

  it('lists articles and loads the category filter', async () => {
    collaborators.apiFetch.mockImplementation((path: string) => {
      if (path.startsWith('/api/help/articles')) {
        return Promise.resolve({ content: articles, total: 1 })
      }
      return Promise.resolve(categories)
    })
    const wrapper = await mountContent(HelpArticles)

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/help/articles?pageNo=1&pageSize=10')
    const text = wrapper.find('[data-test="article-rows"]').text()
    expect(text).toContain('How to deposit')
    expect(text).toContain('Getting started')
    expect(text).toContain('Published')
  })

  it('filters by category', async () => {
    collaborators.apiFetch.mockImplementation((path: string) => {
      if (path.startsWith('/api/help/articles')) {
        return Promise.resolve({ content: articles, total: 1 })
      }
      return Promise.resolve(categories)
    })
    const wrapper = await mountContent(HelpArticles)

    await wrapper.find('select[data-test="category-filter"]').setValue('c-1')
    await flushPromises()
    await settle()
    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/help/articles?pageNo=1&pageSize=10&cate=c-1')
  })

  it('creates an article in a chosen category', async () => {
    collaborators.apiFetch.mockImplementation((path: string) => {
      if (path.startsWith('/api/help/articles')) {
        return Promise.resolve({ content: articles, total: 1 })
      }
      return Promise.resolve(categories)
    })
    const wrapper = await mountContent(HelpArticles)

    await button(wrapper, 'New article').trigger('click')
    await settle()
    await wrapper.find('select[name="category"]').setValue('c-2')
    await wrapper.find('input[name="title"]').setValue('How to withdraw')
    await wrapper.find('textarea[name="content"]').setValue('<p>Step by step.</p>')
    await wrapper.find('input[name="published"]').setValue(true)
    await button(wrapper, 'Create article').trigger('click')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/help/articles', {
      method: 'POST',
      body: { category_id: 'c-2', title: 'How to withdraw', content: '<p>Step by step.</p>', published: true },
    })
  })

  it('publishes a draft in place with the full body', async () => {
    collaborators.apiFetch.mockImplementation((path: string) => {
      if (path.startsWith('/api/help/articles')) {
        return Promise.resolve({ content: articles, total: 1 })
      }
      return Promise.resolve(categories)
    })
    const wrapper = await mountContent(HelpArticles)

    await button(wrapper, 'Unpublish').trigger('click')
    await flushPromises()
    await settle()
    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/help/articles/h-1', {
      method: 'PUT',
      body: {
        category_id: 'c-1',
        title: 'How to deposit',
        content: '<p>Deposits credit after confirmations.</p>',
        published: false,
      },
    })
  })

  it('keeps the create button disabled while no category exists', async () => {
    collaborators.apiFetch.mockImplementation((path: string) => {
      if (path.startsWith('/api/help/articles')) {
        return Promise.resolve({ content: [], total: 0 })
      }
      return Promise.resolve([])
    })
    const wrapper = await mountContent(HelpArticles)

    expect(button(wrapper, 'New article').attributes('disabled')).toBeDefined()
  })
})
