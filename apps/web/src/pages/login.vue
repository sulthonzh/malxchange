<template>
  <AuthCard title="Log in">
    <el-form
      ref="formRef"
      :model="form"
      :rules="rules"
      label-position="top"
      @submit.prevent="submit"
    >
      <el-form-item prop="email">
        <el-input v-model="form.email" name="email" placeholder="Email address" autocomplete="username" />
      </el-form-item>
      <el-form-item prop="password">
        <el-input
          v-model="form.password"
          name="password"
          type="password"
          placeholder="Password"
          autocomplete="current-password"
          show-password
        />
      </el-form-item>
      <el-form-item prop="device">
        <el-input v-model="form.device" name="device" placeholder="Device name" />
      </el-form-item>

      <p class="forgot-link">
        <NuxtLink to="/findPwd">Forgot your password?</NuxtLink>
      </p>

      <el-alert
        v-if="formError !== ''"
        class="auth-error"
        type="error"
        :closable="false"
        :title="formError"
      />

      <el-button class="auth-submit" native-type="submit" :loading="submitting">
        Log in
      </el-button>
    </el-form>

    <div class="auth-links">
      <span>Don't have an account yet?</span>
      <NuxtLink to="/register" class="auth-links__action">Sign up</NuxtLink>
    </div>
  </AuthCard>
</template>

<script setup lang="ts">
import type { FormInstance, FormRules } from 'element-plus'

import type { AuthTicket } from '~/composables/useSession'
import { validateEmail } from '~/utils/authValidation'
import { deviceLabel } from '~/utils/device'
import { safeRedirectTarget } from '~/utils/redirect'

// Ported from the legacy Login page: the email + password form on the
// shared auth card. The legacy phone-country selector, Geetest captcha,
// and the redirect on an existing session are gone — email is the sole
// identity (ADR-0006), lockout replaces captcha, and nothing redirects
// implicitly. The device label feeds the per-device session the Member
// later sees in their session list.
const LOGIN_PATH = '/uc/login'

const { apiFetch } = useApi()
const { setSession } = useSession()
const route = useRoute()

const formRef = ref<FormInstance>()
const submitting = ref(false)
const formError = ref('')
const form = reactive({
  email: '',
  password: '',
  device: 'Web browser',
})

const rules: FormRules = {
  email: [
    { required: true, message: 'Enter your email address.', trigger: 'blur' },
    { validator: validateEmail, trigger: 'blur' },
  ],
  password: [{ required: true, message: 'Enter your password.', trigger: 'blur' }],
  device: [
    { required: true, message: 'Name this sign-in so you can recognize it in your session list.', trigger: 'blur' },
  ],
}

onMounted(() => {
  form.device = deviceLabel(navigator.userAgent)
})

useHead({ title: 'Log in' })

async function submit(): Promise<void> {
  formError.value = ''
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) {
    return
  }
  submitting.value = true
  try {
    const ticket = await apiFetch<AuthTicket>(LOGIN_PATH, {
      method: 'POST',
      body: { email: form.email, password: form.password, device: form.device },
    })
    setSession(ticket.token, ticket.member)
    // The member middleware wrote ?redirect=… when it bounced an
    // anonymous visitor here; only a same-site path is honored.
    await navigateTo(safeRedirectTarget(route.query.redirect))
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'Sign in failed. Try again.'
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped lang="scss">
.forgot-link {
  margin: 0 0 15px;
  text-align: right;
  font-size: 12px;

  a {
    color: var(--text-secondary);

    &:hover {
      color: var(--accent);
    }
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
