<template>
  <section class="members">
    <h1>Members</h1>
    <p class="hint">
      Search by email substring or exact member id. Opening a row shows the member's identity,
      status, KYC state, and channel bindings; locking a member revokes their live sessions.
    </p>

    <el-form inline class="filters" @submit.prevent>
      <el-form-item>
        <el-input
          v-model="query"
          data-test="member-search"
          placeholder="Email or member id"
          clearable
          @keyup.enter="search"
          @clear="search"
        />
      </el-form-item>
      <el-form-item>
        <el-select v-model="status" data-test="member-status" @change="search">
          <el-option label="Any status" value="" />
          <el-option label="Active" value="active" />
          <el-option label="Locked" value="locked" />
        </el-select>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" data-test="member-search-go" @click="search">Search</el-button>
      </el-form-item>
    </el-form>

    <p v-if="error" class="error" data-test="member-error">{{ error }}</p>

    <el-table
      v-loading="loading"
      :data="members"
      data-test="member-table"
      empty-text="No members match this search."
      @row-click="open"
    >
      <el-table-column prop="email" label="Email" min-width="220" />
      <el-table-column label="Status" width="110">
        <template #default="{ row }">
          <el-tag :type="row.status === 'locked' ? 'danger' : 'success'" size="small">
            {{ row.status }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="KYC" width="120">
        <template #default="{ row }">
          <el-tag :type="kycTag(row.kycStatus)" size="small">{{ row.kycStatus }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="createdAt" label="Registered" width="200" />
    </el-table>

    <el-pagination
      v-model:current-page="pageNo"
      :page-size="pageSize"
      :total="total"
      layout="prev, pager, next, total"
      class="pager"
      @current-change="load"
    />
  </section>
</template>

<script setup lang="ts">
// Member search (issue #58): support's view over api's member list —
// substring email or exact id, status filter, paged. The table links
// into the detail page; the server-side permission gate decides what a
// Role may actually do from there.
definePageMeta({ middleware: 'employee' })

interface MemberRow {
  id: string
  email: string
  status: string
  kycStatus: string
  createdAt: string
}

interface MemberSearchResponse {
  total: number
  members: MemberRow[]
}

const { apiFetch } = useApi()

const query = ref('')
const status = ref('')
const pageNo = ref(1)
const pageSize = 20
const total = ref(0)
const members = ref<MemberRow[]>([])
const loading = ref(false)
const error = ref('')

useHead({ title: 'Members' })

onMounted(load)

async function load(): Promise<void> {
  error.value = ''
  loading.value = true
  try {
    const params = new URLSearchParams({
      pageNo: String(pageNo.value),
      pageSize: String(pageSize),
    })
    if (query.value.trim() !== '') {
      params.set('q', query.value.trim())
    }
    if (status.value !== '') {
      params.set('status', status.value)
    }
    const out = await apiFetch<MemberSearchResponse>(`/api/members?${params.toString()}`)
    members.value = out.members
    total.value = out.total
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'The member list could not be loaded.'
  } finally {
    loading.value = false
  }
}

/** A fresh search restarts at the first page. */
function search(): void {
  pageNo.value = 1
  load()
}

function open(row: MemberRow): void {
  navigateTo(`/members/${row.id}`)
}

function kycTag(state: string): 'success' | 'warning' | 'danger' | 'info' {
  if (state === 'verified') {
    return 'success'
  }
  if (state === 'pending') {
    return 'warning'
  }
  if (state === 'rejected') {
    return 'danger'
  }
  return 'info'
}
</script>

<style scoped lang="scss">
.members {
  h1 {
    font-size: 20px;
    font-weight: 600;
    margin: 0 0 8px;
  }

  .hint {
    color: var(--text-secondary);
    font-size: 13px;
    margin: 0 0 16px;
  }

  .filters {
    margin-bottom: 8px;

    :deep(.el-input) {
      width: 280px;
    }

    :deep(.el-select) {
      width: 150px;
    }
  }

  .error {
    color: var(--el-color-danger, #f56c6c);
    font-size: 13px;
    margin: 0 0 8px;
  }

  :deep(.el-table__row) {
    cursor: pointer;
  }

  .pager {
    margin-top: 12px;
    justify-content: flex-end;
  }
}
</style>
