<template>
  <div>
    <el-button size="small" :loading="sending" :disabled="cooldown > 0" @click="send">
      {{ cooldown > 0 ? `Resend in ${cooldown}s` : 'Send code' }}
    </el-button>
  </div>
</template>

<script setup lang="ts">
// One shared code-request button (spec #24 code flows all share the
// cooldown mechanic server-side): posts the body the flow needs to the
// code endpoint, then counts down locally so Members do not hammer
// resend. The parent form owns the actual code field.
const props = defineProps<{
  action: string
  codeBody?: Record<string, string>
}>()

const emit = defineEmits<{ (event: 'sent'): void }>()

const { apiFetch } = useApi()

const sending = ref(false)
const cooldown = ref(0)
let timer: ReturnType<typeof setInterval> | undefined

onUnmounted(() => {
  if (timer !== undefined) {
    clearInterval(timer)
  }
})

async function send(): Promise<void> {
  sending.value = true
  try {
    await apiFetch(props.action, { method: 'POST', body: props.codeBody ?? {} })
    emit('sent')
    cooldown.value = 30
    timer = setInterval(() => {
      cooldown.value -= 1
      if (cooldown.value <= 0 && timer !== undefined) {
        clearInterval(timer)
        timer = undefined
      }
    }, 1000)
  } catch {
    // The parent form surfaces the named error; the button stays ready.
    emit('sent')
  }
}
</script>
