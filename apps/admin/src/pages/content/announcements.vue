<template>
  <section class="announcements">
    <header class="page-head">
      <div>
        <h1>Announcements</h1>
        <p class="hint">Published announcements are the member surface's news feed, newest first.</p>
      </div>
      <el-button type="primary" @click="openCreate">New announcement</el-button>
    </header>

    <p v-if="loadError" class="error" role="alert">{{ loadError }}</p>

    <el-table v-else :data="rows" v-loading="loading" data-test="announcement-rows">
      <el-table-column prop="title" label="Title" min-width="220" />
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
      <el-table-column label="Updated" width="180">
        <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
      </el-table-column>
      <el-table-column label="Actions" width="240" align="right">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">Edit</el-button>
          <el-button size="small" :type="row.published ? 'warning' : 'success'" @click="togglePublish(row)">
            {{ row.published ? 'Unpublish' : 'Publish' }}
          </el-button>
          <el-popconfirm
            title="Delete this announcement for good?"
            confirm-button-text="Delete"
            cancel-button-text="Keep"
            @confirm="remove(row)"
          >
            <template #reference>
              <el-button size="small" type="danger" :data-test="`delete-${row.id}`">Delete</el-button>
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

    <el-dialog v-model="dialogOpen" :title="editing ? 'Edit announcement' : 'New announcement'" width="640">
      <el-form :model="form" :rules="rules" ref="formRef" label-position="top">
        <el-form-item label="Title" prop="title">
          <el-input v-model="form.title" name="title" maxlength="200" placeholder="Maintenance window on Sunday" />
        </el-form-item>
        <el-form-item label="Body (HTML)" prop="content">
          <el-input
            v-model="form.content"
            name="content"
            type="textarea"
            :rows="10"
            placeholder="<p>What members need to know.</p>"
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
          {{ editing ? 'Save changes' : 'Create announcement' }}
        </el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import type { FormInstance, FormRules } from 'element-plus'

import { requiredRule } from '~/utils/rules'

// Announcements management (issue #63): support's news feed — create,
// edit, publish/unpublish, delete. Publishing is the edit form's
// switch (the api models it as an edit of the published flag); the
// quick toggle flips it in place. The server's permission middleware
// is the gate; this page only renders what the session may do.
definePageMeta({ middleware: 'employee' })

interface AnnouncementRow {
  id: string
  title: string
  content: string
  published: boolean
  created_at: string
  updated_at: string
}

const { apiFetch } = useApi()

const rows = ref<AnnouncementRow[]>([])
const total = ref(0)
const pageNo = ref(1)
const pageSize = 10
const loading = ref(false)
const loadError = ref('')

const dialogOpen = ref(false)
const editing = ref<AnnouncementRow | null>(null)
const submitting = ref(false)
const formError = ref('')
const formRef = ref<FormInstance>()
const form = reactive({ title: '', content: '', published: false })

const rules: FormRules = {
  title: [
    requiredRule('Enter a title.'),
    { max: 200, message: 'Keep the title within 200 characters.', trigger: 'blur' },
  ],
  content: [requiredRule('Write the announcement body (HTML).')],
}

useHead({ title: 'Announcements' })

onMounted(load)

async function load(target = pageNo.value): Promise<void> {
  pageNo.value = target
  loading.value = true
  loadError.value = ''
  try {
    const page = await apiFetch<{ content: AnnouncementRow[]; total: number }>(
      `/api/announcements?pageNo=${pageNo.value}&pageSize=${pageSize}`,
    )
    rows.value = page.content
    total.value = page.total
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The announcements could not be loaded. Try again.'
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editing.value = null
  form.title = ''
  form.content = ''
  form.published = false
  formError.value = ''
  dialogOpen.value = true
}

function openEdit(row: AnnouncementRow): void {
  editing.value = row
  form.title = row.title
  form.content = row.content
  form.published = row.published
  formError.value = ''
  dialogOpen.value = true
}

async function submit(): Promise<void> {
  formError.value = ''
  // The deterministic refusal: the same checks the api answers 400
  // with, enforced here so a malformed save never leaves the dialog.
  if (form.title.trim() === '' || form.content.trim() === '') {
    formError.value = 'Enter a title and a body before saving.'
    return
  }
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) {
    return
  }
  submitting.value = true
  try {
    const body = { title: form.title, content: form.content, published: form.published }
    if (editing.value) {
      await apiFetch(`/api/announcements/${editing.value.id}`, { method: 'PUT', body })
    } else {
      await apiFetch('/api/announcements', { method: 'POST', body })
    }
    dialogOpen.value = false
    await load()
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The announcement could not be saved. Try again.'
  } finally {
    submitting.value = false
  }
}

// togglePublish flips the publish flag in place — the same PUT the
// edit dialog submits, without opening it.
async function togglePublish(row: AnnouncementRow): Promise<void> {
  loadError.value = ''
  try {
    await apiFetch(`/api/announcements/${row.id}`, {
      method: 'PUT',
      body: { title: row.title, content: row.content, published: !row.published },
    })
    await load()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The announcement could not be saved. Try again.'
  }
}

async function remove(row: AnnouncementRow): Promise<void> {
  loadError.value = ''
  try {
    await apiFetch(`/api/announcements/${row.id}`, { method: 'DELETE' })
    await load()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The announcement could not be deleted. Try again.'
  }
}

function formatTime(value: string): string {
  return new Date(value).toISOString().replace('T', ' ').slice(0, 16) + ' UTC'
}
</script>

<style scoped lang="scss">
.announcements {
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
