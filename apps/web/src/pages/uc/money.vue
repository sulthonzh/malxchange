<template>
  <div class="uc-assets">
    <h1>Assets</h1>
    <el-table :data="accounts" :row-key="(row: BalanceRow) => row.coin">
      <el-table-column prop="coin" label="Coin" width="120" />
      <el-table-column label="Available" min-width="140">
        <template #default="{ row }: { row: BalanceRow }">{{ formatMoneyEnUs(row.available, scaleOf(row.coin)) }}</template>
      </el-table-column>
      <el-table-column label="Frozen" min-width="140">
        <template #default="{ row }: { row: BalanceRow }">{{ formatMoneyEnUs(row.frozen, scaleOf(row.coin)) }}</template>
      </el-table-column>
      <el-table-column label="Total" min-width="140">
        <template #default="{ row }: { row: BalanceRow }">{{ totalOf(row) }}</template>
      </el-table-column>
    </el-table>
    <p class="uc-assets__actions">
      <NuxtLink to="/uc/recharge">Deposit</NuxtLink>
      <NuxtLink to="/uc/withdraw">Withdraw</NuxtLink>
    </p>
  </div>
</template>

<script setup lang="ts">
import { formatMoneyEnUs } from '~/utils/format'
import { parseDecimal } from '~/utils/money'

definePageMeta({ middleware: 'member' })

interface BalanceRow {
  coin: string
  available: string
  frozen: string
}

const { apiFetch } = useApi()

const accounts = ref<BalanceRow[]>([])
const scales = ref<Record<string, number>>({})

useHead({ title: 'Assets' })

function scaleOf(coin: string): number {
  return scales.value[coin] ?? 8
}

function totalOf(row: BalanceRow): string {
  try {
    return formatMoneyEnUs(parseDecimal(row.available).add(parseDecimal(row.frozen)).toString(), scaleOf(row.coin))
  } catch {
    return '—'
  }
}

onMounted(() => {
  void (async () => {
    try {
      const [rows, coins] = await Promise.all([
        apiFetch<BalanceRow[]>('/uc/wallet/accounts'),
        apiFetch<Array<{ name: string; scale: number }>>('/uc/wallet/coins'),
      ])
      accounts.value = rows
      for (const coin of coins) {
        scales.value[coin.name] = coin.scale
      }
    } catch {
      accounts.value = []
    }
  })()
})
</script>

<style scoped lang="scss">
.uc-assets {
  h1 {
    font-size: 20px;
    margin: 0 0 16px;
  }
}

.uc-assets__actions {
  display: flex;
  gap: 16px;
  margin-top: 16px;
}
</style>
