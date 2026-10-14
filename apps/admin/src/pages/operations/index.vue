<template>
  <section class="ops">
    <h1>Wallet Ops</h1>
    <p class="hint">
      Custody risk and withdrawal liquidity at a glance: failed operations, hot-balance
      ceiling/floor alerts per Coin, open incidents, and sweep pauses.
    </p>

    <el-alert
      v-if="error"
      :title="error"
      type="error"
      show-icon
      :closable="false"
      class="block"
    />
    <el-alert
      v-else-if="summary && !summary.foldOK"
      title="Ledger does not fold onto the Journal — run Reconcile for the per-Coin figures."
      type="error"
      show-icon
      :closable="false"
      class="block"
    />

    <div class="stats">
      <el-card class="stat">
        <p class="stat-label">Failed withdrawals</p>
        <p class="stat-value" data-test="failed-withdrawals">{{ summary?.failed.withdrawals ?? '—' }}</p>
      </el-card>
      <el-card class="stat">
        <p class="stat-label">Failed sweeps</p>
        <p class="stat-value" data-test="failed-sweeps">{{ summary?.failed.sweeps ?? '—' }}</p>
      </el-card>
      <el-card class="stat">
        <p class="stat-label">Failed hot transfers</p>
        <p class="stat-value" data-test="failed-hot-transfers">{{ summary?.failed.hotTransfers ?? '—' }}</p>
      </el-card>
      <el-card class="stat">
        <p class="stat-label">Open incidents</p>
        <p class="stat-value" data-test="open-incidents">{{ summary?.openIncidents ?? '—' }}</p>
      </el-card>
    </div>

    <h2>Alerts</h2>
    <el-empty
      v-if="summary && summary.alerts.length === 0"
      description="No ceiling or floor alerts — every Coin sits inside its corridor."
      :image-size="60"
    />
    <el-alert
      v-for="alert in summary?.alerts ?? []"
      :key="alert.coin + alert.kind"
      :title="`${alert.coin} — ${alert.kind === 'hot_ceiling' ? 'above ceiling' : 'below floor'}`"
      :description="alert.detail"
      :type="alert.kind === 'hot_ceiling' ? 'warning' : 'info'"
      show-icon
      :closable="false"
      class="block"
    />

    <h2>Hot balances per Coin</h2>
    <el-table :data="coinRows" data-test="coin-rows">
      <el-table-column prop="coin" label="Coin" width="120" />
      <el-table-column label="Hot balance">
        <template #default="{ row }">
          <span v-if="row.hot !== null">{{ row.hot }}</span>
          <span v-else class="muted">unknown (no chain client)</span>
        </template>
      </el-table-column>
      <el-table-column label="Alert state" width="160">
        <template #default="{ row }">
          <el-tag v-if="row.alert === 'hot_ceiling'" type="warning">Above ceiling</el-tag>
          <el-tag v-else-if="row.alert === 'hot_floor'" type="info">Below floor</el-tag>
          <el-tag v-else type="success">Inside corridor</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Sweeps" width="180">
        <template #default="{ row }">
          <el-switch
            :model-value="row.paused"
            :disabled="!canPause"
            :loading="row.busy"
            :aria-label="`Sweeps for ${row.coin}`"
            @change="togglePause(row, $event)"
          />
          <span class="switch-label">{{ row.paused ? 'paused' : 'running' }}</span>
        </template>
      </el-table-column>
    </el-table>
    <p v-if="!canPause" class="hint">
      Pausing sweeps needs the pause:sweep permission; the switch is read-only for your Role.
    </p>
  </section>
</template>

<script setup lang="ts">
// The Wallet Ops dashboard (issue #60): the admin-api /api/ops summary
// (wallet's GET /ops/summary) — failed withdrawal/sweep/hot-transfer
// counts, per-Coin hot balances with ceiling/floor alert states, open
// incidents, sweep pauses. The pause switch fronts POST
// /api/coins/{name}/sweep-pause and is permission-gated in the UI the
// same way the server gates it (the server is the real gate).
definePageMeta({ middleware: 'employee' })

interface OpsAlert {
  coin: string
  kind: 'hot_ceiling' | 'hot_floor'
  detail: string
}

interface OpsSummary {
  alerts: OpsAlert[]
  hotBalances: Record<string, string>
  failed: { withdrawals: number; sweeps: number; hotTransfers: number }
  openIncidents: number
  sweepsPaused: string[]
  foldOK: boolean
}

interface CoinRow {
  coin: string
  hot: string | null
  alert: '' | 'hot_ceiling' | 'hot_floor'
  paused: boolean
  busy: boolean
}

const { apiFetch } = useApi()
const { session } = useAdminSession()

const summary = ref<OpsSummary | null>(null)
const error = ref('')
const canPause = computed(() => (session.value?.permissions ?? []).includes('pause:sweep'))

const coinRows = computed<CoinRow[]>(() => {
  if (!summary.value) {
    return []
  }
  const coins = new Set([
    ...Object.keys(summary.value.hotBalances),
    ...summary.value.sweepsPaused,
  ])
  const alertOf = new Map(summary.value.alerts.map((a) => [a.coin, a.kind]))
  return [...coins].sort().map((coin) => ({
    coin,
    hot: summary.value?.hotBalances[coin] ?? null,
    alert: alertOf.get(coin) ?? '',
    paused: summary.value?.sweepsPaused.includes(coin) ?? false,
    busy: false,
  }))
})

async function load(): Promise<void> {
  error.value = ''
  try {
    summary.value = await apiFetch<OpsSummary>('/api/ops')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'The summary could not be loaded.'
  }
}

async function togglePause(row: CoinRow, paused: unknown): Promise<void> {
  row.busy = true
  error.value = ''
  try {
    await apiFetch(`/api/coins/${encodeURIComponent(row.coin)}/sweep-pause`, {
      method: 'POST',
      body: { paused: Boolean(paused) },
    })
    await load()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'The pause could not be changed.'
    row.busy = false
  }
}

onMounted(load)
useHead({ title: 'Wallet Ops' })
</script>

<style scoped lang="scss">
.ops {
  h1 {
    font-size: 20px;
    font-weight: 600;
    margin: 0 0 8px;
  }

  h2 {
    font-size: 15px;
    font-weight: 600;
    margin: 20px 0 8px;
  }

  .hint {
    color: var(--text-secondary);
    font-size: 13px;
    margin: 0 0 12px;
  }

  .block {
    margin-bottom: 10px;
  }

  .stats {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 10px;
    margin: 12px 0 4px;
  }

  .stat-label {
    color: var(--text-secondary);
    font-size: 12px;
    margin: 0 0 4px;
  }

  .stat-value {
    font-size: 24px;
    font-weight: 600;
    margin: 0;
  }

  .muted {
    color: var(--text-muted);
  }

  .switch-label {
    color: var(--text-secondary);
    font-size: 12px;
    margin-left: 8px;
  }
}
</style>
