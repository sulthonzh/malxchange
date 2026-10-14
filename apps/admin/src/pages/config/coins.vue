// The Coin editor (issue #61) over the wallet's Coin config RPC,
// fronted by admin-api's GET/PUT /api/coins. Super-admin's alone per
// the ADR-0012 matrix; the edit form only renders when the session's
// live permission set admits it — the server's permission middleware
// is the real gate either way. Fields the wallet treats as
// write-preserved (absent = keep, e.g. the xpub custody field) are
// sent only when actually changed: the body is the diff against the
// loaded row.
<template>
  <section class="coin-editor">
    <h1>Coins</h1>
    <p class="hint">
      Chain configuration the wallet enforces: deposit confirmations, withdrawal
      fee and minimum, sweep behavior, and custody ceilings. Changes take effect
      on the next withdrawal or sweep pass.
    </p>
    <el-alert v-if="loadError" class="error" type="error" :closable="false" :title="loadError" />
    <el-table v-if="coins.length" :data="coins" size="small">
      <el-table-column prop="name" label="Coin" width="90" />
      <el-table-column prop="family" label="Family" width="80" />
      <el-table-column prop="scale" label="Scale" width="70" />
      <el-table-column prop="confirmations" label="Confirmations" width="120" />
      <el-table-column prop="withdrawFee" label="Withdraw fee" width="110" />
      <el-table-column prop="minWithdraw" label="Min withdraw" width="110" />
      <el-table-column label="Withdrawable" width="110">
        <template #default="{ row }">
          <el-tag :type="row.withdrawable ? 'success' : 'info'" size="small">
            {{ row.withdrawable ? 'open' : 'closed' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="sweepThreshold" label="Sweep threshold" width="120" />
      <el-table-column label="Sweeps" width="100">
        <template #default="{ row }">
          <el-tag :type="row.sweepPaused ? 'warning' : 'success'" size="small">
            {{ row.sweepPaused ? 'paused' : 'running' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="coldAddress" label="Cold address" min-width="160" />
      <el-table-column label="">
        <template #default="{ row }">
          <el-button v-if="canEdit" size="small" @click="open(row)">Edit</el-button>
        </template>
      </el-table-column>
    </el-table>
    <p v-else-if="!loadError" class="hint">No Coins are configured yet.</p>

    <el-card v-if="editing" class="editor" shadow="never">
      <template #header>
        <span>Editing {{ form.name }}</span>
      </template>
      <el-alert v-if="saveError" class="error" type="error" :closable="false" :title="saveError" />
      <el-form label-position="top" @submit.prevent="save">
        <div class="grid">
          <el-form-item label="Scale (display decimals)">
            <el-input v-model.number="form.scale" type="number" min="0" name="scale" />
          </el-form-item>
          <el-form-item label="Deposit confirmations">
            <el-input v-model.number="form.confirmations" type="number" min="1" name="confirmations" />
          </el-form-item>
          <el-form-item label="Withdrawal fee">
            <el-input v-model="form.withdrawFee" name="withdrawFee" placeholder="0.0005" />
          </el-form-item>
          <el-form-item label="Minimum withdrawal">
            <el-input v-model="form.minWithdraw" name="minWithdraw" placeholder="0.001" />
          </el-form-item>
          <el-form-item label="Withdrawals open">
            <el-switch v-model="form.withdrawable" name="withdrawable" />
          </el-form-item>
          <el-form-item label="Sweep threshold">
            <el-input v-model="form.sweepThreshold" name="sweepThreshold" placeholder="0.05" />
          </el-form-item>
          <el-form-item label="Minimum sweep interval (seconds)">
            <el-input v-model.number="form.sweepMinSeconds" type="number" min="0" name="sweepMinSeconds" />
          </el-form-item>
          <el-form-item label="Sweeps paused">
            <el-switch v-model="form.sweepPaused" name="sweepPaused" />
          </el-form-item>
          <el-form-item label="Hot-balance ceiling (0 = unset)">
            <el-input v-model="form.hotCeiling" name="hotCeiling" placeholder="0" />
          </el-form-item>
          <el-form-item label="Hot-balance floor">
            <el-input v-model="form.hotFloor" name="hotFloor" placeholder="0" />
          </el-form-item>
          <el-form-item label="Cold address">
            <el-input v-model="form.coldAddress" name="coldAddress" placeholder="bc1… (hot→cold destination)" />
          </el-form-item>
          <el-form-item label="Watch-only account key (xpub)">
            <el-input v-model="form.xpub" name="xpub" placeholder="paste to (re)arm the watcher" />
          </el-form-item>
        </div>
        <div class="actions">
          <el-button @click="editing = false">Cancel</el-button>
          <el-button type="warning" native-type="submit" :loading="saving">Save</el-button>
        </div>
      </el-form>
    </el-card>
  </section>
</template>

<script setup lang="ts">
// The wire shape of the wallet's Coin rows (absent here: the xpub —
// write-only, never rendered back by the api).
export interface CoinRow {
  name: string
  family: string
  native?: boolean
  contract?: string
  scale: number
  chainDecimals?: number
  confirmations: number
  withdrawFee: string
  minWithdraw: string
  withdrawable: boolean
  sweepThreshold: string
  sweepMinSeconds: number
  sweepPaused: boolean
  coldAddress: string
  hotCeiling: string
  hotFloor: string
}

definePageMeta({ middleware: 'employee' })

const { apiFetch } = useApi()
const { session } = useAdminSession()

const coins = ref<CoinRow[]>([])
const loadError = ref('')
const editing = ref(false)
const saving = ref(false)
const saveError = ref('')
const form = ref<CoinRow & { xpub: string }>({ ...emptyForm() })
const original = ref<(CoinRow & { xpub: string }) | null>(null)

const canEdit = computed(() => (session.value?.permissions ?? []).includes('edit:coin'))

function emptyForm(): CoinRow & { xpub: string } {
  return {
    name: '', family: '', scale: 0, confirmations: 1,
    withdrawFee: '0', minWithdraw: '0', withdrawable: false,
    sweepThreshold: '0', sweepMinSeconds: 0, sweepPaused: false,
    coldAddress: '', hotCeiling: '0', hotFloor: '0', xpub: '',
  }
}

useHead({ title: 'Coins' })

async function load(): Promise<void> {
  loadError.value = ''
  try {
    coins.value = await apiFetch<CoinRow[]>('/api/coins')
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The Coin list could not be loaded.'
  }
}

function open(row: CoinRow): void {
  form.value = { ...row, xpub: '' }
  original.value = { ...row, xpub: '' }
  saveError.value = ''
  editing.value = true
}

/** Only changed fields travel — absent means keep on the wallet side. */
function diff(): Record<string, unknown> {
  const patch: Record<string, unknown> = {}
  const now = form.value
  const was = original.value!
  const put = (key: string, value: unknown, changed: boolean): void => {
    if (changed) {
      patch[key] = value
    }
  }
  put('scale', now.scale, now.scale !== was.scale)
  put('confirmations', now.confirmations, now.confirmations !== was.confirmations)
  put('withdrawFee', now.withdrawFee, now.withdrawFee !== was.withdrawFee)
  put('minWithdraw', now.minWithdraw, now.minWithdraw !== was.minWithdraw)
  put('withdrawable', now.withdrawable, now.withdrawable !== was.withdrawable)
  put('sweepThreshold', now.sweepThreshold, now.sweepThreshold !== was.sweepThreshold)
  put('sweepMinSeconds', now.sweepMinSeconds, now.sweepMinSeconds !== was.sweepMinSeconds)
  put('sweepPaused', now.sweepPaused, now.sweepPaused !== was.sweepPaused)
  put('coldAddress', now.coldAddress, now.coldAddress !== was.coldAddress)
  put('hotCeiling', now.hotCeiling, now.hotCeiling !== was.hotCeiling)
  put('hotFloor', now.hotFloor, now.hotFloor !== was.hotFloor)
  // The custody key travels only when typed into — an empty field must
  // never wipe the stored xpub.
  put('xpub', now.xpub, now.xpub !== '' )
  return patch
}

async function save(): Promise<void> {
  const body = diff()
  if (Object.keys(body).length === 0) {
    editing.value = false
    return
  }
  saving.value = true
  saveError.value = ''
  try {
    await apiFetch<Record<string, unknown>>(`/api/coins/${form.value.name}`, {
      method: 'PUT',
      body,
    })
    editing.value = false
    await load()
  } catch (error) {
    saveError.value = error instanceof Error ? error.message : 'The change could not be saved.'
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped lang="scss">
.coin-editor {
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

  .error {
    margin-bottom: 12px;
  }

  .editor {
    margin-top: 16px;

    .grid {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
      gap: 0 16px;
    }

    .actions {
      display: flex;
      justify-content: flex-end;
      gap: 8px;
    }
  }
}
</style>
