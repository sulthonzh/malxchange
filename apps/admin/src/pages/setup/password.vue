<template>
  <div class="auth-card">
    <h1 class="brand">malxchange admin</h1>
    <h2>Change your password</h2>
    <p class="hint">
      Your sign-in credential is one-time. Choose a new password before anything else — every
      signed-in session, including this one, is revoked when the password changes.
    </p>
    <el-alert v-if="formError" class="auth-error" type="error" :closable="false" :title="formError" />
    <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent="submit">
      <el-form-item label="Current password" prop="current_password">
        <el-input name="current_password" v-model="form.current_password" type="password" autocomplete="current-password" show-password />
      </el-form-item>
      <el-form-item label="New password" prop="new_password">
        <el-input name="new_password" v-model="form.new_password" type="password" autocomplete="new-password" show-password />
      </el-form-item>
      <el-form-item label="Confirm new password" prop="confirm_password">
        <el-input name="confirm_password" v-model="form.confirm_password" type="password" autocomplete="new-password" show-password />
      </el-form-item>
      <el-button class="submit" type="warning" native-type="submit" :loading="submitting">Change password</el-button>
    </el-form>
  </div>
</template>

<script setup lang="ts">
import type { FormInstance, FormRules } from 'element-plus'

// Forced first-login step one (issue #56): rotate the one-time
// credential. The api revokes every session on success and clears the
// cookie, so the client drops its mirror and returns to the sign-in
// page — the second login continues to TOTP enrollment.
definePageMeta({ layout: 'bare', middleware: 'employee' })

const { apiFetch } = useApi()
const { session } = useAdminSession()

const formRef = ref<FormInstance>()
const submitting = ref(false)
const formError = ref('')
const form = reactive({ current_password: '', new_password: '', confirm_password: '' })

const rules: FormRules = {
  current_password: [{ required: true, message: 'Enter your current password.', trigger: 'blur' }],
  new_password: [
    { required: true, message: 'Choose a new password of at least 8 characters.', trigger: 'blur' },
    { min: 8, message: 'Choose a password of at least 8 characters.', trigger: 'blur' },
  ],
  confirm_password: [
    { required: true, message: 'Repeat the new password.', trigger: 'blur' },
    {
      validator: (_rule, value, callback) => {
        if (value !== form.new_password) {
          callback(new Error('The passwords do not match.'))
        } else {
          callback()
        }
      },
      trigger: 'blur',
    },
  ],
}

useHead({ title: 'Change your password' })

async function submit(): Promise<void> {
  formError.value = ''
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) {
    return
  }
  submitting.value = true
  try {
    await apiFetch('/api/password', {
      method: 'POST',
      body: { current_password: form.current_password, new_password: form.new_password },
    })
    // Every session died with the rotation — including this one.
    session.value = null
    await navigateTo('/login?rotated=1')
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The password change failed. Try again.'
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

  .auth-error {
    margin-bottom: 12px;
  }

  .submit {
    width: 100%;
  }
}
</style>
