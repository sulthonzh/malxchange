<template>
  <MemberOrders scope="current" />
</template>

<script setup lang="ts">
definePageMeta({ middleware: 'member' })
useHead({ title: 'Current orders' })
</script>
