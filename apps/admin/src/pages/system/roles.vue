<template>
  <section class="roles">
    <div class="page-head">
      <h1>Roles</h1>
      <p class="hint">
        Every Employee holds exactly one Role; a Role's grants decide what its holders may do.
        Seeded Roles and Roles held by an Employee cannot be deleted — their grants stay editable.
      </p>
    </div>

    <el-alert
      v-if="actionError"
      class="action-error"
      type="error"
      :title="actionError"
      data-testid="action-error"
    />

    <el-table v-loading="loading" :data="roles" class="roles-table">
      <el-table-column prop="name" label="Role" min-width="160" />
      <el-table-column label="Flags" min-width="200">
        <template #default="{ row }">
          <el-tag v-if="row.system" size="small" type="info">Seeded</el-tag>
          <el-tag v-if="row.in_use" size="small" type="warning">In use</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Grants" min-width="90">
        <template #default="{ row }">{{ row.permissions.length }}</template>
      </el-table-column>
      <el-table-column label="Actions" min-width="120">
        <template #default="{ row }">
          <el-tooltip
            v-if="deleteGuard(row)"
            :content="deleteGuard(row)"
            :teleported="false"
            placement="top"
          >
            <span class="guarded"><el-button size="small" disabled>Delete</el-button></span>
          </el-tooltip>
          <el-button v-else size="small" @click="removeRole(row)">Delete</el-button>
        </template>
      </el-table-column>
    </el-table>

    <h2>Grant editor</h2>
    <p class="hint">
      The rows are the server's permission catalog — display-only; strings exist because a handler
      checks them, never because the UI invented one. Toggling a grant persists immediately and
      bites on every holder's next request, mid-session.
    </p>
    <el-table v-loading="loading" :data="catalog" class="editor-table">
      <el-table-column label="Permission" min-width="220">
        <template #default="{ row }">
          <code class="permission-name">{{ row }}</code>
        </template>
      </el-table-column>
      <el-table-column
        v-for="role in roles"
        :key="role.id"
        :label="role.name"
        :class-name="`role-${role.id}`"
        align="center"
        min-width="130"
      >
        <template #default="{ row }">
          <el-switch
            :model-value="role.permissions.includes(String(row))"
            @change="toggle(role, String(row), $event === true)"
          />
        </template>
      </el-table-column>
    </el-table>
  </section>
</template>

<script setup lang="ts">
import type { RoleRow } from '~/utils/apiTypes'

// The Role grant editor (issue #57) over the #54 Role endpoints. The
// catalog comes from GET /api/permissions — the read-only strings the
// registry seeds — so the matrix always shows every catalog row, even
// one currently granted to nobody. A toggle is one immediate
// POST /api/roles/{id}/grants; a refusal reverts the switch and lands
// in the page alert. Deletion guards (seeded, in use) come from the
// list itself; the server's 409 sentences render when they still fire.
definePageMeta({ middleware: 'employee' })

useHead({ title: 'Roles' })

const { apiFetch } = useApi()

const roles = ref<RoleRow[]>([])
const catalog = ref<string[]>([])
const loading = ref(false)
const actionError = ref('')

// The template's guard accessor: gates the delete button and names its
// tooltip, so an undeletable Role always explains itself.
function deleteGuard(role: RoleRow): string {
  if (role.system) {
    return 'Seeded roles cannot be deleted.'
  }
  if (role.in_use) {
    return 'This Role is held by an Employee and cannot be deleted.'
  }
  return ''
}

async function load(): Promise<void> {
  loading.value = true
  actionError.value = ''
  try {
    const [roleRows, permissions] = await Promise.all([
      apiFetch<RoleRow[]>('/api/roles'),
      apiFetch<string[]>('/api/permissions'),
    ])
    roles.value = roleRows
    catalog.value = permissions
  } catch (error) {
    actionError.value =
      error instanceof Error ? error.message : 'The Roles could not be loaded. Try again.'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})

async function toggle(role: RoleRow, permission: string, granted: boolean): Promise<void> {
  const had = role.permissions.includes(permission)
  if (had === granted) {
    return
  }
  applyGrant(role, permission, granted)
  actionError.value = ''
  try {
    await apiFetch(`/api/roles/${role.id}/grants`, {
      method: 'POST',
      body: { permission, granted },
    })
  } catch (error) {
    applyGrant(role, permission, !granted) // the refused toggle never happened
    actionError.value =
      error instanceof Error ? error.message : 'The grant could not be saved. Try again.'
  }
}

function applyGrant(role: RoleRow, permission: string, granted: boolean): void {
  if (granted && !role.permissions.includes(permission)) {
    role.permissions.push(permission)
  }
  if (!granted) {
    const at = role.permissions.indexOf(permission)
    if (at >= 0) {
      role.permissions.splice(at, 1)
    }
  }
}

async function removeRole(role: RoleRow): Promise<void> {
  const guard = deleteGuard(role)
  if (guard !== '') {
    actionError.value = guard
    return
  }
  actionError.value = ''
  try {
    await apiFetch(`/api/roles/${role.id}`, { method: 'DELETE' })
    await load()
  } catch (error) {
    actionError.value = error instanceof Error ? error.message : 'The Role could not be deleted.'
  }
}
</script>

<style scoped lang="scss">
.page-head {
  margin-bottom: 16px;

  h1 {
    font-size: 20px;
    font-weight: 600;
    margin: 0 0 4px;
  }
}

h2 {
  font-size: 15px;
  font-weight: 600;
  margin: 28px 0 4px;
}

.hint {
  color: var(--text-secondary);
  font-size: 13px;
  margin: 0 0 12px;
  max-width: 720px;
}

.action-error {
  margin-bottom: 12px;
}

.permission-name {
  font-family: var(--mono-font, monospace);
  font-size: 13px;
  color: var(--text);
}

.roles-table {
  margin-bottom: 8px;
}

.guarded {
  display: inline-block;
  cursor: not-allowed;
}
</style>
