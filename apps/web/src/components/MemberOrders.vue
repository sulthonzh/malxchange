<template>
  <div class="uc-entrust">
    <h1>{{ scope === 'current' ? 'Current orders' : 'History orders' }}</h1>
    <el-table :data="orders" :row-key="(row: MemberOrder) => row.orderId">
      <el-table-column prop="market" label="Market" width="120" />
      <el-table-column prop="side" label="Side" width="80" />
      <el-table-column prop="price" label="Price" min-width="110">
        <template #default="{ row }: { row: MemberOrder }">
          {{ row.price === undefined || row.price === '' ? 'market' : row.price }}
        </template>
      </el-table-column>
      <el-table-column prop="amount" label="Amount" min-width="120" />
      <el-table-column prop="filledAmount" label="Filled" min-width="120" />
      <el-table-column prop="turnover" label="Turnover" min-width="120" />
      <el-table-column prop="status" label="Status" width="100" />
      <el-table-column v-if="scope === 'history'" label="Fees" min-width="160">
        <template #default="{ row }: { row: MemberOrder }">
          {{ feesOf(row) }}
        </template>
      </el-table-column>
      <el-table-column v-if="scope === 'current'" label="Action" width="100">
        <template #default="{ row }: { row: MemberOrder }">
          <el-button link type="danger" size="small" :loading="canceling === row.orderId" @click="cancel(row.orderId)">
            Cancel
          </el-button>
        </template>
      </el-table-column>
    </el-table>
    <p v-if="orders.length === 0 && loadError === ''" class="uc-entrust__muted">
      {{ scope === 'current' ? 'No open orders.' : 'No order history yet.' }}
    </p>
    <p v-if="loadError !== ''" class="uc-entrust__error" role="alert">{{ loadError }}</p>
  </div>
</template>

<script setup lang="ts">
import type { MemberOrder } from '~/utils/exchange'

definePageMeta({ middleware: 'member' })

const props = defineProps<{ scope: 'current' | 'history' }>()

const { apiFetch } = useApi()

const orders = ref<MemberOrder[]>([])
const loadError = ref('')
const canceling = ref('')

useHead({ title: () => (props.scope === 'current' ? 'Current orders' : 'History orders') })

function feesOf(order: MemberOrder): string {
  if (!order.fills || order.fills.length === 0) {
    return '—'
  }
  return order.fills.map((fill) => fill.fee).join(', ')
}

async function reload(): Promise<void> {
  loadError.value = ''
  try {
    orders.value = await apiFetch<MemberOrder[]>(`/uc/orders/${props.scope}`)
  } catch (error) {
    orders.value = []
    loadError.value = error instanceof Error ? error.message : 'The orders could not be loaded.'
  }
}

async function cancel(orderId: string): Promise<void> {
  canceling.value = orderId
  try {
    await apiFetch(`/uc/orders/${orderId}/cancel`, { method: 'POST' })
    await reload()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'The order could not be canceled.'
  } finally {
    canceling.value = ''
  }
}

onMounted(() => {
  void reload()
})
</script>

<style scoped lang="scss">
.uc-entrust {
  h1 {
    font-size: 20px;
    margin: 0 0 16px;
  }
}

.uc-entrust__muted {
  color: var(--text-secondary);
  font-size: 14px;
}

.uc-entrust__error {
  color: #f15057;
  font-size: 14px;
}
</style>
