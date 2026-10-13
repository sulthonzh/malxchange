<template>
  <div class="uc-withdraw">
    <h1>Withdraw</h1>

    <div v-if="gate !== null && !gate.allowed" class="uc-withdraw__gate" role="status">
      <p v-if="gate.kycStatus !== 'verified'">Verify your identity before withdrawing.</p>
      <p v-else-if="!gate.authenticatorBound">Bind an authenticator before withdrawing.</p>
      <p>
        <NuxtLink to="/uc">Open security settings</NuxtLink>
      </p>
    </div>

    <el-form :model="form" label-position="top" @submit.prevent="submit">
      <el-form-item label="Coin">
        <el-select v-model="form.coin" placeholder="Choose a Coin" @change="refreshPreview">
          <el-option
            v-for="coin in withdrawableCoins"
            :key="coin.name"
            :label="coin.name"
            :value="coin.name"
            :disabled="!coin.withdrawable"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="Address">
        <el-select v-model="form.address" placeholder="Choose a saved address or paste one below" clearable>
          <el-option v-for="saved in savedAddresses" :key="saved.id" :label="savedLabel(saved)" :value="saved.address" />
        </el-select>
        <el-input v-model="form.address" placeholder="Destination address" autocomplete="off" class="uc-withdraw__address-input" />
      </el-form-item>
      <el-form-item label="Amount">
        <el-input v-model="form.amount" inputmode="decimal" autocomplete="off" @input="refreshPreview" />
      </el-form-item>
      <p v-if="preview" class="uc-withdraw__preview">
        Fee {{ formatMoneyEnUs(preview.fee, coinScale) }} · You receive
        {{ formatMoneyEnUs(preview.net, coinScale) }} {{ form.coin }}.
      </p>
      <el-form-item label="Authenticator code">
        <el-input v-model="form.code" inputmode="numeric" autocomplete="one-time-code" />
      </el-form-item>
      <p v-if="formError !== ''" class="uc-withdraw__error" role="alert">{{ formError }}</p>
      <p v-if="submitted" class="uc-withdraw__done" role="status">
        Your withdrawal was submitted for review. Funds leave after approval.
      </p>
      <el-button native-type="submit" type="primary" :loading="submitting">Submit withdrawal</el-button>
    </el-form>

    <p class="uc-withdraw__addresses">
      <NuxtLink to="/uc/withdraw/address">Manage saved addresses</NuxtLink>
    </p>
  </div>
</template>

<script setup lang="ts">
import { formatMoneyEnUs } from '~/utils/format'
import type { WalletCoin, WithdrawAddress, WithdrawPreview } from '~/utils/funds'

definePageMeta({ middleware: 'member' })

interface GateState {
  allowed: boolean
  kycStatus: string
  authenticatorBound: boolean
}

const { apiFetch } = useApi()

const coins = ref<WalletCoin[]>([])
const savedAddresses = ref<WithdrawAddress[]>([])
const form = reactive({ coin: '', address: '', amount: '', code: '' })
const preview = ref<WithdrawPreview | null>(null)
const gate = ref<GateState | null>(null)
const formError = ref('')
const submitting = ref(false)
const submitted = ref(false)

useHead({ title: 'Withdraw' })

const withdrawableCoins = computed(() => coins.value)
const coinScale = computed(() => coins.value.find((c) => c.name === form.coin)?.scale ?? 8)

function savedLabel(saved: WithdrawAddress): string {
  return saved.label === '' ? `${saved.coin} · ${saved.address}` : `${saved.label} (${saved.coin})`
}

async function refreshPreview(): Promise<void> {
  preview.value = null
  if (form.coin === '' || form.amount.trim() === '') {
    return
  }
  try {
    preview.value = await apiFetch<WithdrawPreview>(
      `/uc/wallet/withdraw-fee?coin=${encodeURIComponent(form.coin)}&amount=${encodeURIComponent(form.amount.trim())}`,
    )
  } catch {
    preview.value = null
  }
}

async function submit(): Promise<void> {
  formError.value = ''
  submitted.value = false
  if (form.coin === '' || form.address.trim() === '' || form.amount.trim() === '' || form.code.trim() === '') {
    formError.value = 'Choose a Coin, an address, an amount, and your authenticator code.'
    return
  }
  submitting.value = true
  try {
    await apiFetch('/uc/wallet/withdraw', {
      method: 'POST',
      body: { coin: form.coin, address: form.address.trim(), amount: form.amount.trim(), code: form.code.trim() },
    })
    submitted.value = true
    form.amount = ''
    form.code = ''
    preview.value = null
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The withdrawal could not be submitted.'
  } finally {
    submitting.value = false
  }
}

onMounted(() => {
  void (async () => {
    try {
      coins.value = await apiFetch<WalletCoin[]>('/uc/wallet/coins')
      savedAddresses.value = await apiFetch<WithdrawAddress[]>('/uc/withdraw-addresses')
      const summary = await apiFetch<{ kycStatus: string; authenticatorBound: boolean }>('/uc/security')
      gate.value = {
        allowed: summary.kycStatus === 'verified' && summary.authenticatorBound,
        kycStatus: summary.kycStatus,
        authenticatorBound: summary.authenticatorBound,
      }
    } catch {
      gate.value = null
    }
  })()
})
</script>

<style scoped lang="scss">
.uc-withdraw {
  h1 {
    font-size: 20px;
    margin: 0 0 16px;
  }
}

.uc-withdraw__gate {
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 12px;
  margin-bottom: 16px;
  font-size: 14px;
}

.uc-withdraw__preview {
  font-size: 14px;
  color: var(--text-secondary);
}

.uc-withdraw__error {
  color: #f15057;
  font-size: 14px;
}

.uc-withdraw__done {
  color: #00b275;
  font-size: 14px;
}

.uc-withdraw__address-input {
  margin-top: 8px;
}

.uc-withdraw__addresses {
  margin-top: 16px;
  font-size: 14px;
}
</style>
