<template>
  <section class="withdrawals">
    <h1>Withdrawals</h1>
    <p class="hint">
      Every member withdrawal waits in the audit state until finance decides: approve sends it to
      the signer, reject returns the funds. Nothing broadcasts unaudited.
    </p>

    <el-form class="filters" inline @submit.prevent="load">
      <el-form-item label="Member">
        <el-input
          v-model="filters.member"
          name="member"
          placeholder="Member id or email"
          clearable
          @keyup.enter="load"
        />
      </el-form-item>
      <el-form-item label="State">
        <el-select v-model="filters.state" name="state" clearable placeholder="All states" style="width: 160px">
          <el-option v-for="state in states" :key="state" :label="state" :value="state" />
        </el-select>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" native-type="submit" :loading="loading">Search</el-button>
      </el-form-item>
    </el-form>

    <el-alert v-if="error" class="error" type="error" :closable="false" :title="error" />

    <el-table :data="rows" v-loading="loading" empty-text="No withdrawals match these filters.">
      <el-table-column label="ID" width="310">
        <template #default="{ row }">
          <NuxtLink :to="`/finance/withdrawals/${row.id}`" class="id-link">{{ row.id }}</NuxtLink>
        </template>
      </el-table-column>
      <el-table-column prop="coin" label="Coin" width="90" />
      <el-table-column prop="member" label="Member" min-width="120" show-overflow-tooltip />
      <el-table-column prop="address" label="Address" min-width="160" show-overflow-tooltip />
      <el-table-column prop="amount" label="Amount" width="120" align="right" />
      <el-table-column prop="fee" label="Fee" width="110" align="right" />
      <el-table-column prop="net" label="Net" width="120" align="right" />
      <el-table-column label="State" width="110">
        <template #default="{ row }">
          <el-tag :type="stateTag[row.state] ?? 'info'" size="small">{{ row.state }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="createdAt" label="Created" min-width="170" show-overflow-tooltip />
    </el-table>
  </section>
</template>

<script setup lang="ts">
// The withdrawal audit queue (issue #59): member/state filters over
// the wallet's list RPC through admin-api. Money values arrive as
// decimal strings at the Coin's scale and render verbatim — never
// floats (ADR-0005). Decisions live on the detail page.
definePageMeta({ middleware: 'employee' })

interface WithdrawalRow {
  id: string
  coin: string
  member: string
  address: string
  amount: string
  fee: string
  net: string
  state: string
  txid?: string
  error?: string
  createdAt: string
}

const { apiFetch } = useApi()

const states = [
  'audit',
  'approved',
  'signed',
  'broadcast',
  'confirmed',
  'rejected',
  'failed',
  'refunded',
]

const stateTag: Record<string, string> = {
  audit: 'warning',
  approved: 'primary',
  signed: 'primary',
  broadcast: 'primary',
  confirmed: 'success',
  rejected: 'info',
  failed: 'danger',
  refunded: 'info',
}

const filters = reactive({ member: '', state: '' })
const rows = ref<WithdrawalRow[]>([])
const loading = ref(false)
const error = ref('')

useHead({ title: 'Withdrawals' })

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const params = new URLSearchParams()
    if (filters.member.trim() !== '') {
      params.set('member', filters.member.trim())
    }
    if (filters.state !== '') {
      params.set('state', filters.state)
    }
    const query = params.toString()
    rows.value = await apiFetch<WithdrawalRow[]>(`/api/withdrawals${query === '' ? '' : `?${query}`}`)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'The withdrawal queue could not be loaded.'
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<style scoped lang="scss">
.withdrawals {
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

  .filters {
    margin-bottom: 12px;
  }

  .error {
    margin-bottom: 12px;
  }

  .id-link {
    font-family: var(--mono-font, monospace);
    font-size: 12px;
  }
}
</style>
