<template>
  <AuthCard title="Reset your password">
    <template v-if="!resetDone">
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
            placeholder="New password"
            autocomplete="new-password"
            show-password
          />
        </el-form-item>
        <el-form-item prop="confirm">
          <el-input
            v-model="form.confirm"
            name="confirm"
            type="password"
            placeholder="Confirm new password"
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
          Reset password
        </el-button>
      </el-form>

      <div class="auth-links">
        <span>Remembered your password?</span>
        <NuxtLink to="/login" class="auth-links__action">Sign in</NuxtLink>
      </div>
    </template>

    <div v-else class="reset-done">
      <p class="auth-status auth-status--ok">Your password has been reset.</p>
      <p class="auth-status">
        Every active session has been signed out, so sign in on each device again.
      </p>
      <el-button class="auth-submit" @click="goSignIn">
        Sign in
      </el-button>
    </div>
  </AuthCard>
</template>

<script setup lang="ts">
import type { FormInstance, FormRules } from 'element-plus'

import { ApiError } from '~/composables/useApi'
import { confirmPasswordMatches, validateEmail } from '~/utils/authValidation'

// Ported from the legacy FindPwd page: email → verification code → new
// password. The filename and route keep the legacy URL /findPwd (the
// front's link vocabulary); GLOSSARY's naming bans apply to new
// identifiers, not this pinned path. The reset flow revokes every
// active session (ADR-0006 sensitive-flow matrix), so the page ends on
// that notice instead of a silent jump to the trading page. Every
// request goes through the
// useForgotPassword seam — see its header for the routes ticket #39 is
// expected to add.
const { requestCode, resetPassword } = useForgotPassword()
const { remaining: resendRemaining, start: startCooldown } = useCooldown()

const formRef = ref<FormInstance>()
const submitting = ref(false)
const sendingCode = ref(false)
const formError = ref('')
const codeSentTo = ref('')
const resetDone = ref(false)
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

useHead({ title: 'Reset your password' })

async function sendCode(): Promise<void> {
  formError.value = ''
  const valid = await formRef.value?.validateField('email').catch(() => false)
  if (!valid) {
    return
  }
  sendingCode.value = true
  try {
    await requestCode(form.email)
    codeSentTo.value = form.email
    startCooldown()
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The code could not be sent. Try again.'
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
    await resetPassword(form.email, form.code, form.password)
    resetDone.value = true
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The password could not be reset. Try again.'
  } finally {
    submitting.value = false
  }
}

function goSignIn(): void {
  void navigateTo('/login')
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

.reset-done {
  .auth-status {
    font-size: 14px;
    line-height: 22px;
  }
}
</style>
