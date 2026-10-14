<template>
  <section class="audit-log">
    <div class="page-head">
      <h1>Audit log</h1>
      <p class="hint">
        One row per mutating admin request and sign-in event, written by the server's middleware —
        never by handlers. Actions are verb:resource strings; before/after carry the redacted row
        JSON (credentials never enter the log).
      </p>
    </div>

    <el-form class="filters" inline>
      <el-form-item label="Actor">
        <el-input
          name="actor"
          v-model="filters.actor"
          placeholder="employee@exchange.example.com"
          clearable
        />
      </el-form-item>
      <el-form-item label="Action">
        <el-input name="action" v-model="filters.action" placeholder="verb:resource" clearable />
      </el-form-item>
      <el-form-item label="Target">
        <el-input name="target" v-model="filters.target" placeholder="Affected id" clearable />
      </el-form-item>
      <el-form-item label="Time range">
        <el-date-picker
          v-model="filters.range"
          type="datetimerange"
          start-placeholder="From"
          end-placeholder="To"
        />
      </el-form-item>
      <el-form-item>
        <el-button type="warning" class="search" @click="search">Search</el-button>
      </el-form-item>
    </el-form>

    <el-alert
      v-if="actionError"
      class="action-error"
      type="error"
      :title="actionError"
      data-testid="action-error"
    />

    <el-table v-loading="loading" :data="rows" class="audit-table">
      <el-table-column label="Time" min-width="150">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column prop="actor" label="Actor" min-width="200" />
      <el-table-column prop="action" label="Action" min-width="150" />
      <el-table-column prop="target" label="Target" min-width="120" />
      <el-table-column prop="origin_ip" label="Origin IP" min-width="130" />
      <el-table-column label="Before → After" min-width="380">
        <template #default="{ row }">
          <pre class="before-json json">{{ renderJson(row.before) }}</pre>
          <pre class="after-json json">{{ renderJson(row.after) }}</pre>
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      class="pager"
      layout="prev, pager, next, total"
      :total="total"
      :page-size="pageSize"
      :current-page="page"
      @current-change="loadPage"
    />
  </section>
</template>

<script setup lang="ts">
import type { AuditLogRow } from '~/utils/apiTypes'
import { auditQuery, type AuditFilters } from '~/utils/auditQuery'
import { formatDateTime } from '~/utils/format'

// Audit log search (issue #57) over the #57 search endpoint: the four
// filters compose one query; rows render the stored envelope verbatim.
// An api refusal (a malformed bound, a lost permission) renders in the
// page alert — an empty table never silently masks a failure.
definePageMeta({ middleware: 'employee' })

useHead({ title: 'Audit log' })

const { apiFetch } = useApi()

const filters = reactive<AuditFilters>({ actor: '', action: '', target: '', range: null })
const rows = ref<AuditLogRow[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)
const loading = ref(false)
const actionError = ref('')

// Renders one stored JSON half for the table: an unset half (SQL NULL)
// is an em dash, present JSON is its compact wire form.
function renderJson(value: unknown): string {
  return value === null || value === undefined ? '—' : JSON.stringify(value)
}

async function load(): Promise<void> {
  loading.value = true
  actionError.value = ''
  try {
    const query = auditQuery(filters)
    const suffix =
      page.value > 1 ? (query === '' ? `?page=${page.value}` : `${query}&page=${page.value}`) : query
    const answer = await apiFetch<{
      rows: AuditLogRow[]
      total: number
      page: number
      page_size: number
    }>(`/api/audit-log${suffix}`)
    rows.value = answer.rows
    total.value = answer.total
    page.value = answer.page
    pageSize.value = answer.page_size
  } catch (error) {
    actionError.value =
      error instanceof Error ? error.message : 'The audit log could not be loaded. Try again.'
  } finally {
    loading.value = false
  }
}

function search(): void {
  page.value = 1
  void load()
}

function loadPage(next: number): void {
  page.value = next
  void load()
}

onMounted(() => {
  void load()
})
</script>

<style scoped lang="scss">
.page-head {
  margin-bottom: 16px;

  h1 {
    font-size: 20px;
    font-weight: 600;
    margin: 0 0 4px;
  }
}

.hint {
  color: var(--text-secondary);
  font-size: 13px;
  margin: 0;
  max-width: 720px;
}

.filters {
  margin-bottom: 12px;
}

.action-error {
  margin-bottom: 12px;
}

.json {
  font-family: var(--mono-font, monospace);
  font-size: 12px;
  margin: 2px 0;
  white-space: pre-wrap;
  word-break: break-all;
}

.pager {
  margin-top: 12px;
}
</style>
