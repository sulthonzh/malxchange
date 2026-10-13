<template>
  <NuxtLayout>
    <div class="member-center">
      <aside class="member-center__nav" aria-label="Member center">
        <el-menu :default-active="activeSection" router>
          <el-menu-item v-for="item in sections" :key="item.path" :index="item.path">
            {{ item.label }}
          </el-menu-item>
        </el-menu>
      </aside>
      <main class="member-center__content">
        <slot />
      </main>
    </div>
  </NuxtLayout>
</template>

<script setup lang="ts">
// The member-center shell (spec #24 kept list): the ported left
// navigation over the security, assets, records, deposit, withdraw,
// addresses, orders, and favorites sections — OTC, promotion,
// innovation, and payment bindings are cut per the inventory.
const route = useRoute()

const sections = [
  { label: 'Security', path: '/uc' },
  { label: 'Assets', path: '/uc/money' },
  { label: 'Records', path: '/uc/record' },
  { label: 'Deposit', path: '/uc/recharge' },
  { label: 'Withdraw', path: '/uc/withdraw' },
  { label: 'Withdraw addresses', path: '/uc/withdraw/address' },
  { label: 'Current orders', path: '/uc/entrust/current' },
  { label: 'History orders', path: '/uc/entrust/history' },
  { label: 'Favorites', path: '/uc/favor' },
] as const

const activeSection = computed(() => route.path)
</script>

<style scoped lang="scss">
.member-center {
  display: grid;
  grid-template-columns: 220px 1fr;
  gap: 16px;
  padding: 16px;
}

.member-center__content {
  min-width: 0;
}
</style>
