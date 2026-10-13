<template>
  <div class="kyc-panel">
    <template v-if="status === 'verified'">
      <p class="kyc-panel__done">Your identity is verified. Withdrawals are unlocked once an authenticator is bound.</p>
    </template>
    <template v-else-if="status === 'pending'">
      <p class="kyc-panel__pending">Your identity check is being reviewed. This usually takes up to one business day.</p>
    </template>
    <template v-else>
      <el-form :model="form" label-position="top" @submit.prevent="submit">
        <el-form-item label="Full name">
          <el-input v-model="form.fullName" autocomplete="name" placeholder="Exactly as on the document" />
        </el-form-item>
        <el-form-item label="Document number">
          <el-input v-model="form.documentNumber" autocomplete="off" />
        </el-form-item>
        <el-form-item label="Document front image">
          <input type="file" accept="image/*" @change="(event) => takeFile(event, 'front')" />
        </el-form-item>
        <el-form-item label="Document back image">
          <input type="file" accept="image/*" @change="(event) => takeFile(event, 'back')" />
        </el-form-item>
        <p v-if="formError !== ''" class="kyc-panel__error" role="alert">{{ formError }}</p>
        <el-button native-type="submit" type="primary" :loading="submitting">Submit identity check</el-button>
      </el-form>
    </template>
  </div>
</template>

<script setup lang="ts">
// The KYC panel (spec #24 story 12): full name, document number, and the
// front/back document images submitted to POST /uc/kyc. No local
// document-format validation (per spec), no handheld photo. The status
// comes from the security summary; the parent reloads it after submit.
const props = defineProps<{ status: string }>()

const emit = defineEmits<{ (event: 'submitted'): void }>()

const { apiFetch } = useApi()

const form = reactive({ fullName: '', documentNumber: '' })
const images = reactive<{ front: string; back: string }>({ front: '', back: '' })
const formError = ref('')
const submitting = ref(false)

function readAsDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : '')
    reader.onerror = () => reject(new Error('read'))
    reader.readAsDataURL(file)
  })
}

async function takeFile(event: Event, side: 'front' | 'back'): Promise<void> {
  const input = event.target as HTMLInputElement | null
  const file = input?.files?.[0]
  if (!file) {
    return
  }
  if (file.size > 5 * 1024 * 1024) {
    formError.value = 'Each image must be smaller than 5 MB.'
    return
  }
  try {
    const dataUrl = await readAsDataUrl(file)
    images[side] = dataUrl.includes(',') ? dataUrl.slice(dataUrl.indexOf(',') + 1) : dataUrl
  } catch {
    formError.value = 'The image could not be read. Try another file.'
  }
}

async function submit(): Promise<void> {
  formError.value = ''
  if (form.fullName.trim() === '' || form.documentNumber.trim() === '') {
    formError.value = 'Enter your full name and document number exactly as they appear on the document.'
    return
  }
  if (images.front === '' || images.back === '') {
    formError.value = 'Attach both the front and the back of your document.'
    return
  }
  submitting.value = true
  try {
    await apiFetch('/uc/kyc', {
      method: 'POST',
      body: {
        fullName: form.fullName.trim(),
        documentNumber: form.documentNumber.trim(),
        frontImage: images.front,
        backImage: images.back,
      },
    })
    emit('submitted')
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'The identity check could not be submitted.'
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped lang="scss">
.kyc-panel__error {
  color: #f15057;
  font-size: 14px;
}

.kyc-panel__done {
  color: #00b275;
  font-size: 14px;
}

.kyc-panel__pending {
  color: var(--text-secondary);
  font-size: 14px;
}
</style>
