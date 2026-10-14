<template>
  <div class="auth-card">
    <h1 class="brand">malxchange admin</h1>
    <h2>Enroll two-factor authentication</h2>
    <p class="hint">
      Add the secret to your authenticator app, then confirm with the code it shows. Finish this
      step to unlock your session.
    </p>
    <el-alert v-if="formError" class="auth-error" type="error" :closable="false" :title="formError" />
    <el-skeleton v-if="enrolling" :rows="2" animated />
    <template v-else-if="enrollment">
      <div class="secret-block">
        <p class="label">Secret (shown exactly once)</p>
        <code class="secret">{{ enrollment.secret }}</code>
        <p class="label">otpauth URI</p>
        <code class="secret">{{ enrollment.otpauth_uri }}</code>
      </div>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent="submit">
        <el-form-item label="Authenticator code" prop="code">
          <el-input name="code" v-model="form.code" inputmode="numeric" placeholder="6-digit code" />
        </el-form-item>
        <el-button class="submit" type="warning" native-type="submit" :loading="submitting">Confirm</el-button>
      </el-form>
    </template>
  </div>
</template>

<script setup lang="ts">
import type { FormInstance, FormRules } from 'element-plus'

// Forced first-login step two (issue #56): TOTP enrollment. The
// staged secret is shown exactly once (internal/admin/setup.go); the
// first valid code activates the binding and the same restricted
// session unlocks into the shell — no second login.
definePageMeta({ layout: 'bare', middleware: 'employee' })

interface EnrollResponse {
  secret: string
  otpauth_uri: string
}

interface ConfirmResponse {
  totp_bound: boolean
  must_rotate: boolean
}

const { apiFetch } = useApi()
const { session } = useAdminSession()

const formRef = ref<FormInstance>()
const enrolling = ref(true)
const submitting = ref(false)
const formError = ref('')
const enrollment = ref<EnrollResponse | null>(null)
const form = reactive({ code: '' })

const rules: FormRules = {
  code: [
    { required: true, message: 'Enter the code your authenticator app shows.', trigger: 'blur' },
  ],
}

useHead({ title: 'Enroll two-factor authentication' })

onMounted(enroll)

async function enroll(): Promise<void> {
  formError.value = ''
  enrolling.value = true
  try {
    enrollment.value = await apiFetch<EnrollResponse>('/api/totp/enroll', { method: 'POST', body: {} })
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'Enrollment failed. Reload the page to try again.'
  } finally {
    enrolling.value = false
  }
}

async function submit(): Promise<void> {
  formError.value = ''
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) {
    return
  }
  submitting.value = true
  try {
    const response = await apiFetch<ConfirmResponse>('/api/totp/confirm', {
      method: 'POST',
      body: { code: form.code },
    })
    if (session.value) {
      session.value = {
        ...session.value,
        totpBound: response.totp_bound,
        mustRotate: response.must_rotate,
      }
    }
    // A confirmed binding finishes forced setup: home.
    await navigateTo('/')
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'Confirming the code failed. Try again.'
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped lang="scss">
.auth-card {
  width: 420px;
  margin: 0 auto;

  .brand {
    font-size: 20px;
    font-weight: 600;
    color: var(--accent);
    margin: 0 0 4px;
  }

  h2 {
    font-size: 16px;
    font-weight: 400;
    margin: 0 0 8px;
  }

  .hint {
    color: var(--text-secondary);
    font-size: 13px;
    margin: 0 0 16px;
  }

  .auth-error {
    margin-bottom: 12px;
  }

  .secret-block {
    background: var(--panel-bg);
    border: 1px solid var(--panel-border);
    border-radius: 6px;
    padding: 12px;
    margin-bottom: 16px;

    .label {
      color: var(--text-secondary);
      font-size: 12px;
      margin: 0 0 4px;

      & + .secret {
        margin-bottom: 10px;
      }
    }

    .secret {
      display: block;
      color: var(--accent);
      font-size: 13px;
      word-break: break-all;
    }
  }

  .submit {
    width: 100%;
  }
}
</style>
