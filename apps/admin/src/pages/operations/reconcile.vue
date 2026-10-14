<template>
  <section class="reconcile">
    <h1>Reconcile</h1>
    <p class="hint">
      The fold invariant (Ledger == Journal, per Coin) and the hot-wallet reconciliation
      (expected vs chain balance). A nonzero delta means custody drift — investigate before
      moving funds.
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
      v-else-if="report && !report.ok"
      title="Reconciliation found a mismatch — see the flags below."
      type="error"
      show-icon
      :closable="false"
      class="block"
    />
    <el-alert
      v-else-if="report && report.ok"
      title="Everything folds: Ledger == Journal and every Coin's hot wallet matches its expectation."
      type="success"
      show-icon
      :closable="false"
      class="block"
    />

    <div class="toolbar">
      <el-button :loading="busy" @click="load">Refresh</el-button>
    </div>

    <h2>Fold — Ledger vs Journal</h2>
    <el-table :data="report?.fold ?? []" data-test="fold-rows">
      <el-table-column prop="coin" label="Coin" width="120" />
      <el-table-column prop="ledgerAvail" label="Ledger available" />
      <el-table-column prop="ledgerFrozen" label="Ledger frozen" />
      <el-table-column prop="journalAvail" label="Journal available" />
      <el-table-column prop="journalFrozen" label="Journal frozen" />
      <el-table-column label="Folds" width="100">
        <template #default="{ row }">
          <el-tag :type="row.ok ? 'success' : 'danger'">{{ row.ok ? 'yes' : 'no' }}</el-tag>
        </template>
      </el-table-column>
    </el-table>

    <h2>Hot wallet per Coin</h2>
    <el-table :data="report?.coins ?? []" data-test="coin-rows">
      <el-table-column prop="coin" label="Coin" width="100" />
      <el-table-column prop="ledgerSum" label="Ledger sum" />
      <el-table-column prop="feesCollected" label="Fees" />
      <el-table-column prop="coldOut" label="Cold out" />
      <el-table-column prop="coldIn" label="Cold in" />
      <el-table-column prop="inFlight" label="In flight" />
      <el-table-column prop="unswept" label="Unswept" />
      <el-table-column prop="expectedHot" label="Expected hot" />
      <el-table-column label="Chain hot">
        <template #default="{ row }">
          <span v-if="row.chainHot !== ''">{{ row.chainHot }}</span>
          <span v-else class="muted">unknown</span>
        </template>
      </el-table-column>
      <el-table-column prop="delta" label="Delta" />
      <el-table-column label="OK" width="90">
        <template #default="{ row }">
          <el-tag :type="row.ok ? 'success' : 'danger'">{{ row.ok ? 'yes' : 'no' }}</el-tag>
        </template>
      </el-table-column>
    </el-table>
  </section>
</template>

<script setup lang="ts">
// The reconcile view (issue #60): wallet's GET /reconcile — the
// Ledger==Journal fold invariant plus per-Coin expected-vs-chain hot
// figures. Read-only; the numbers come straight from wallet.
definePageMeta({ middleware: 'employee' })

interface FoldCheck {
  coin: string
  ledgerAvail: string
  ledgerFrozen: string
  journalAvail: string
  journalFrozen: string
  ok: boolean
}

interface CoinRecon {
  coin: string
  ledgerSum: string
  feesCollected: string
  coldOut: string
  coldIn: string
  inFlight: string
  unswept: string
  expectedHot: string
  chainHot: string
  delta: string
  ok: boolean
}

interface ReconcileReport {
  ok: boolean
  fold: FoldCheck[]
  coins: CoinRecon[]
}

const { apiFetch } = useApi()

const busy = ref(false)
const error = ref('')
const report = ref<ReconcileReport | null>(null)

async function load(): Promise<void> {
  busy.value = true
  error.value = ''
  try {
    report.value = await apiFetch<ReconcileReport>('/api/ops/reconcile')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'The reconcile report could not be loaded.'
  } finally {
    busy.value = false
  }
}

onMounted(load)
useHead({ title: 'Reconcile' })
</script>

<style scoped lang="scss">
.reconcile {
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

  .toolbar {
    margin-bottom: 8px;
  }

  .muted {
    color: var(--text-muted);
  }
}
</style>
