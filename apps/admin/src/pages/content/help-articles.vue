<template>
  <section class="help-articles">
    <header class="page-head">
      <div>
        <h1>Help articles</h1>
        <p class="hint">The help center's documents, filtered by category. Members read published articles only.</p>
      </div>
      <el-button type="primary" :disabled="!categories.length" @click="openCreate">New article</el-button>
    </header>

    <div class="filters">
      <el-select
        v-model="cate"
        placeholder="All categories"
        clearable
        data-test="category-filter"
        @change="() => load(1)"
      >
        <el-option v-for="category in categories" :key="category.id" :label="category.title" :value="category.id" />
      </el-select>
    </div>

    <p v-if="loadError" class="error" role="alert">{{ loadError }}</p>

    <el-table v-else :data="rows" v-loading="loading" data-test="article-rows">
      <el-table-column prop="title" label="Title" min-width="220" />
      <el-table-column prop="category" label="Category" width="160" />
      <el-table-column label="Status" width="120">
        <template #default="{ row }">
          <el-tag :type="row.published ? 'success' : 'info'" size="small">
            {{ row.published ? 'Published' : 'Draft' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Created" width="180">
        <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="Actions" width="240" align="right">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">Edit</el-button>
          <el-button size="small" :type="row.published ? 'warning' : 'success'" @click="togglePublish(row)">
            {{ row.published ? 'Unpublish' : 'Publish' }}
          </el-button>
          <el-popconfirm
            title="Delete this article for good?"
            confirm-button-text="Delete"
            cancel-button-text="Keep"
            @confirm="remove(row)"
          >
            <template #reference>
              <el-button size="small" type="danger">Delete</el-button>
            </template>
          </el-popconfirm>
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      v-if="total > pageSize"
      class="pager"
      layout="prev, pager, next"
      :total="total"
      :page-size="pageSize"
      :current-page="pageNo"
      @current-change="load"
    />

    <el-dialog v-model="dialogOpen" :title="editing ? 'Edit article' : 'New article'" width="640">
      <el-form :model="form" :rules="rules" ref="formRef" label-position="top">
        <el-form-item label="Category" prop="category_id">
          <el-select v-model="form.category_id" name="category" placeholder="Choose a category">
            <el-option v-for="category in categories" :key="category.id" :label="category.title" :value="category.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="Title" prop="title">
          <el-input v-model="form.title" name="title" maxlength="200" placeholder="How to deposit" />
        </el-form-item>
        <el-form-item label="Body (HTML)" prop="content">
          <el-input
            v-model="form.content"
            name="content"
            type="textarea"
            :rows="12"
            placeholder="<p>Every step the member needs, in reading order.</p>"
          />
        </el-form-item>
        <el-form-item label="Published">
          <el-switch v-model="form.published" name="published" />
          <span class="switch-hint">Drafts are invisible to members until you publish.</span>
        </el-form-item>
      </el-form>
      <p v-if="formError" class="error" role="alert">{{ formError }}</p>
      <template #footer>
        <el-button @click="dialogOpen = false">Cancel</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">
          {{ editing ? 'Save changes' : 'Create article' }}
        </el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import type { FormInstance, FormRules } from 'element-plus'

import { requiredRule } from '~/utils/rules'

// Help articles management (issue #63): the help center's documents —
// create, edit, move between categories, publish/unpublish, delete.
// Requires categories to exist first (the api refuses an unknown
// category, and the create button stays disabled until one loads).
definePageMeta({ middleware: 'employee' })

interface CategoryOption {
  id: string
  title: string
}

interface ArticleRow {
  id: string
  category_id: string
  category: string
  title: string
  content: string
  published: boolean
  created_at: string
  updated_at: string
}

const { apiFetch } = useApi()

const categories = ref<CategoryOption[]>([])
const cate = ref('')
const rows = ref<ArticleRow[]>([])
const total = ref(0)
const pageNo = ref(1)
const pageSize = 10
const loading = ref(false)
const loadError = ref('')

const dialogOpen = ref(false)
const editing = ref<ArticleRow | null>(null)
const submitting = ref(false)
const formError = ref('')
const formRef = ref<FormInstance>()
const form = reactive({ category_id: '', title: '', content: '', published: false })

const rules: FormRules = {
  category_id: [requiredRule('Choose a category.')],
  title: [
    requiredRule('Enter a title.'),
    { max: 200, message: 'Keep the title within 200 characters.', trigger: 'blur' },
  ],
  content: [requiredRule('Write the article body (HTML).')],
}

useHead({ title: 'Help articles' })

onMounted(() => {
  loadCategories()
  load(pageNo.value)
})

async function loadCategories(): Promise<void> {
  try {
    categories.value = await apiFetch<CategoryOption[]>('/api/help/categories')
  } catch {
    // The table's own load surfaces the error; the filter just stays
    // empty until a reload.
  }
}

async function load(target = pageNo.value): Promise<void> {
  pageNo.value = target
  loading.value = true
  loadError.value = ''
  try {
    const filter = cate.value ? `&cate=${encodeURIComponent(cate.value)}` : ''
    const page = await apiFetch<{ content: ArticleRow[]; total: number }>(
      `/api/help/articles?pageNo=${pageNo.value}&pageSize=${pageSize}${filter}`,
    )
    rows.value = page.content
    total.value = page.total
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The articles could not be loaded. Try again.'
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editing.value = null
  form.category_id = cate.value
  form.title = ''
  form.content = ''
  form.published = false
  formError.value = ''
  dialogOpen.value = true
}

function openEdit(row: ArticleRow): void {
  editing.value = row
  form.category_id = row.category_id
  form.title = row.title
  form.content = row.content
  form.published = row.published
  formError.value = ''
  dialogOpen.value = true
}

async function submit(): Promise<void> {
  formError.value = ''
  // The deterministic refusal — the api answers 400 for the same input.
  if (form.category_id === '' || form.title.trim() === '' || form.content.trim() === '') {
    formError.value = 'Choose a category and enter a title and a body before saving.'
    return
  }
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) {
    return
  }
  submitting.value = true
  try {
    const body = {
      category_id: form.category_id,
      title: form.title,
      content: form.content,
      published: form.published,
    }
    if (editing.value) {
      await apiFetch(`/api/help/articles/${editing.value.id}`, { method: 'PUT', body })
    } else {
      await apiFetch('/api/help/articles', { method: 'POST', body })
    }
    dialogOpen.value = false
    await Promise.all([load(), loadCategories()])
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The article could not be saved. Try again.'
  } finally {
    submitting.value = false
  }
}

// togglePublish flips the publish flag in place — the same PUT the
// edit dialog submits, without opening it.
async function togglePublish(row: ArticleRow): Promise<void> {
  loadError.value = ''
  try {
    await apiFetch(`/api/help/articles/${row.id}`, {
      method: 'PUT',
      body: {
        category_id: row.category_id,
        title: row.title,
        content: row.content,
        published: !row.published,
      },
    })
    await load()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The article could not be saved. Try again.'
  }
}

async function remove(row: ArticleRow): Promise<void> {
  loadError.value = ''
  try {
    await apiFetch(`/api/help/articles/${row.id}`, { method: 'DELETE' })
    await Promise.all([load(), loadCategories()])
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The article could not be deleted. Try again.'
  }
}

function formatTime(value: string): string {
  return new Date(value).toISOString().replace('T', ' ').slice(0, 16) + ' UTC'
}
</script>

<style scoped lang="scss">
.help-articles {
  .page-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 12px;

    h1 {
      font-size: 20px;
      font-weight: 600;
      margin: 0 0 4px;
    }

    .hint {
      color: var(--text-secondary);
      font-size: 13px;
      margin: 0;
    }
  }

  .filters {
    margin-bottom: 12px;

    .el-select {
      width: 240px;
    }
  }

  .error {
    color: var(--el-color-danger);
    font-size: 13px;
    margin: 0 0 12px;
  }

  .pager {
    margin-top: 12px;
    justify-content: flex-end;
  }

  .switch-hint {
    color: var(--text-muted);
    font-size: 12px;
    margin-left: 8px;
  }
}
</style>
