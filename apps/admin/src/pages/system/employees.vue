<template>
  <section class="employees">
    <div class="page-head">
      <div>
        <h1>Employees</h1>
        <p class="hint">
          Staff identity for the back office. Creating an Employee and resetting their password or
          two-factor demand a fresh authenticator code (Step-up) and hand over a one-time
          credential the Employee must rotate at first sign-in.
        </p>
      </div>
      <el-button type="warning" class="create" @click="openCreate">Create Employee</el-button>
    </div>

    <el-alert
      v-if="actionError"
      class="action-error"
      type="error"
      :title="actionError"
      data-testid="action-error"
    />

    <el-table v-loading="loading" :data="employees" class="employees-table">
      <el-table-column prop="email" label="Email" min-width="220" />
      <el-table-column prop="role" label="Role" min-width="120" />
      <el-table-column label="Status" min-width="240">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'danger'" size="small">
            {{ row.enabled ? 'Enabled' : 'Disabled' }}
          </el-tag>
          <el-tag v-if="row.must_rotate" size="small" type="warning">One-time password</el-tag>
          <el-tag v-if="!row.totp_bound" size="small" type="info">Two-factor not enrolled</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Created" min-width="150">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="Actions" min-width="400">
        <template #default="{ row }">
          <template v-if="row.enabled">
            <el-tooltip
              v-if="disableGuard(row)"
              :content="disableGuard(row)"
              :teleported="false"
              placement="top"
            >
              <span class="guarded"><el-button size="small" disabled>Disable</el-button></span>
            </el-tooltip>
            <el-button v-else size="small" @click="disable(row)">Disable</el-button>
          </template>
          <el-button v-else size="small" @click="enable(row)">Enable</el-button>
          <el-button size="small" @click="openResetPassword(row)">Reset password</el-button>
          <el-button size="small" @click="openResetTotp(row)">Reset two-factor</el-button>
          <el-tooltip
            v-if="row.id === selfId"
            content="You cannot change your own Role."
            :teleported="false"
            placement="top"
          >
            <span class="guarded"><el-button size="small" disabled>Change role</el-button></span>
          </el-tooltip>
          <el-button v-else size="small" @click="openAssignRole(row)">Change role</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="createVisible" title="Create Employee" width="480">
      <el-alert
        v-if="dialogError"
        class="dialog-error"
        type="error"
        :closable="false"
        :title="dialogError"
      />
      <el-form :model="createForm" label-position="top" @submit.prevent>
        <el-form-item label="Email" prop="email">
          <el-input
            name="email"
            v-model="createForm.email"
            placeholder="employee@exchange.example.com"
          />
        </el-form-item>
        <el-form-item label="One-time password" prop="password">
          <el-input
            name="password"
            v-model="createForm.password"
            type="password"
            show-password
            placeholder="The Employee rotates this at first sign-in"
          />
        </el-form-item>
        <el-form-item label="Role" prop="roleId">
          <el-select v-model="createForm.roleId" placeholder="Choose a Role" :teleported="false">
            <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="Authenticator code (Step-up)" prop="code">
          <el-input
            name="code"
            v-model="createForm.code"
            inputmode="numeric"
            placeholder="6-digit code"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">Cancel</el-button>
        <el-button type="warning" :loading="busy" @click="submitCreate">Create</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="resetPasswordVisible" title="Reset password" width="480">
      <el-alert
        v-if="dialogError"
        class="dialog-error"
        type="error"
        :closable="false"
        :title="dialogError"
      />
      <el-alert
        v-if="oneTimePassword !== ''"
        class="one-time-result"
        type="warning"
        :closable="false"
      >
        <template #title>
          One-time password: <code class="otp">{{ oneTimePassword }}</code> — shown once. The
          Employee rotates it at next sign-in.
        </template>
      </el-alert>
      <p v-if="resetPasswordTarget" class="hint">
        Resetting the password for <strong>{{ resetPasswordTarget.email }}</strong> revokes their
        live sessions.
      </p>
      <el-form :model="resetForm" label-position="top" @submit.prevent>
        <el-form-item label="New password (leave empty to generate one)" prop="password">
          <el-input
            name="password"
            v-model="resetForm.password"
            type="password"
            show-password
            placeholder="Generated and shown once when left empty"
          />
        </el-form-item>
        <el-form-item label="Authenticator code (Step-up)" prop="code">
          <el-input name="code" v-model="resetForm.code" inputmode="numeric" placeholder="6-digit code" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="resetPasswordVisible = false">Close</el-button>
        <el-button type="warning" :loading="busy" @click="submitResetPassword">Reset</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="resetTotpVisible" title="Reset two-factor" width="480">
      <el-alert
        v-if="dialogError"
        class="dialog-error"
        type="error"
        :closable="false"
        :title="dialogError"
      />
      <p v-if="resetTotpTarget" class="hint">
        Clearing the authenticator binding for
        <strong>{{ resetTotpTarget.email }}</strong> revokes their live sessions and forces
        re-enrollment at next sign-in.
      </p>
      <el-form :model="totpForm" label-position="top" @submit.prevent>
        <el-form-item label="Authenticator code (Step-up)" prop="code">
          <el-input name="code" v-model="totpForm.code" inputmode="numeric" placeholder="6-digit code" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="resetTotpVisible = false">Cancel</el-button>
        <el-button type="warning" :loading="busy" @click="submitResetTotp">Reset</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="assignVisible" title="Change role" width="480">
      <el-alert
        v-if="dialogError"
        class="dialog-error"
        type="error"
        :closable="false"
        :title="dialogError"
      />
      <p v-if="assignTarget" class="hint">
        Moving <strong>{{ assignTarget.email }}</strong> to another Role. Every Employee holds
        exactly one Role; the change bites on their next request.
      </p>
      <el-form :model="assignForm" label-position="top" @submit.prevent>
        <el-form-item label="Role" prop="roleId">
          <el-select v-model="assignForm.roleId" placeholder="Choose a Role" :teleported="false">
            <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="assignVisible = false">Cancel</el-button>
        <el-button type="warning" :loading="busy" @click="submitAssign">Confirm</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import type { EmployeeRow, RoleRow } from '~/utils/apiTypes'
import { disableRefusal, roleChangeRefusal } from '~/utils/employeeGuards'
import { formatDateTime } from '~/utils/format'

// Employee management (issue #57) over the #54 management endpoints.
// The ADR-0012 guards (self-disable, self-role-removal, last enabled
// super-admin) are mirrored client-side — the buttons go inert with the
// refusal as their tooltip — and every server refusal renders in the
// page alert. Step-up actions carry the caller's fresh authenticator
// code in the body; the api's 401 sentences surface in the dialog.
definePageMeta({ middleware: 'employee' })

useHead({ title: 'Employees' })

const { apiFetch } = useApi()
const { session } = useAdminSession()

const selfId = computed(() => session.value?.employee.id ?? '')

const employees = ref<EmployeeRow[]>([])
const roles = ref<RoleRow[]>([])
const loading = ref(false)
const busy = ref(false)
const actionError = ref('')
const dialogError = ref('')

// The template's guard accessor: one call site per rendered row gates
// the button and names the tooltip, so the refusal is always visible
// text, never a silent inert button.
function disableGuard(row: EmployeeRow): string {
  return disableRefusal(row, selfId.value, employees.value) ?? ''
}

async function load(): Promise<void> {
  loading.value = true
  actionError.value = ''
  try {
    const [staff, roleRows] = await Promise.all([
      apiFetch<EmployeeRow[]>('/api/employees'),
      apiFetch<RoleRow[]>('/api/roles'),
    ])
    employees.value = staff
    roles.value = roleRows
  } catch (error) {
    actionError.value =
      error instanceof Error ? error.message : 'The Employee list could not be loaded. Try again.'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})

async function runRowAction(action: () => Promise<void>): Promise<void> {
  actionError.value = ''
  try {
    await action()
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : 'The action failed. Try again.'
  }
}

async function disable(target: EmployeeRow): Promise<void> {
  const refusal = disableRefusal(target, selfId.value, employees.value)
  if (refusal !== null) {
    // The client guard caught it — show the refusal, send nothing.
    actionError.value = refusal
    return
  }
  await runRowAction(async () => {
    await apiFetch(`/api/employees/${target.id}/disable`, { method: 'POST', body: {} })
    await load()
  })
}

async function enable(target: EmployeeRow): Promise<void> {
  await runRowAction(async () => {
    await apiFetch(`/api/employees/${target.id}/enable`, { method: 'POST', body: {} })
    await load()
  })
}

// ---- create ----

const createVisible = ref(false)
const createForm = reactive({ email: '', password: '', roleId: '', code: '' })

function openCreate(): void {
  dialogError.value = ''
  createForm.email = ''
  createForm.password = ''
  createForm.roleId = ''
  createForm.code = ''
  createVisible.value = true
}

// The client-side guard: every field the dialog demands, checked
// before anything is sent — a miss renders its sentence in the dialog,
// it never silently no-ops. The server re-checks everything.
function requireField(value: string, sentence: string): string {
  return value.trim() === '' ? sentence : ''
}

async function submitCreate(): Promise<void> {
  const missing =
    requireField(createForm.email, 'Enter the Employee’s email address.') ||
    (createForm.email.includes('@') ? '' : 'Enter a valid email address.') ||
    requireField(createForm.password, 'Enter a one-time password.') ||
    requireField(createForm.roleId, 'Choose a Role from the list.') ||
    requireField(createForm.code, 'Enter a fresh authenticator code.')
  dialogError.value = missing
  if (missing !== '') {
    return
  }
  busy.value = true
  try {
    await apiFetch('/api/employees', {
      method: 'POST',
      body: {
        email: createForm.email,
        password: createForm.password,
        role_id: createForm.roleId,
        code: createForm.code,
      },
    })
    createVisible.value = false
    await load()
  } catch (error) {
    dialogError.value = error instanceof Error ? error.message : 'The Employee could not be created.'
  } finally {
    busy.value = false
  }
}

// ---- password reset ----

const resetPasswordVisible = ref(false)
const resetPasswordTarget = ref<EmployeeRow | null>(null)
const oneTimePassword = ref('')
const resetForm = reactive({ password: '', code: '' })

function openResetPassword(target: EmployeeRow): void {
  dialogError.value = ''
  oneTimePassword.value = ''
  resetForm.password = ''
  resetForm.code = ''
  resetPasswordTarget.value = target
  resetPasswordVisible.value = true
}

async function submitResetPassword(): Promise<void> {
  dialogError.value = requireField(resetForm.code, 'Enter a fresh authenticator code.')
  if (dialogError.value !== '') {
    return
  }
  busy.value = true
  try {
    const answer = await apiFetch<{ one_time_password?: string }>(
      `/api/employees/${resetPasswordTarget.value?.id}/reset-password`,
      { method: 'POST', body: { password: resetForm.password, code: resetForm.code } },
    )
    oneTimePassword.value = answer.one_time_password ?? ''
    if (oneTimePassword.value === '') {
      resetPasswordVisible.value = false
    }
    await load()
  } catch (error) {
    dialogError.value = error instanceof Error ? error.message : 'The password could not be reset.'
  } finally {
    busy.value = false
  }
}

// ---- TOTP reset ----

const resetTotpVisible = ref(false)
const resetTotpTarget = ref<EmployeeRow | null>(null)
const totpForm = reactive({ code: '' })

function openResetTotp(target: EmployeeRow): void {
  dialogError.value = ''
  totpForm.code = ''
  resetTotpTarget.value = target
  resetTotpVisible.value = true
}

async function submitResetTotp(): Promise<void> {
  dialogError.value = requireField(totpForm.code, 'Enter a fresh authenticator code.')
  if (dialogError.value !== '') {
    return
  }
  busy.value = true
  try {
    await apiFetch(`/api/employees/${resetTotpTarget.value?.id}/reset-totp`, {
      method: 'POST',
      body: { code: totpForm.code },
    })
    resetTotpVisible.value = false
    await load()
  } catch (error) {
    dialogError.value =
      error instanceof Error ? error.message : 'Two-factor could not be reset.'
  } finally {
    busy.value = false
  }
}

// ---- Role assignment ----

const assignVisible = ref(false)
const assignTarget = ref<EmployeeRow | null>(null)
const assignForm = reactive({ roleId: '' })

function openAssignRole(target: EmployeeRow): void {
  dialogError.value = ''
  assignForm.roleId = ''
  assignTarget.value = target
  assignVisible.value = true
}

async function submitAssign(): Promise<void> {
  dialogError.value = requireField(assignForm.roleId, 'Choose a Role from the list.')
  if (dialogError.value !== '') {
    return
  }
  const target = assignTarget.value
  const chosen = roles.value.find((role) => role.id === assignForm.roleId)
  if (target !== null && chosen !== undefined) {
    // The client guard mirrors the server: refuse before sending.
    const refusal = roleChangeRefusal(target, selfId.value, chosen.name, employees.value)
    if (refusal !== null) {
      dialogError.value = refusal
      return
    }
  }
  busy.value = true
  try {
    await apiFetch(`/api/employees/${target?.id}/role`, {
      method: 'POST',
      body: { role_id: assignForm.roleId },
    })
    assignVisible.value = false
    await load()
  } catch (error) {
    dialogError.value = error instanceof Error ? error.message : 'The Role could not be assigned.'
  } finally {
    busy.value = false
  }
}
</script>

<style scoped lang="scss">
.page-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;

  h1 {
    font-size: 20px;
    font-weight: 600;
    margin: 0 0 4px;
  }
}

.hint {
  color: var(--text-secondary);
  font-size: 13px;
  margin: 0;
  max-width: 640px;
}

.action-error {
  margin-bottom: 12px;
}

.dialog-error {
  margin-bottom: 12px;
}

.one-time-result {
  margin-bottom: 12px;

  .otp {
    font-family: var(--mono-font, monospace);
    font-size: 14px;
    user-select: all;
  }
}

.guarded {
  display: inline-block;
  cursor: not-allowed;
}
</style>
