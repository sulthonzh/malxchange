<template>
  <div class="uc-security">
    <h1>Security</h1>

    <el-card class="uc-security__card" header="Account">
      <dl class="uc-security__facts">
        <div>
          <dt>Email</dt>
          <dd>{{ summary?.email ?? '—' }}</dd>
        </div>
        <div>
          <dt>Phone</dt>
          <dd>{{ summary?.phone === '' ? 'Not bound' : (summary?.phone ?? '—') }}</dd>
        </div>
      </dl>
    </el-card>

    <el-card class="uc-security__card" header="Change password">
      <el-form :model="passwordForm" label-position="top" @submit.prevent="changePassword">
        <el-form-item label="Current password">
          <el-input v-model="passwordForm.oldPassword" type="password" autocomplete="current-password" />
        </el-form-item>
        <SecurityCodeButton action="/uc/password/change/code" @sent="notice('The code was sent to your email.')" />
        <el-form-item label="Email code">
          <el-input v-model="passwordForm.code" inputmode="numeric" autocomplete="one-time-code" />
        </el-form-item>
        <el-form-item label="New password">
          <el-input v-model="passwordForm.newPassword" type="password" autocomplete="new-password" />
        </el-form-item>
        <p v-if="formError !== ''" class="uc-security__error" role="alert">{{ formError }}</p>
        <p v-if="noticeText !== ''" class="uc-security__notice" role="status">{{ noticeText }}</p>
        <el-button native-type="submit" type="primary" :loading="submitting">Change password</el-button>
      </el-form>
      <p class="uc-security__hint">Changing your password signs you out on every device.</p>
    </el-card>

    <el-card class="uc-security__card" header="Change email">
      <el-form :model="emailForm" label-position="top" @submit.prevent="requestEmailChange">
        <el-form-item label="Login password">
          <el-input v-model="emailForm.password" type="password" autocomplete="current-password" />
        </el-form-item>
        <el-form-item label="New email address">
          <el-input v-model="emailForm.newEmail" type="email" autocomplete="email" />
        </el-form-item>
        <p v-if="emailError !== ''" class="uc-security__error" role="alert">{{ emailError }}</p>
        <el-button native-type="submit" type="primary" :loading="emailSubmitting">Send codes</el-button>
      </el-form>
      <el-form v-if="emailCodesSent" :model="emailConfirm" label-position="top" @submit.prevent="confirmEmailChange">
        <p class="uc-security__hint">
          Codes were sent to your current address and to the new one. Enter both to finish the change.
        </p>
        <el-form-item label="Code to your current address">
          <el-input v-model="emailConfirm.oldCode" inputmode="numeric" autocomplete="one-time-code" />
        </el-form-item>
        <el-form-item label="Code to the new address">
          <el-input v-model="emailConfirm.newCode" inputmode="numeric" autocomplete="one-time-code" />
        </el-form-item>
        <p v-if="emailError !== ''" class="uc-security__error" role="alert">{{ emailError }}</p>
        <el-button native-type="submit" type="primary" :loading="emailSubmitting">Confirm email change</el-button>
      </el-form>
    </el-card>

    <el-card class="uc-security__card" header="Bind phone">
      <p v-if="!phoneAvailable" class="uc-security__hint">
        Phone codes are unavailable right now. Your account works fully without a phone.
      </p>
      <el-form v-else :model="phoneForm" label-position="top" @submit.prevent="bindPhone">
        <el-form-item label="Phone number">
          <el-input v-model="phoneForm.phone" placeholder="+15551234567" autocomplete="tel" />
        </el-form-item>
        <SecurityCodeButton action="/uc/phone/code" :code-body="{ phone: phoneForm.phone }" @sent="notice('The code was sent to your phone.')" />
        <el-form-item label="Phone code">
          <el-input v-model="phoneForm.code" inputmode="numeric" autocomplete="one-time-code" />
        </el-form-item>
        <el-form-item label="Login password">
          <el-input v-model="phoneForm.password" type="password" autocomplete="current-password" />
        </el-form-item>
        <p v-if="phoneError !== ''" class="uc-security__error" role="alert">{{ phoneError }}</p>
        <el-button native-type="submit" type="primary" :loading="phoneSubmitting">Bind phone</el-button>
      </el-form>
    </el-card>

    <el-card class="uc-security__card" header="Authenticator">
      <template v-if="!summary?.authenticatorBound">
        <p class="uc-security__hint">
          Bind an authenticator app before your first withdrawal. Withdrawals stay blocked until one is bound.
        </p>
        <SecurityCodeButton action="/uc/authenticator/code" @sent="notice('The code was sent to your email.')" />
        <el-form :model="totpBegin" label-position="top" @submit.prevent="beginTotp">
          <el-form-item label="Email code">
            <el-input v-model="totpBegin.code" inputmode="numeric" autocomplete="one-time-code" />
          </el-form-item>
          <el-button native-type="submit" type="primary" :loading="totpSubmitting">Show setup secret</el-button>
        </el-form>
        <div v-if="totpSecret !== ''" class="uc-security__secret">
          <p>
            Enter this secret in your authenticator app, then prove it with a live code. The secret is shown
            exactly once.
          </p>
          <code>{{ totpSecret }}</code>
          <el-form :model="totpConfirm" label-position="top" @submit.prevent="confirmTotp">
            <el-form-item label="Live authenticator code">
              <el-input v-model="totpConfirm.code" inputmode="numeric" autocomplete="one-time-code" />
            </el-form-item>
            <el-button native-type="submit" type="primary" :loading="totpSubmitting">Bind authenticator</el-button>
          </el-form>
        </div>
      </template>
      <template v-else>
        <p class="uc-security__hint">An authenticator is bound. Unbinding needs your login password and a live code.</p>
        <el-form :model="totpUnbind" label-position="top" @submit.prevent="unbindTotp">
          <el-form-item label="Login password">
            <el-input v-model="totpUnbind.password" type="password" autocomplete="current-password" />
          </el-form-item>
          <el-form-item label="Live authenticator code">
            <el-input v-model="totpUnbind.code" inputmode="numeric" autocomplete="one-time-code" />
          </el-form-item>
          <p v-if="totpError !== ''" class="uc-security__error" role="alert">{{ totpError }}</p>
          <el-button native-type="submit" type="danger" :loading="totpSubmitting">Unbind authenticator</el-button>
        </el-form>
      </template>
      <p v-if="totpError !== ''" class="uc-security__error" role="alert">{{ totpError }}</p>
    </el-card>

    <el-card class="uc-security__card" header="Identity check (KYC)">
      <KycPanel :status="summary?.kycStatus ?? 'unverified'" @submitted="reload" />
    </el-card>

    <el-card class="uc-security__card" header="Active sessions">
      <el-table :data="sessions" :row-key="(row: SessionRow) => row.id">
        <el-table-column prop="device" label="Device" min-width="140" />
        <el-table-column prop="lastUsed" label="Last used" min-width="160">
          <template #default="{ row }: { row: SessionRow }">{{ formatDateTime(row.lastUsed) }}</template>
        </el-table-column>
        <el-table-column label="This device" width="110">
          <template #default="{ row }: { row: SessionRow }">{{ row.current ? 'Yes' : '' }}</template>
        </el-table-column>
      </el-table>
      <el-button type="danger" :loading="revoking" class="uc-security__revoke" @click="revokeAll">
        Sign out everywhere
      </el-button>
      <p class="uc-security__hint">Useful when a device is lost: every session dies, including this one.</p>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { formatDateTime } from '~/utils/format'

definePageMeta({ middleware: 'member' })

interface SecuritySummary {
  email: string
  phone: string
  authenticatorBound: boolean
  kycStatus: string
}

interface SessionRow {
  id: string
  device: string
  createdAt: string
  lastUsed: string
  current: boolean
}

const { apiFetch } = useApi()
const { setSession } = useSession()

const summary = ref<SecuritySummary | null>(null)
const sessions = ref<SessionRow[]>([])
const formError = ref('')
const noticeText = ref('')
const submitting = ref(false)
const emailError = ref('')
const emailSubmitting = ref(false)
const emailCodesSent = ref(false)
const phoneAvailable = ref(true)
const phoneError = ref('')
const phoneSubmitting = ref(false)
const totpError = ref('')
const totpSubmitting = ref(false)
const totpSecret = ref('')
const revoking = ref(false)

const passwordForm = reactive({ oldPassword: '', code: '', newPassword: '' })
const emailForm = reactive({ password: '', newEmail: '' })
const emailConfirm = reactive({ oldCode: '', newCode: '' })
const phoneForm = reactive({ phone: '', code: '', password: '' })
const totpBegin = reactive({ code: '' })
const totpConfirm = reactive({ code: '' })
const totpUnbind = reactive({ password: '', code: '' })

useHead({ title: 'Security' })

function notice(message: string): void {
  noticeText.value = message
  formError.value = ''
}

async function reload(): Promise<void> {
  try {
    summary.value = await apiFetch<SecuritySummary>('/uc/security')
    sessions.value = await apiFetch<SessionRow[]>('/uc/sessions')
  } catch {
    summary.value = null
  }
}

async function changePassword(): Promise<void> {
  formError.value = ''
  submitting.value = true
  try {
    await apiFetch('/uc/password/change', {
      method: 'POST',
      body: { oldPassword: passwordForm.oldPassword, code: passwordForm.code, newPassword: passwordForm.newPassword },
    })
    notice('Your password was changed. Every device was signed out — sign in again.')
    passwordForm.oldPassword = ''
    passwordForm.code = ''
    passwordForm.newPassword = ''
    await reload()
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The password could not be changed.'
  } finally {
    submitting.value = false
  }
}

async function requestEmailChange(): Promise<void> {
  emailError.value = ''
  emailSubmitting.value = true
  try {
    await apiFetch('/uc/email/change/request', {
      method: 'POST',
      body: { password: emailForm.password, newEmail: emailForm.newEmail },
    })
    emailCodesSent.value = true
  } catch (error) {
    emailError.value = error instanceof Error ? error.message : 'The email change could not be started.'
  } finally {
    emailSubmitting.value = false
  }
}

async function confirmEmailChange(): Promise<void> {
  emailError.value = ''
  emailSubmitting.value = true
  try {
    const updated = await apiFetch<{ email: string }>('/uc/email/change/confirm', {
      method: 'POST',
      body: { newEmail: emailForm.newEmail, oldCode: emailConfirm.oldCode, newCode: emailConfirm.newCode },
    })
    emailCodesSent.value = false
    emailForm.password = ''
    emailForm.newEmail = ''
    emailConfirm.oldCode = ''
    emailConfirm.newCode = ''
    notice(`Your email address is now ${updated.email}.`)
    await reload()
  } catch (error) {
    emailError.value = error instanceof Error ? error.message : 'The email change could not be confirmed.'
  } finally {
    emailSubmitting.value = false
  }
}

async function bindPhone(): Promise<void> {
  phoneError.value = ''
  phoneSubmitting.value = true
  try {
    await apiFetch('/uc/phone/bind', {
      method: 'POST',
      body: { phone: phoneForm.phone, password: phoneForm.password, code: phoneForm.code },
    })
    phoneForm.code = ''
    phoneForm.password = ''
    notice(`Your phone ${phoneForm.phone} is bound.`)
    await reload()
  } catch (error) {
    phoneError.value = error instanceof Error ? error.message : 'The phone could not be bound.'
  } finally {
    phoneSubmitting.value = false
  }
}

async function beginTotp(): Promise<void> {
  totpError.value = ''
  totpSubmitting.value = true
  try {
    const begun = await apiFetch<{ secret: string; otpauthUri: string }>('/uc/authenticator/bind/begin', {
      method: 'POST',
      body: { code: totpBegin.code },
    })
    totpSecret.value = begun.secret
  } catch (error) {
    totpError.value = error instanceof Error ? error.message : 'The authenticator setup could not start.'
  } finally {
    totpSubmitting.value = false
  }
}

async function confirmTotp(): Promise<void> {
  totpError.value = ''
  totpSubmitting.value = true
  try {
    await apiFetch('/uc/authenticator/bind/confirm', { method: 'POST', body: { code: totpConfirm.code } })
    totpSecret.value = ''
    totpBegin.code = ''
    totpConfirm.code = ''
    notice('Your authenticator is bound. Withdrawals are unlocked.')
    await reload()
  } catch (error) {
    totpError.value = error instanceof Error ? error.message : 'The authenticator could not be bound.'
  } finally {
    totpSubmitting.value = false
  }
}

async function unbindTotp(): Promise<void> {
  totpError.value = ''
  totpSubmitting.value = true
  try {
    await apiFetch('/uc/authenticator/unbind', {
      method: 'POST',
      body: { password: totpUnbind.password, code: totpUnbind.code },
    })
    totpUnbind.password = ''
    totpUnbind.code = ''
    notice('Your authenticator was unbound. Withdrawals are blocked until you bind a new one.')
    await reload()
  } catch (error) {
    totpError.value = error instanceof Error ? error.message : 'The authenticator could not be unbound.'
  } finally {
    totpSubmitting.value = false
  }
}

async function revokeAll(): Promise<void> {
  revoking.value = true
  try {
    await apiFetch('/uc/sessions/revoke-all', { method: 'POST' })
    setSession('', { id: '', email: '', status: '', createdAt: '' })
    await navigateTo('/login')
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The sessions could not be revoked.'
  } finally {
    revoking.value = false
  }
}

onMounted(() => {
  void (async () => {
    await reload()
    // Phone bind shows only when a phone channel is active (409 =
    // none active, and the account works fully without a phone).
    try {
      await apiFetch('/uc/phone/code', { method: 'POST', body: { phone: '+10000000000' } })
    } catch (error) {
      if (error instanceof Error && error.message.includes('unavailable')) {
        phoneAvailable.value = false
      }
    }
  })()
})
</script>

<style scoped lang="scss">
.uc-security {
  h1 {
    font-size: 20px;
    margin: 0 0 16px;
  }
}

.uc-security__card {
  margin-bottom: 16px;
}

.uc-security__facts {
  margin: 0;

  div {
    display: flex;
    gap: 12px;
    margin-bottom: 8px;
  }

  dt {
    color: var(--text-secondary);
    min-width: 90px;
  }

  dd {
    margin: 0;
  }
}

.uc-security__error {
  color: #f15057;
  font-size: 14px;
}

.uc-security__notice {
  color: #00b275;
  font-size: 14px;
}

.uc-security__hint {
  color: var(--text-secondary);
  font-size: 13px;
}

.uc-security__secret code {
  display: block;
  padding: 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 4px;
  font-size: 16px;
  letter-spacing: 1px;
  margin-bottom: 12px;
  word-break: break-all;
}

.uc-security__revoke {
  margin-top: 12px;
}
</style>
