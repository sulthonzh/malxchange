<template>
  <section class="help-categories">
    <header class="page-head">
      <div>
        <h1>Help categories</h1>
        <p class="hint">
          The help center's sections. Every category appears on the member overview; a category can be
          deleted only once it holds no articles.
        </p>
      </div>
      <el-button type="primary" @click="openCreate">New category</el-button>
    </header>

    <p v-if="loadError" class="error" role="alert">{{ loadError }}</p>

    <el-table v-else :data="rows" v-loading="loading" data-test="category-rows">
      <el-table-column prop="title" label="Title" min-width="240" />
      <el-table-column label="Articles" width="120">
        <template #default="{ row }">
          <el-tag size="small" type="info">{{ row.articles }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Created" width="180">
        <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="Actions" width="180" align="right">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">Edit</el-button>
          <el-popconfirm
            title="Delete this empty category?"
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

    <el-dialog v-model="dialogOpen" :title="editing ? 'Edit category' : 'New category'" width="480">
      <el-form :model="form" :rules="rules" ref="formRef" label-position="top">
        <el-form-item label="Title" prop="title">
          <el-input v-model="form.title" name="title" maxlength="200" placeholder="Getting started" />
        </el-form-item>
      </el-form>
      <p v-if="formError" class="error" role="alert">{{ formError }}</p>
      <template #footer>
        <el-button @click="dialogOpen = false">Cancel</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">
          {{ editing ? 'Save changes' : 'Create category' }}
        </el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import type { FormInstance, FormRules } from 'element-plus'

import { requiredRule } from '~/utils/rules'

// Help categories management (issue #63): the help center's sections,
// created, renamed, and deleted (when empty). Articles live on their
// own page; the member overview lists every category with its
// published articles.
definePageMeta({ middleware: 'employee' })

interface CategoryRow {
  id: string
  title: string
  articles: number
  created_at: string
  updated_at: string
}

const { apiFetch } = useApi()

const rows = ref<CategoryRow[]>([])
const loading = ref(false)
const loadError = ref('')

const dialogOpen = ref(false)
const editing = ref<CategoryRow | null>(null)
const submitting = ref(false)
const formError = ref('')
const formRef = ref<FormInstance>()
const form = reactive({ title: '' })

const rules: FormRules = {
  title: [
    requiredRule('Enter a title.'),
    { max: 200, message: 'Keep the title within 200 characters.', trigger: 'blur' },
  ],
}

useHead({ title: 'Help categories' })

onMounted(load)

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    rows.value = await apiFetch<CategoryRow[]>('/api/help/categories')
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The categories could not be loaded. Try again.'
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editing.value = null
  form.title = ''
  formError.value = ''
  dialogOpen.value = true
}

function openEdit(row: CategoryRow): void {
  editing.value = row
  form.title = row.title
  formError.value = ''
  dialogOpen.value = true
}

async function submit(): Promise<void> {
  formError.value = ''
  // The deterministic refusal — the api answers 400 for the same input.
  if (form.title.trim() === '') {
    formError.value = 'Enter a title before saving.'
    return
  }
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) {
    return
  }
  submitting.value = true
  try {
    if (editing.value) {
      await apiFetch(`/api/help/categories/${editing.value.id}`, {
        method: 'PUT',
        body: { title: form.title },
      })
    } else {
      await apiFetch('/api/help/categories', { method: 'POST', body: { title: form.title } })
    }
    dialogOpen.value = false
    await load()
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The category could not be saved. Try again.'
  } finally {
    submitting.value = false
  }
}

async function remove(row: CategoryRow): Promise<void> {
  loadError.value = ''
  try {
    await apiFetch(`/api/help/categories/${row.id}`, { method: 'DELETE' })
    await load()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The category could not be deleted. Try again.'
  }
}

function formatTime(value: string): string {
  return new Date(value).toISOString().replace('T', ' ').slice(0, 16) + ' UTC'
}
</script>

<style scoped lang="scss">
.help-categories {
  .page-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 16px;

    h1 {
      font-size: 20px;
      font-weight: 600;
      margin: 0 0 4px;
    }

    .hint {
      color: var(--text-secondary);
      font-size: 13px;
      margin: 0;
      max-width: 560px;
    }
  }

  .error {
    color: var(--el-color-danger);
    font-size: 13px;
    margin: 0 0 12px;
  }
}
</style>
