// The engine status page (issue #61): the all-Roles window into the
// trading engine (ADR-0012 — read-side for everyone). Per Market the
// Book Ready state, open Orders, Halted flag, and sequence; plus the
// command consumer's lag, which the status RPC now reports (an honest
// unknown when the broker cannot answer). The view refreshes itself
// every five seconds — the same cadence the engine live-applies config
// changes on.
<template>
  <section class="engine-status">
    <h1>Engine status</h1>
    <p class="hint">
      Live state of the matching engine per Market, refreshed every five seconds.
      Disabling a Market halts its book — resting orders stay, cancels still flow.
    </p>
    <div class="lag">
      <span class="label">Consumer lag</span>
      <el-tag v-if="lag === null" type="info" size="small">unknown</el-tag>
      <el-tag v-else-if="lag === 0" type="success" size="small">caught up</el-tag>
      <el-tag v-else :type="lag > 100 ? 'danger' : 'warning'" size="small">{{ lag }} behind</el-tag>
      <span v-if="refreshedAt" class="at">as of {{ refreshedAt }}</span>
      <el-button class="refresh" size="small" :loading="loading" @click="load">Refresh</el-button>
    </div>
    <el-alert v-if="loadError" class="error" type="error" :closable="false" :title="loadError" />
    <el-table v-if="markets.length" :data="markets" size="small">
      <el-table-column prop="market" label="Market" width="120" />
      <el-table-column label="Book ready" width="110">
        <template #default="{ row }">
          <el-tag :type="row.ready ? 'success' : 'danger'" size="small">
            {{ row.ready ? 'ready' : 'recovering' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Accepting orders" width="130">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'warning'" size="small">
            {{ row.enabled ? 'yes' : 'no — halted' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Halted" width="90">
        <template #default="{ row }">
          <el-tag :type="row.halted ? 'danger' : 'info'" size="small">{{ row.halted ? 'halted' : 'no' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="openOrders" label="Open orders" width="110" />
      <el-table-column prop="lastSeq" label="Last sequence" width="120" />
    </el-table>
    <p v-else-if="!loadError && !loading" class="hint">The engine has no Markets loaded.</p>
  </section>
</template>

<script setup lang="ts">
// StatusSnapshot on the wire (internal/exchange sweep.go) plus the
// engine status envelope's consumerLag.
export interface EngineStatusSnapshot {
  market: string
  ready: boolean
  enabled: boolean
  halted: boolean
  openOrders: number
  lastSeq: number
}

export interface EngineStatus {
  markets: EngineStatusSnapshot[]
  /** null = the broker could not answer; never a lying zero. */
  consumerLag: number | null
  at: string
}

definePageMeta({ middleware: 'employee' })

const { apiFetch } = useApi()

const markets = ref<EngineStatusSnapshot[]>([])
const lag = ref<number | null>(null)
const refreshedAt = ref('')
const loadError = ref('')
const loading = ref(false)

const POLL_MS = 5000
let timer: ReturnType<typeof setInterval> | undefined

useHead({ title: 'Engine status' })

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const body = await apiFetch<EngineStatus>('/api/engine')
    markets.value = body.markets ?? []
    lag.value = body.consumerLag
    refreshedAt.value = new Date().toLocaleTimeString()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The engine status could not be loaded.'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, POLL_MS)
})

onUnmounted(() => {
  if (timer !== undefined) {
    clearInterval(timer)
  }
})
</script>

<style scoped lang="scss">
.engine-status {
  h1 {
    font-size: 20px;
    font-weight: 600;
    margin: 0 0 4px;
  }

  .hint {
    color: var(--text-secondary);
    font-size: 13px;
    margin: 0 0 16px;
  }

  .lag {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 16px;

    .label {
      color: var(--text-secondary);
      font-size: 13px;
    }

    .at {
      color: var(--text-muted);
      font-size: 12px;
    }

    .refresh {
      margin-left: auto;
    }
  }

  .error {
    margin-bottom: 12px;
  }
}
</style>
