<template>
  <section class="home">
    <h1>Home</h1>
    <p class="identity">
      Signed in as <strong>{{ session?.employee.email }}</strong>
      <el-tag size="small">{{ session?.employee.role }}</el-tag>
    </p>
    <h2>Your permissions</h2>
    <p class="hint">
      The sidebar offers exactly the surfaces this set admits; the server enforces the same set on
      every request.
    </p>
    <div class="permissions">
      <el-tag v-for="permission in session?.permissions ?? []" :key="permission" class="perm">
        {{ permission }}
      </el-tag>
      <span v-if="!(session?.permissions ?? []).length" class="none">
        No permissions are granted to your Role yet.
      </span>
    </div>
  </section>
</template>

<script setup lang="ts">
// The admin home (issue #56): deliberately simple — no demo charts.
// It shows the signed-in Employee and their live permission set, the
// same set that derives the sidebar.
definePageMeta({ middleware: 'employee' })

const { session } = useAdminSession()

useHead({ title: 'Home' })
</script>

<style scoped lang="scss">
.home {
  h1 {
    font-size: 20px;
    font-weight: 600;
    margin: 0 0 8px;
  }

  .identity {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 0 24px;
    color: var(--text-secondary);
  }

  h2 {
    font-size: 15px;
    font-weight: 600;
    margin: 0 0 4px;
  }

  .hint {
    color: var(--text-secondary);
    font-size: 13px;
    margin: 0 0 12px;
  }

  .permissions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;

    .perm {
      font-family: var(--mono-font, monospace);
    }

    .none {
      color: var(--text-muted);
      font-size: 13px;
    }
  }
}
</style>
