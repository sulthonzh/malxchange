// The Market editor (issue #61) over the exchange's Market-config RPC,
// fronted by admin-api's GET/PUT /api/markets. Super-admin's alone per
// the ADR-0012 matrix. The exchange live-applies every accepted change
// within its 5-second config watch and publishes market.updated; the
// freeze guard refuses tick/lot/scale edits while orders rest — that
// refusal arrives as a 409 sentence and is rendered as-is. The body is
// the diff against the loaded row: absent fields keep the stored value
// and an explicitly cleared collar sends null (unbounded).
<template>
  <section class="market-editor">
    <h1>Markets</h1>
    <p class="hint">
      Trading rules per Market: state flags (disabling halts the book — cancels
      still flow), fees, price and amount collars, minimum turnover, market
      orders, and listing order. The engine applies accepted changes within five
      seconds; tick, lot, and scale changes are refused while orders rest.
    </p>
    <el-alert v-if="loadError" class="error" type="error" :closable="false" :title="loadError" />
    <el-table v-if="markets.length" :data="markets" size="small">
      <el-table-column prop="market" label="Market" width="110" />
      <el-table-column label="State" width="90">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'danger'" size="small">
            {{ row.enabled ? 'enabled' : 'halted' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Listed" width="90">
        <template #default="{ row }">
          <el-tag :type="row.visible ? 'success' : 'info'" size="small">
            {{ row.visible ? 'visible' : 'hidden' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="makerFeeRate" label="Maker fee" width="90" />
      <el-table-column prop="takerFeeRate" label="Taker fee" width="90" />
      <el-table-column prop="tickSize" label="Tick" width="80" />
      <el-table-column prop="lotSize" label="Lot" width="90" />
      <el-table-column label="Price collar" width="150">
        <template #default="{ row }">{{ collar(row.minPrice, row.maxPrice) }}</template>
      </el-table-column>
      <el-table-column label="Amount collar" width="130">
        <template #default="{ row }">{{ collar(row.minAmount, row.maxAmount) }}</template>
      </el-table-column>
      <el-table-column prop="minTurnover" label="Min turnover" width="110" />
      <el-table-column label="Market orders" width="120">
        <template #default="{ row }">
          <el-tag :type="row.marketOrdersEnabled ? 'success' : 'info'" size="small">
            {{ row.marketOrdersEnabled ? 'on' : 'off' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="sort" label="Sort" width="70" />
      <el-table-column prop="zone" label="Zone" width="100" />
      <el-table-column label="">
        <template #default="{ row }">
          <el-button v-if="canEdit" size="small" @click="open(row)">Edit</el-button>
        </template>
      </el-table-column>
    </el-table>
    <p v-else-if="!loadError" class="hint">No Markets are configured yet.</p>

    <el-card v-if="editing" class="editor" shadow="never">
      <template #header>
        <span>Editing {{ form.market }}</span>
      </template>
      <el-alert v-if="saveError" class="error" type="error" :closable="false" :title="saveError" />
      <el-form label-position="top" @submit.prevent="save">
        <div class="grid">
          <el-form-item label="Enabled (off = halted, cancels still flow)">
            <el-switch v-model="form.enabled" name="enabled" />
          </el-form-item>
          <el-form-item label="Visible on the front">
            <el-switch v-model="form.visible" name="visible" />
          </el-form-item>
          <el-form-item label="Market orders enabled">
            <el-switch v-model="form.marketOrdersEnabled" name="marketOrdersEnabled" />
          </el-form-item>
          <el-form-item label="Featured">
            <el-switch v-model="form.featured" name="featured" />
          </el-form-item>
          <el-form-item label="Maker fee rate">
            <el-input v-model="form.makerFeeRate" name="makerFeeRate" placeholder="0.001" />
          </el-form-item>
          <el-form-item label="Taker fee rate">
            <el-input v-model="form.takerFeeRate" name="takerFeeRate" placeholder="0.001" />
          </el-form-item>
          <el-form-item label="Tick size">
            <el-input v-model="form.tickSize" name="tickSize" placeholder="0.10" />
          </el-form-item>
          <el-form-item label="Lot size">
            <el-input v-model="form.lotSize" name="lotSize" placeholder="0.0001" />
          </el-form-item>
          <el-form-item label="Base scale (display decimals)">
            <el-input v-model.number="form.baseScale" type="number" min="0" name="baseScale" />
          </el-form-item>
          <el-form-item label="Quote scale (display decimals)">
            <el-input v-model.number="form.quoteScale" type="number" min="0" name="quoteScale" />
          </el-form-item>
          <el-form-item label="Minimum price (blank = unbounded)">
            <el-input v-model="form.minPrice" name="minPrice" :placeholder="original?.minPrice ?? ''" />
          </el-form-item>
          <el-form-item label="Maximum price (blank = unbounded)">
            <el-input v-model="form.maxPrice" name="maxPrice" :placeholder="original?.maxPrice ?? ''" />
          </el-form-item>
          <el-form-item label="Minimum amount">
            <el-input v-model="form.minAmount" name="minAmount" placeholder="0" />
          </el-form-item>
          <el-form-item label="Maximum amount (blank = unbounded)">
            <el-input v-model="form.maxAmount" name="maxAmount" :placeholder="original?.maxAmount ?? ''" />
          </el-form-item>
          <el-form-item label="Minimum turnover">
            <el-input v-model="form.minTurnover" name="minTurnover" placeholder="5" />
          </el-form-item>
          <el-form-item label="Listing sort">
            <el-input v-model.number="form.sort" type="number" min="0" name="sort" />
          </el-form-item>
          <el-form-item label="Zone">
            <el-input v-model="form.zone" name="zone" placeholder="main" />
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
// The wire shape of the exchange's Market config rows (ADR-0009).
export interface MarketRow {
  market: string
  baseCoin: string
  quoteCoin: string
  baseScale: number
  quoteScale: number
  tickSize: string
  lotSize: string
  makerFeeRate: string
  takerFeeRate: string
  minPrice: string | null
  maxPrice: string | null
  minAmount: string
  maxAmount: string | null
  minTurnover: string
  marketOrdersEnabled: boolean
  enabled: boolean
  visible: boolean
  sort: number
  zone: string
  featured: boolean
}

type Collar = 'minPrice' | 'maxPrice' | 'maxAmount'

type EditForm = MarketRow & Record<Collar, string>

definePageMeta({ middleware: 'employee' })

const { apiFetch } = useApi()
const { session } = useAdminSession()

const markets = ref<MarketRow[]>([])
const loadError = ref('')
const editing = ref(false)
const saving = ref(false)
const saveError = ref('')
const form = ref<EditForm | null>(null)
const original = ref<EditForm | null>(null)

const canEdit = computed(() => (session.value?.permissions ?? []).includes('edit:market'))

/** Null collars render as blank inputs and travel back as JSON null. */
function toForm(row: MarketRow): EditForm {
  return {
    ...row,
    minPrice: row.minPrice ?? '',
    maxPrice: row.maxPrice ?? '',
    maxAmount: row.maxAmount ?? '',
  }
}

function collar(min: string | null, max: string | null): string {
  const lo = min ?? '—'
  const hi = max ?? '∞'
  return `${lo} … ${hi}`
}

useHead({ title: 'Markets' })

async function load(): Promise<void> {
  loadError.value = ''
  try {
    markets.value = await apiFetch<MarketRow[]>('/api/markets')
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The Market list could not be loaded.'
  }
}

function open(row: MarketRow): void {
  form.value = toForm(row)
  original.value = toForm(row)
  saveError.value = ''
  editing.value = true
}

/** Only changed fields travel; a cleared collar is an explicit null. */
function diff(): Record<string, unknown> {
  const patch: Record<string, unknown> = {}
  const now = form.value!
  const was = original.value!
  const scalar = (key: keyof MarketRow): void => {
    if (now[key] !== was[key]) {
      patch[key] = now[key]
    }
  }
  scalar('enabled')
  scalar('visible')
  scalar('marketOrdersEnabled')
  scalar('featured')
  scalar('makerFeeRate')
  scalar('takerFeeRate')
  scalar('tickSize')
  scalar('lotSize')
  scalar('baseScale')
  scalar('quoteScale')
  scalar('minAmount')
  scalar('minTurnover')
  scalar('sort')
  scalar('zone')
  for (const key of ['minPrice', 'maxPrice', 'maxAmount'] as const) {
    if (now[key] !== was[key]) {
      patch[key] = now[key] === '' ? null : now[key]
    }
  }
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
    // The market rides the path raw (house style — the member app's
    // /market/depth/BTC/USDT paths do the same).
    await apiFetch<MarketRow>(`/api/markets/${form.value.market}`, {
      method: 'PUT',
      body,
    })
    editing.value = false
    await load()
  } catch (error) {
    // A freeze-guard refusal arrives as the exchange's own sentence.
    saveError.value = error instanceof Error ? error.message : 'The change could not be saved.'
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped lang="scss">
.market-editor {
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
