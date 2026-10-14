<template>
  <div class="auth-card">
    <h1 class="brand">malxchange admin</h1>
    <h2>Sign in</h2>
    <p class="hint">Employees sign in with email, password, and an authenticator code.</p>
    <el-alert
      v-if="rotated"
      class="rotated-note"
      type="success"
      :closable="false"
      title="Password changed. Sign in with your new password."
    />
    <el-alert v-if="formError" class="auth-error" type="error" :closable="false" :title="formError" />
    <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent="submit">
      <el-form-item label="Email" prop="email">
        <el-input name="email" v-model="form.email" autocomplete="username" placeholder="employee@exchange.example.com" />
      </el-form-item>
      <el-form-item label="Password" prop="password">
        <el-input name="password" v-model="form.password" type="password" autocomplete="current-password" show-password />
      </el-form-item>
      <el-form-item label="Authenticator code" prop="code">
        <el-input name="code" v-model="form.code" inputmode="numeric" placeholder="6-digit code — required once two-factor is enrolled" />
      </el-form-item>
      <el-button class="submit" type="warning" native-type="submit" :loading="submitting">Sign in</el-button>
    </el-form>
  </div>
</template>

<script setup lang="ts">
import type { FormInstance, FormRules } from 'element-plus'

import type { AdminSession, LoginResponse } from '~/composables/useAdminSession'
import { toAdminSession } from '~/composables/useAdminSession'
import { loginTarget } from '~/utils/sessionGate'

// The Employee sign-in (issue #56): email + password + the mandatory
// TOTP code once two-factor is bound. The one-time first-login
// credential authenticates with the password alone (the api demands no
// code for an unbound Employee), so the code field stays optional —
// the api's refusal names it when it is required. The session token
// never appears: the api answers over the httpOnly cookie.
definePageMeta({ layout: 'bare' })

const { apiFetch } = useApi()
const { session } = useAdminSession()
const route = useRoute()

const formRef = ref<FormInstance>()
const submitting = ref(false)
const formError = ref('')
const rotated = computed(() => route.query.rotated === '1')
const form = reactive({ email: '', password: '', code: '' })

const rules: FormRules = {
  email: [
    { required: true, message: 'Enter your email address.', trigger: 'blur' },
    { type: 'email', message: 'Enter a valid email address.', trigger: 'blur' },
  ],
  password: [{ required: true, message: 'Enter your password.', trigger: 'blur' }],
}

useHead({ title: 'Sign in' })

async function submit(): Promise<void> {
  formError.value = ''
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) {
    return
  }
  submitting.value = true
  try {
    const response = await apiFetch<LoginResponse>('/api/login', {
      method: 'POST',
      body: { email: form.email, password: form.password, code: form.code },
    })
    const next: AdminSession = toAdminSession(response)
    session.value = next
    await navigateTo(loginTarget(next))
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'Sign in failed. Try again.'
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped lang="scss">
.auth-card {
  width: 380px;
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

  .rotated-note {
    margin-bottom: 12px;
  }

  .auth-error {
    margin-bottom: 12px;
  }

  .submit {
    width: 100%;
  }
}
</style>
