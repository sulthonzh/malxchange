<template>
  <AuthCard title="Sign up">
    <el-form
      ref="formRef"
      :model="form"
      :rules="rules"
      label-position="top"
      @submit.prevent="submit"
    >
      <el-form-item prop="email">
        <el-input v-model="form.email" name="email" placeholder="Email address" autocomplete="email" />
      </el-form-item>
      <el-form-item prop="code" class="code-item">
        <el-input v-model="form.code" name="code" placeholder="6-digit code" maxlength="6" />
        <button
          type="button"
          class="send-code"
          :disabled="resendRemaining > 0 || sendingCode"
          @click="sendCode"
        >
          Send code
        </button>
      </el-form-item>
      <el-form-item prop="password">
        <el-input
          v-model="form.password"
          name="password"
          type="password"
          placeholder="Password"
          autocomplete="new-password"
          show-password
        />
      </el-form-item>
      <el-form-item prop="confirm">
        <el-input
          v-model="form.confirm"
          name="confirm"
          type="password"
          placeholder="Confirm password"
          autocomplete="new-password"
          show-password
        />
      </el-form-item>

      <el-alert
        v-if="formError !== ''"
        class="auth-error"
        type="error"
        :closable="false"
        :title="formError"
      />
      <p v-if="codeSentTo !== ''" class="auth-status auth-status--ok">
        We sent a 6-digit verification code to {{ codeSentTo }}.
      </p>
      <p v-if="resendRemaining > 0" class="auth-status auth-status--wait">
        You can request a new code in {{ resendRemaining }} seconds.
      </p>

      <el-button class="auth-submit" native-type="submit" :loading="submitting">
        Sign up
      </el-button>
    </el-form>

    <div class="auth-links">
      <span>Already have an account?</span>
      <NuxtLink to="/login" class="auth-links__action">Log in</NuxtLink>
    </div>
  </AuthCard>
</template>

<script setup lang="ts">
import type { FormInstance, FormRules } from 'element-plus'

import { ApiError } from '~/composables/useApi'
import type { AuthTicket } from '~/composables/useSession'
import { confirmPasswordMatches, validateEmail } from '~/utils/authValidation'
import { safeRedirectTarget } from '~/utils/redirect'

// Ported from the legacy Register page: email → verification code →
// password → signed in. The legacy phone-country selector, captcha, and
// promotion code are gone; email is the sole identity and the code
// proves control of it (ADR-0006). A successful registration answers
// with a session, so the Member lands signed in.
const REGISTER_CODE_PATH = '/uc/register/code'
const REGISTER_PATH = '/uc/register'

const { apiFetch } = useApi()
const { setSession } = useSession()
const route = useRoute()
const { remaining: resendRemaining, start: startCooldown } = useCooldown()

const formRef = ref<FormInstance>()
const submitting = ref(false)
const sendingCode = ref(false)
const formError = ref('')
const codeSentTo = ref('')
const form = reactive({
  email: '',
  code: '',
  password: '',
  confirm: '',
})

const rules: FormRules = {
  email: [
    { required: true, message: 'Enter your email address.', trigger: 'blur' },
    { validator: validateEmail, trigger: 'blur' },
  ],
  code: [
    { required: true, message: 'Enter the 6-digit code from your email.', trigger: 'blur' },
    { pattern: /^\d{6}$/, message: 'Enter the 6-digit code from your email.', trigger: 'blur' },
  ],
  password: [
    { required: true, message: 'Choose a password.', trigger: 'blur' },
    { min: 8, message: 'Choose a password of at least 8 characters.', trigger: 'blur' },
  ],
  confirm: [{ validator: confirmPasswordMatches(() => form.password), trigger: 'blur' }],
}

useHead({ title: 'Sign up' })

async function sendCode(): Promise<void> {
  formError.value = ''
  const valid = await formRef.value?.validateField('email').catch(() => false)
  if (!valid) {
    return
  }
  sendingCode.value = true
  try {
    await apiFetch(REGISTER_CODE_PATH, { method: 'POST', body: { email: form.email } })
    codeSentTo.value = form.email
    startCooldown()
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The code could not be sent. Try again.'
    // A 429 means the api already sent one moments ago — wait it out.
    if (error instanceof ApiError && error.status === 429) {
      startCooldown()
    }
  } finally {
    sendingCode.value = false
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
    const ticket = await apiFetch<AuthTicket>(REGISTER_PATH, {
      method: 'POST',
      body: { email: form.email, code: form.code, password: form.password },
    })
    setSession(ticket.token, ticket.member)
    await navigateTo(safeRedirectTarget(route.query.redirect))
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'Sign up failed. Try again.'
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped lang="scss">
.code-item {
  :deep(.el-input) {
    padding-right: 38%;
  }
}

.auth-links {
  margin-top: 15px;
  display: flex;
  justify-content: space-between;

  &__action {
    color: var(--accent);
  }
}
</style>
