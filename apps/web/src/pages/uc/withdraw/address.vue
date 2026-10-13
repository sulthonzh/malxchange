<template>
  <div class="uc-addresses">
    <h1>Withdraw addresses</h1>
    <el-form :model="form" label-position="top" @submit.prevent="add">
      <el-form-item label="Coin">
        <el-select v-model="form.coin" placeholder="Choose a Coin">
          <el-option v-for="coin in coins" :key="coin.name" :label="coin.name" :value="coin.name" />
        </el-select>
      </el-form-item>
      <el-form-item label="Address">
        <el-input v-model="form.address" autocomplete="off" />
      </el-form-item>
      <el-form-item label="Label">
        <el-input v-model="form.label" placeholder="For example: my cold wallet" autocomplete="off" />
      </el-form-item>
      <p v-if="formError !== ''" class="uc-addresses__error" role="alert">{{ formError }}</p>
      <el-button native-type="submit" type="primary" :loading="submitting">Add address</el-button>
    </el-form>
    <el-table :data="addresses" :row-key="(row: WithdrawAddress) => row.id" class="uc-addresses__table">
      <el-table-column prop="coin" label="Coin" width="110" />
      <el-table-column prop="address" label="Address" min-width="220" />
      <el-table-column prop="label" label="Label" min-width="140" />
      <el-table-column label="Action" width="100">
        <template #default="{ row }: { row: WithdrawAddress }">
          <el-button link type="danger" size="small" :loading="deleting === row.id" @click="remove(row.id)">
            Delete
          </el-button>
        </template>
      </el-table-column>
    </el-table>
    <p v-if="addresses.length === 0" class="uc-addresses__muted">No saved addresses yet.</p>
  </div>
</template>

<script setup lang="ts">
import type { WalletCoin, WithdrawAddress } from '~/utils/funds'

definePageMeta({ middleware: 'member' })

const { apiFetch } = useApi()

const coins = ref<WalletCoin[]>([])
const addresses = ref<WithdrawAddress[]>([])
const form = reactive({ coin: '', address: '', label: '' })
const formError = ref('')
const submitting = ref(false)
const deleting = ref('')

useHead({ title: 'Withdraw addresses' })

async function reload(): Promise<void> {
  try {
    addresses.value = await apiFetch<WithdrawAddress[]>('/uc/withdraw-addresses')
  } catch {
    addresses.value = []
  }
}

async function add(): Promise<void> {
  formError.value = ''
  if (form.coin === '' || form.address.trim() === '') {
    formError.value = 'Choose a Coin and enter the address.'
    return
  }
  submitting.value = true
  try {
    await apiFetch('/uc/withdraw-addresses', {
      method: 'POST',
      body: { coin: form.coin, address: form.address.trim(), label: form.label.trim() },
    })
    form.address = ''
    form.label = ''
    await reload()
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The address could not be added.'
  } finally {
    submitting.value = false
  }
}

async function remove(id: string): Promise<void> {
  deleting.value = id
  try {
    await apiFetch(`/uc/withdraw-addresses/${id}`, { method: 'DELETE' })
    await reload()
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The address could not be deleted.'
  } finally {
    deleting.value = ''
  }
}

onMounted(() => {
  void (async () => {
    try {
      coins.value = await apiFetch<WalletCoin[]>('/uc/wallet/coins')
    } catch {
      coins.value = []
    }
    await reload()
  })()
})
</script>

<style scoped lang="scss">
.uc-addresses {
  h1 {
    font-size: 20px;
    margin: 0 0 16px;
  }
}

.uc-addresses__table {
  margin-top: 16px;
}

.uc-addresses__error {
  color: #f15057;
  font-size: 14px;
}

.uc-addresses__muted {
  color: var(--text-secondary);
  font-size: 14px;
}
</style>
