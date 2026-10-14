<template>
  <div class="shell">
    <aside class="rail">
      <p class="brand">malxchange<span>admin</span></p>
      <AppSidebar />
    </aside>
    <div class="main">
      <header class="topbar">
        <span v-if="session" class="who">
          {{ session.employee.email }}
          <el-tag size="small">{{ session.employee.role }}</el-tag>
        </span>
        <el-button v-if="session" size="small" text :loading="signingOut" @click="signOut">
          Sign out
        </el-button>
      </header>
      <main class="content">
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
// The admin chrome (issue #56): sidebar + topbar shell around every
// provisioned page. Sign-out revokes the session server-side
// (POST /api/logout, audited) before the local mirror drops.
const { apiFetch } = useApi()
const { session } = useAdminSession()

const signingOut = ref(false)

async function signOut(): Promise<void> {
  signingOut.value = true
  try {
    await apiFetch('/api/logout', { method: 'POST' })
    session.value = null
    await navigateTo('/login')
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : 'Sign out failed. Try again.')
  } finally {
    signingOut.value = false
  }
}
</script>

<style scoped lang="scss">
.shell {
  display: flex;
  min-height: 100vh;

  .rail {
    width: 220px;
    flex-shrink: 0;
    background: var(--panel-bg);
    border-right: 1px solid var(--panel-border);
    padding: 16px 12px;

    .brand {
      color: var(--text);
      font-weight: 600;
      margin: 0 0 12px;
      padding: 0 10px;

      span {
        color: var(--accent);
        margin-left: 6px;
      }
    }
  }

  .main {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .topbar {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 16px;
    background: var(--panel-bg);
    border-bottom: 1px solid var(--panel-border);
    padding: 10px 20px;

    .who {
      display: flex;
      align-items: center;
      gap: 8px;
      color: var(--text-secondary);
      font-size: 13px;
    }
  }

  .content {
    flex: 1;
    padding: 24px;
  }
}
</style>
