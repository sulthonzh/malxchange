<template>
  <div class="uc-record">
    <h1>Records</h1>
    <el-form :model="filter" label-position="top" class="uc-record__filter">
      <el-form-item label="Coin">
        <el-select v-model="filter.coin" placeholder="All Coins" clearable @change="reload">
          <el-option v-for="coin in coins" :key="coin" :label="coin" :value="coin" />
        </el-select>
      </el-form-item>
    </el-form>
    <el-table :data="entries" :row-key="(_, index) => index">
      <el-table-column prop="at" label="Time" min-width="160">
        <template #default="{ row }: { row: JournalEntry }">{{ formatDateTime(row.at) }}</template>
      </el-table-column>
      <el-table-column prop="coin" label="Coin" width="100" />
      <el-table-column prop="kind" label="Kind" width="130" />
      <el-table-column prop="amount" label="Amount" min-width="130">
        <template #default="{ row }: { row: JournalEntry }">{{ row.amount }}</template>
      </el-table-column>
      <el-table-column prop="reason" label="Reason" min-width="200" />
    </el-table>
    <p v-if="entries.length === 0" class="uc-record__muted">No records yet.</p>
    <p v-if="loadError !== ''" class="uc-record__error" role="alert">{{ loadError }}</p>
  </div>
</template>

<script setup lang="ts">
import { formatDateTime } from '~/utils/format'
import type { JournalEntry } from '~/utils/funds'

definePageMeta({ middleware: 'member' })

const { apiFetch } = useApi()

const coins = ref<string[]>([])
const entries = ref<JournalEntry[]>([])
const filter = reactive({ coin: '' })
const loadError = ref('')

useHead({ title: 'Records' })

async function reload(): Promise<void> {
  loadError.value = ''
  try {
    const query = filter.coin === '' ? '' : `?coin=${encodeURIComponent(filter.coin)}`
    entries.value = await apiFetch<JournalEntry[]>(`/uc/wallet/records${query}`)
  } catch (error) {
    entries.value = []
    loadError.value = error instanceof Error ? error.message : 'The records could not be loaded.'
  }
}

onMounted(() => {
  void (async () => {
    try {
      const rows = await apiFetch<Array<{ coin: string }>>('/uc/wallet/accounts')
      coins.value = [...new Set(rows.map((row) => row.coin))]
    } catch {
      coins.value = []
    }
    await reload()
  })()
})
</script>

<style scoped lang="scss">
.uc-record {
  h1 {
    font-size: 20px;
    margin: 0 0 16px;
  }
}

.uc-record__filter {
  max-width: 240px;
}

.uc-record__muted {
  color: var(--text-secondary);
  font-size: 14px;
}

.uc-record__error {
  color: #f15057;
  font-size: 14px;
}
</style>
