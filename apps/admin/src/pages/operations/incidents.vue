<template>
  <section class="incidents">
    <h1>Incidents</h1>
    <p class="hint">
      Wallet-raised incidents — deep reorgs, chain failures — are adjudicated by hand: resolving
      one records the human decision; nothing is ever clawed back automatically (ADR-0008).
    </p>

    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false" class="block" />

    <div class="toolbar">
      <el-radio-group v-model="statusFilter" data-test="status-filter" @change="load">
        <el-radio-button value="open">Open</el-radio-button>
        <el-radio-button value="resolved">Resolved</el-radio-button>
        <el-radio-button value="">All</el-radio-button>
      </el-radio-group>
      <el-button :loading="busy" @click="load">Refresh</el-button>
    </div>

    <el-table :data="incidents" data-test="incident-rows">
      <el-table-column prop="coin" label="Coin" width="90" />
      <el-table-column prop="kind" label="Kind" width="140" />
      <el-table-column prop="detail" label="Detail" show-overflow-tooltip />
      <el-table-column label="Status" width="120">
        <template #default="{ row }">
          <el-tag :type="row.status === 'open' ? 'danger' : 'success'">{{ row.status }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="createdAt" label="Raised" width="180" />
      <el-table-column label="" width="120">
        <template #default="{ row }">
          <el-button
            v-if="row.status === 'open'"
            size="small"
            type="primary"
            :data-test="`resolve-${row.id}`"
            :loading="resolving === row.id"
            @click="resolve(row)"
          >
            Resolve
          </el-button>
        </template>
      </el-table-column>
    </el-table>
  </section>
</template>

<script setup lang="ts">
// The incidents page (issue #60): wallet's open incidents with resolve
// — POST /api/incidents/{id}/resolve is audit-logged with the incident
// id as target (the middleware writes it; the page only renders).
definePageMeta({ middleware: 'employee' })

interface Incident {
  id: string
  coin: string
  kind: string
  detail: string
  status: 'open' | 'resolved'
  createdAt: string
}

const { apiFetch } = useApi()

const error = ref('')
const busy = ref(false)
const resolving = ref('')
const statusFilter = ref<'open' | 'resolved' | ''>('open')
const incidents = ref<Incident[]>([])

async function load(): Promise<void> {
  busy.value = true
  error.value = ''
  try {
    const query = statusFilter.value === '' ? '' : `?status=${statusFilter.value}`
    incidents.value = await apiFetch<Incident[]>(`/api/incidents${query}`)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'The incidents could not be loaded.'
  } finally {
    busy.value = false
  }
}

async function resolve(row: Incident): Promise<void> {
  resolving.value = row.id
  error.value = ''
  try {
    await apiFetch(`/api/incidents/${encodeURIComponent(row.id)}/resolve`, {
      method: 'POST',
      body: {},
    })
    await load()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'The incident could not be resolved.'
  } finally {
    resolving.value = ''
  }
}

onMounted(load)
useHead({ title: 'Incidents' })
</script>

<style scoped lang="scss">
.incidents {
  h1 {
    font-size: 20px;
    font-weight: 600;
    margin: 0 0 8px;
  }

  .hint {
    color: var(--text-secondary);
    font-size: 13px;
    margin: 0 0 12px;
  }

  .block {
    margin-bottom: 10px;
  }

  .toolbar {
    display: flex;
    gap: 10px;
    margin-bottom: 12px;
  }
}
</style>
