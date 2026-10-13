<template>
  <MemberOrders scope="history" />
</template>

<script setup lang="ts">
definePageMeta({ middleware: 'member' })
useHead({ title: 'History orders' })
</script>
