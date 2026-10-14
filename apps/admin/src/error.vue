<template>
  <div class="error-page">
    <p class="status">{{ error.statusCode }}</p>
    <h1>{{ headline }}</h1>
    <p class="detail">{{ detail }}</p>
    <el-button type="warning" @click="goHome">Go to the home page</el-button>
  </div>
</template>

<script setup lang="ts">
import type { NuxtError } from '#app'

// The 404/error surface (issue #56): unknown routes render this page
// with a 404 status instead of silently serving the home page — a
// route whose slice has not landed yet answers here too.
const props = defineProps<{ error: NuxtError }>()

const isNotFound = computed(() => props.error.statusCode === 404)
const headline = computed(() =>
  isNotFound.value
    ? 'The page you are looking for does not exist.'
    : 'Something went wrong while showing this page.',
)
const detail = computed(() =>
  isNotFound.value
    ? 'Check the address, or return to the home page.'
    : 'Return to the home page and try again. If the problem repeats, contact your administrator.',
)

useHead({ title: isNotFound.value ? 'Page not found' : 'Error' })

function goHome() {
  clearError({ redirect: '/' })
}
</script>

<style scoped lang="scss">
.error-page {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 0 20px;
  text-align: center;
  background: var(--body-bg);
  color: var(--text);

  .status {
    font-size: 72px;
    font-weight: 600;
    color: var(--accent);
    margin: 0;
  }

  h1 {
    font-size: 22px;
    font-weight: 400;
    margin: 0;
  }

  .detail {
    color: var(--text-secondary);
    margin: 0 0 20px;
  }
}
</style>
