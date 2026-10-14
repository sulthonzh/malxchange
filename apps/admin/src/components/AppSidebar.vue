<template>
  <nav class="sidebar-nav">
    <NuxtLink to="/" class="entry" :class="{ active: route.path === '/' }">Home</NuxtLink>
    <section v-for="section in sections" :key="section.title" class="section">
      <p class="section-title">{{ section.title }}</p>
      <NuxtLink
        v-for="entry in section.routes"
        :key="entry.path"
        :to="entry.path"
        class="entry"
        :class="{ active: route.path === entry.path }"
      >
        {{ entry.title }}
      </NuxtLink>
    </section>
  </nav>
</template>

<script setup lang="ts">
import { adminSections, visibleRoutes } from '~/utils/routes'

// The permission-derived sidebar (issue #56): the static route table
// intersected with the live permission set from login/me. Presentation
// only — the admin-api's permission middleware is the gate, so this
// hiding is cosmetic and an unauthorized URL is refused server-side.
const { session } = useAdminSession()
const route = useRoute()

const sections = computed(() => visibleRoutes(adminSections, session.value?.permissions ?? []))
</script>

<style scoped lang="scss">
.sidebar-nav {
  display: flex;
  flex-direction: column;
  gap: 4px;

  .section {
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin-top: 10px;
  }

  .section-title {
    color: var(--text-muted);
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    margin: 0 0 4px;
  }

  .entry {
    color: var(--text-secondary);
    text-decoration: none;
    font-size: 14px;
    padding: 6px 10px;
    border-radius: 6px;

    &:hover {
      color: var(--text);
      background: var(--panel-header-bg);
    }

    &.active {
      color: var(--accent);
      background: var(--panel-header-bg);
    }
  }
}
</style>
