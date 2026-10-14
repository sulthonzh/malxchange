<template>
  <NuxtLayout>
    <NuxtPage />
  </NuxtLayout>
</template>

<script setup lang="ts">
// Every page sets its own title through useHead; this template appends
// the app name so no page can ship an ambiguous document title.
useHead({
  titleTemplate: (title) => (title ? `${title} · malxchange admin` : 'malxchange admin'),
})
</script>
