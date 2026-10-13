<template>
  <div class="uc-deposit">
    <h1>Deposit</h1>
    <el-form :model="form" label-position="top">
      <el-form-item label="Coin">
        <el-select v-model="form.coin" placeholder="Choose a Coin" @change="loadAddress">
          <el-option v-for="coin in coins" :key="coin.name" :label="coin.name" :value="coin.name" />
        </el-select>
      </el-form-item>
    </el-form>
    <div v-if="address" class="uc-deposit__address">
      <p>Send only {{ form.coin }} to this address. Anything else is lost.</p>
      <code>{{ address.address }}</code>
    </div>
    <p v-if="addressError !== ''" class="uc-deposit__error" role="alert">{{ addressError }}</p>
    <h2>Detected deposits</h2>
    <el-table :data="deposits" :row-key="(row: MemberDeposit) => `${row.txid}:${row.vout}`">
      <el-table-column prop="coin" label="Coin" width="100" />
      <el-table-column prop="amount" label="Amount" min-width="130">
        <template #default="{ row }: { row: MemberDeposit }">{{ formatMoneyEnUs(row.amount, 8) }}</template>
      </el-table-column>
      <el-table-column label="Confirmations" width="140">
        <template #default="{ row }: { row: MemberDeposit }">{{ row.confirmations }} / {{ row.needed }}</template>
      </el-table-column>
      <el-table-column prop="status" label="Status" width="120" />
    </el-table>
    <p v-if="deposits.length === 0" class="uc-deposit__muted">No deposits yet.</p>
  </div>
</template>

<script setup lang="ts">
import { formatMoneyEnUs } from '~/utils/format'
import type { DepositAddress, MemberDeposit, WalletCoin } from '~/utils/funds'

definePageMeta({ middleware: 'member' })

const { apiFetch } = useApi()

const coins = ref<WalletCoin[]>([])
const form = reactive({ coin: '' })
const address = ref<DepositAddress | null>(null)
const addressError = ref('')
const deposits = ref<MemberDeposit[]>([])

useHead({ title: 'Deposit' })

async function loadAddress(): Promise<void> {
  addressError.value = ''
  address.value = null
  if (form.coin === '') {
    return
  }
  try {
    address.value = await apiFetch<DepositAddress>('/uc/wallet/deposit-address', {
      method: 'POST',
      body: { coin: form.coin },
    })
  } catch (error) {
    addressError.value = error instanceof Error ? error.message : 'The deposit address could not be loaded.'
  }
}

onMounted(() => {
  void (async () => {
    try {
      coins.value = await apiFetch<WalletCoin[]>('/uc/wallet/coins')
      deposits.value = await apiFetch<MemberDeposit[]>('/uc/wallet/deposits')
    } catch {
      deposits.value = []
    }
  })()
})
</script>

<style scoped lang="scss">
.uc-deposit {
  h1,
  h2 {
    font-size: 20px;
    margin: 0 0 16px;
  }

  h2 {
    font-size: 16px;
    margin-top: 24px;
  }
}

.uc-deposit__address code {
  display: block;
  padding: 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 4px;
  word-break: break-all;
}

.uc-deposit__error {
  color: #f15057;
  font-size: 14px;
}

.uc-deposit__muted {
  color: var(--text-secondary);
  font-size: 14px;
}
</style>
