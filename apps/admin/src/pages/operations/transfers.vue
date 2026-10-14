<template>
  <section class="transfers">
    <h1>Transfers</h1>
    <p class="hint">
      Hot→cold movements are created here (each demands a fresh authenticator code); the
      cold→hot direction is an offline procedure recorded here with its chain txid.
    </p>

    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false" class="block" />
    <el-alert v-if="notice" :title="notice" type="success" show-icon :closable="false" class="block" />

    <el-tabs v-model="tab">
      <el-tab-pane label="Hot → cold" name="hot">
        <el-card class="panel" data-test="create-hot">
          <p class="panel-title">Move hot funds to cold</p>
          <el-form inline @submit.prevent>
            <el-form-item label="Coin">
              <el-input v-model="hotForm.coin" placeholder="BTC" style="width: 120px" />
            </el-form-item>
            <el-form-item label="Amount">
              <el-input v-model="hotForm.amount" placeholder="0.5" style="width: 160px" />
            </el-form-item>
            <el-form-item label="Authenticator code">
              <el-input
                v-model="hotForm.code"
                placeholder="6-digit code"
                style="width: 140px"
                data-test="hot-code"
              />
            </el-form-item>
            <el-form-item>
              <el-button
                type="primary"
                data-test="hot-submit"
                :loading="hotBusy"
                @click="createHotTransfer"
              >
                Move to cold
              </el-button>
            </el-form-item>
          </el-form>
          <p class="hint">
            The transfer signs like a withdrawal to the Coin's configured cold address; the
            dashboard suggests it when a Coin sits above its ceiling.
          </p>
        </el-card>

        <el-table :data="hotTransfers" data-test="hot-rows">
          <el-table-column prop="coin" label="Coin" width="90" />
          <el-table-column prop="amount" label="Amount" width="130" />
          <el-table-column prop="address" label="Cold address" show-overflow-tooltip />
          <el-table-column label="State" width="110">
            <template #default="{ row }">
              <el-tag :type="stateTag(row.state)">{{ row.state }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="txid" label="TxID" show-overflow-tooltip />
          <el-table-column prop="error" label="Error" show-overflow-tooltip />
          <el-table-column prop="createdAt" label="Created" width="180" />
          <el-table-column label="" width="110">
            <template #default="{ row }">
              <el-button
                v-if="row.state === 'failed' && canRetry"
                size="small"
                data-test="hot-retry"
                @click="retryHotTransfer(row)"
              >
                Retry
              </el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="Cold → hot" name="cold">
        <el-card class="panel" data-test="record-cold">
          <p class="panel-title">Record an offline cold→hot transfer</p>
          <el-form inline @submit.prevent>
            <el-form-item label="Coin">
              <el-input v-model="coldForm.coin" placeholder="BTC" style="width: 120px" />
            </el-form-item>
            <el-form-item label="Amount">
              <el-input v-model="coldForm.amount" placeholder="1" style="width: 160px" />
            </el-form-item>
            <el-form-item label="Chain txid">
              <el-input v-model="coldForm.txid" placeholder="txid" style="width: 260px" />
            </el-form-item>
            <el-form-item>
              <el-button type="primary" data-test="cold-submit" :loading="coldBusy" @click="recordColdTransfer">
                Record
              </el-button>
            </el-form-item>
          </el-form>
          <p class="hint">
            Cold keys never touch the host (ADR-0008): the transfer happened offline — this
            record is the paper trail, appended with your name and the txid.
          </p>
        </el-card>

        <el-table :data="coldTransfers" data-test="cold-rows">
          <el-table-column prop="coin" label="Coin" width="90" />
          <el-table-column prop="amount" label="Amount" width="130" />
          <el-table-column prop="txid" label="TxID" show-overflow-tooltip />
          <el-table-column prop="actor" label="Recorded by" width="200" />
          <el-table-column prop="notedAt" label="Recorded" width="180" />
        </el-table>
      </el-tab-pane>
    </el-tabs>
  </section>
</template>

<script setup lang="ts">
// The custody transfers page (issue #60): hot→cold creation (Step-up,
// ADR-0012) over POST /api/hot-transfers with retry of failed records
// (Step-up), and the cold→hot paper trail over POST /api/cold-transfers.
// Lists read the live wallet state — the state machine's current word.
definePageMeta({ middleware: 'employee' })

interface HotTransfer {
  id: string
  coin: string
  amount: string
  address: string
  state: string
  txid: string
  error: string
  actor: string
  createdAt: string
}

interface ColdTransfer {
  id: string
  coin: string
  amount: string
  txid: string
  actor: string
  notedAt: string
}

const { apiFetch } = useApi()
const { session } = useAdminSession()

const tab = ref<'hot' | 'cold'>('hot')
const error = ref('')
const notice = ref('')
const hotBusy = ref(false)
const coldBusy = ref(false)
const hotForm = reactive({ coin: '', amount: '', code: '' })
const coldForm = reactive({ coin: '', amount: '', txid: '' })
const hotTransfers = ref<HotTransfer[]>([])
const coldTransfers = ref<ColdTransfer[]>([])

const canRetry = computed(() =>
  (session.value?.permissions ?? []).some((p) => p === 'retry:hot-transfer'),
)

function stateTag(state: string): 'success' | 'warning' | 'info' | 'danger' {
  switch (state) {
    case 'confirmed':
      return 'success'
    case 'failed':
      return 'danger'
    case 'broadcast':
      return 'warning'
    default:
      return 'info'
  }
}

async function load(): Promise<void> {
  error.value = ''
  try {
    const [hot, cold] = await Promise.all([
      apiFetch<HotTransfer[]>('/api/hot-transfers'),
      apiFetch<ColdTransfer[]>('/api/cold-transfers'),
    ])
    hotTransfers.value = hot
    coldTransfers.value = cold
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'The transfer lists could not be loaded.'
  }
}

function fail(cause: unknown, fallback: string): void {
  error.value = cause instanceof Error ? cause.message : fallback
}

async function createHotTransfer(): Promise<void> {
  hotBusy.value = true
  error.value = ''
  notice.value = ''
  try {
    await apiFetch('/api/hot-transfers', {
      method: 'POST',
      body: { coin: hotForm.coin.trim(), amount: hotForm.amount.trim(), code: hotForm.code.trim() },
    })
    notice.value = 'The hot→cold transfer was created and is pending signature.'
    hotForm.coin = ''
    hotForm.amount = ''
    hotForm.code = ''
    await load()
  } catch (cause) {
    fail(cause, 'The transfer could not be created.')
  } finally {
    hotBusy.value = false
  }
}

async function retryHotTransfer(row: HotTransfer): Promise<void> {
  error.value = ''
  notice.value = ''
  // The retry is Step-up-gated like the creation: the code input at
  // the top of the tab is the challenge; an empty code gets the
  // server's 401 sentence rendered above.
  const code = hotForm.code.trim()
  try {
    await apiFetch(`/api/hot-transfers/${encodeURIComponent(row.id)}/retry`, {
      method: 'POST',
      body: { code },
    })
    notice.value = `The transfer ${row.id} was re-armed and is pending signature.`
    await load()
  } catch (cause) {
    fail(cause, 'The retry was refused.')
  }
}

async function recordColdTransfer(): Promise<void> {
  coldBusy.value = true
  error.value = ''
  notice.value = ''
  try {
    await apiFetch('/api/cold-transfers', {
      method: 'POST',
      body: {
        coin: coldForm.coin.trim(),
        amount: coldForm.amount.trim(),
        txid: coldForm.txid.trim(),
      },
    })
    notice.value = 'The cold→hot record was appended to the custody trail.'
    coldForm.coin = ''
    coldForm.amount = ''
    coldForm.txid = ''
    await load()
  } catch (cause) {
    fail(cause, 'The record could not be appended.')
  } finally {
    coldBusy.value = false
  }
}

onMounted(load)
useHead({ title: 'Transfers' })
</script>

<style scoped lang="scss">
.transfers {
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

  .panel {
    margin-bottom: 14px;
  }

  .panel-title {
    font-size: 14px;
    font-weight: 600;
    margin: 0 0 10px;
  }
}
</style>
