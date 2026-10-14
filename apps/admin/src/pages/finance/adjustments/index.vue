<template>
  <section class="adjustments">
    <h1>Manual balance adjustment</h1>
    <p class="hint">
      A signed movement on a member's Ledger balance with a mandatory reason: support cases close
      as auditable Journal entries, never SQL edits. The adjustment lands with a fresh code from
      your authenticator app, and the balance before/after is written to the audit log.
    </p>

    <el-alert v-if="error" class="error" type="error" :closable="false" :title="error" />
    <el-alert v-if="success" class="success" type="success" :closable="false" :title="success" />

    <el-form
      ref="formRef"
      class="form"
      :model="form"
      :rules="rules"
      label-position="top"
      @submit.prevent="submit"
    >
      <el-form-item label="Member" prop="member">
        <el-input v-model="form.member" name="member" placeholder="Member id or email" />
      </el-form-item>
      <el-form-item label="Coin" prop="coin">
        <el-input v-model="form.coin" name="coin" placeholder="USDT" />
      </el-form-item>
      <el-form-item label="Amount (signed)" prop="amount">
        <el-input v-model="form.amount" name="amount" placeholder="e.g. 25.50 or -10" />
        <span class="field-hint">Positive credits the member, negative debits; zero is refused.</span>
      </el-form-item>
      <el-form-item label="Reason" prop="reason">
        <el-input
          v-model="form.reason"
          name="reason"
          type="textarea"
          :rows="3"
          placeholder="Why this movement happens — it enters the Journal and the audit log verbatim"
        />
      </el-form-item>
      <el-button class="submit" type="primary" native-type="submit" :loading="submitting">
        Continue to confirmation
      </el-button>
    </el-form>

    <!-- Step-up (ADR-0012): a manual balance adjustment demands a fresh
         authenticator code. The idempotency key is minted per
         submission, so a retried request can never move money twice. -->
    <el-dialog v-model="confirmVisible" title="Confirm adjustment" width="420px">
      <p class="dialog-hint">
        {{ form.member }} · {{ form.coin }} · {{ form.amount }}
      </p>
      <p class="dialog-hint">Enter a fresh code from your authenticator app to apply this movement.</p>
      <el-input
        v-model="stepUpCode"
        name="code"
        inputmode="numeric"
        placeholder="6-digit code"
        maxlength="6"
      />
      <template #footer>
        <el-button @click="confirmVisible = false">Cancel</el-button>
        <el-button type="primary" :loading="submitting" @click="confirmAdjustment">Confirm</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import type { FormInstance, FormRules } from 'element-plus'

// The manual balance adjustment form (issue #59): signed amount +
// mandatory reason, submitted to the wallet through admin-api with
// Step-up. The wallet appends one Journal adjustment entry (idem key =
// this submission's key) and moves the Ledger in the same transaction.
definePageMeta({ middleware: 'employee' })

const { apiFetch } = useApi()

const formRef = ref<FormInstance>()
const submitting = ref(false)
const error = ref('')
const success = ref('')
const confirmVisible = ref(false)
const stepUpCode = ref('')
const form = reactive({ member: '', coin: '', amount: '', reason: '' })

const rules: FormRules = {
  member: [{ required: true, message: 'Enter the member to adjust.', trigger: 'blur' }],
  coin: [{ required: true, message: 'Enter the Coin.', trigger: 'blur' }],
  amount: [
    { required: true, message: 'Enter a signed amount.', trigger: 'blur' },
    {
      validator: (_rule, value, callback) => {
        if (value === '' || Number.isNaN(Number(value))) {
          callback(new Error('Enter a number, e.g. 25.50 or -10.'))
        } else if (Number(value) === 0) {
          callback(new Error('A zero adjustment moves nothing; enter a non-zero amount.'))
        } else {
          callback()
        }
      },
      trigger: 'blur',
    },
  ],
  reason: [{ required: true, message: 'A reason is mandatory.', trigger: 'blur' }],
}

useHead({ title: 'Manual balance adjustment' })

function mintKey(): string {
  // Per-submission idempotency key: the wallet Journal dedupes on it,
  // so a network retry of THIS submission is a no-op while a fresh
  // form submission adjusts again.
  if (typeof crypto !== 'undefined' && crypto.randomUUID) {
    return crypto.randomUUID()
  }
  return `adj-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

async function submit(): Promise<void> {
  error.value = ''
  success.value = ''
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) {
    return
  }
  stepUpCode.value = ''
  confirmVisible.value = true
}

async function confirmAdjustment(): Promise<void> {
  if (stepUpCode.value.trim() === '') {
    error.value = 'Enter a fresh authenticator code.'
    return
  }
  submitting.value = true
  try {
    const result = await apiFetch<{ ok: boolean; applied: boolean }>('/api/adjustments', {
      method: 'POST',
      body: {
        member: form.member.trim(),
        coin: form.coin.trim(),
        amount: form.amount.trim(),
        reason: form.reason,
        key: mintKey(),
        code: stepUpCode.value.trim(),
      },
    })
    confirmVisible.value = false
    success.value = result.applied
      ? `Adjustment applied: ${form.member} · ${form.coin} · ${form.amount}. The Journal entry and the audit row are written.`
      : 'This submission was already applied — no second movement was made.'
    form.member = ''
    form.coin = ''
    form.amount = ''
    form.reason = ''
    formRef.value?.resetFields()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'The adjustment failed. Try again.'
    confirmVisible.value = false
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped lang="scss">
.adjustments {
  h1 {
    font-size: 20px;
    font-weight: 600;
    margin: 0 0 4px;
  }

  .hint {
    color: var(--text-secondary);
    font-size: 13px;
    margin: 0 0 16px;
    max-width: 720px;
  }

  .form {
    max-width: 480px;
  }

  .error {
    margin-bottom: 12px;
  }

  .success {
    margin-bottom: 12px;
  }

  .field-hint {
    display: block;
    color: var(--text-muted);
    font-size: 12px;
    line-height: 1.4;
    margin-top: 4px;
  }

  .submit {
    margin-top: 4px;
  }

  .dialog-hint {
    color: var(--text-secondary);
    font-size: 13px;
    margin: 0 0 12px;
  }
}
</style>
