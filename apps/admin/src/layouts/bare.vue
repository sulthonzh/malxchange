<template>
  <div class="bare">
    <slot />
  </div>
</template>

<script setup lang="ts">
// The auth layout (login, forced setup): a centered column with no
// shell chrome — an unprovisioned Employee has no navigation yet.
</script>

<style scoped lang="scss">
.bare {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}
</style>
