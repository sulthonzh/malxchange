<template>
  <section class="channels">
    <header class="head">
      <div>
        <h1>Code channels</h1>
        <p class="hint">
          SMS, WhatsApp and Telegram delivery for Verification codes. Email is always on and not
          managed here. Credentials are write-only: they are validated when saved and never shown
          again.
        </p>
      </div>
      <el-button :loading="loading" @click="refresh">Refresh</el-button>
    </header>

    <p v-if="error" class="error" role="alert">{{ error }}</p>

    <el-table v-loading="loading" :data="channels">
      <el-table-column label="Channel" prop="channel" width="140" />
      <el-table-column label="Status" width="110">
        <template #default="{ row }">
          <el-tag :type="row.active ? 'success' : 'info'" size="small">
            {{ row.active ? 'Active' : 'Inactive' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Activated at" prop="activatedAt" />
      <el-table-column label="Updated at" prop="updatedAt" />
      <el-table-column label="Actions" width="280" align="right">
        <template #default="{ row }">
          <template v-if="confirming === row.channel">
            <span class="confirm">Deactivate this channel?</span>
            <el-button size="small" type="danger" @click="deactivate(row.channel)">
              Yes, deactivate
            </el-button>
            <el-button size="small" @click="confirming = ''">Cancel</el-button>
          </template>
          <template v-else>
            <el-button
              v-if="!row.active"
              size="small"
              type="primary"
              @click="openActivate(row.channel)"
            >
              Activate
            </el-button>
            <el-button v-else size="small" @click="confirming = row.channel">Deactivate</el-button>
          </template>
        </template>
      </el-table-column>
    </el-table>

    <el-card v-if="activating" class="activate-card" shadow="never">
      <template #header>Activate {{ activating }}</template>
      <p class="hint">
        Enter the {{ activating }} driver's credentials and a fresh authenticator code. Credentials
        are saved once in the channel registry and never rendered again.
      </p>
      <p v-if="formError" class="error" role="alert">{{ formError }}</p>
      <el-form label-position="top" @submit.prevent="submitActivate">
        <el-form-item v-for="field in activeFields" :key="field.key" :label="field.label">
          <el-input
            v-model="form.credentials[field.key]"
            :name="'credentials-' + field.key"
            :type="field.secret ? 'password' : 'text'"
            :placeholder="field.placeholder"
            :show-password="field.secret"
            :autocomplete="field.secret ? 'new-password' : 'off'"
          />
        </el-form-item>
        <el-form-item label="Authenticator code">
          <el-input
            v-model="form.code"
            name="code"
            placeholder="6-digit code"
            maxlength="6"
            autocomplete="one-time-code"
          />
        </el-form-item>
        <div class="actions">
          <el-button type="primary" native-type="submit" :loading="saving">
            Save and activate
          </el-button>
          <el-button @click="closeActivate">Cancel</el-button>
        </div>
      </el-form>
    </el-card>
  </section>
</template>

<script setup lang="ts">
// The Code channel surface (ticket #62): the super-admin list of the
// SMS/WhatsApp/Telegram registry rows, activation with a per-driver
// credential form + the fresh authenticator code the api demands on
// every credential save (Step-up), and deactivation. The registry
// state lives in the api service — this page only drives admin-api's
// gated front. Credentials are write-only: the form fields exist just
// for one save attempt and are cleared on success AND on cancel; the
// page never fetches credential material, because no endpoint serves
// it.
definePageMeta({ middleware: 'employee' })

interface ChannelRow {
  channel: string
  active: boolean
  activatedAt: string | null
  updatedAt: string
}

interface CredentialField {
  key: string
  label: string
  placeholder: string
  secret: boolean
  required: boolean
}

// The drivers' credential shapes, per the api registry's per-driver
// validation (internal/api/drivers.go): only the named channel's keys
// are required; defaults fill the optional endpoint bases.
const driverFields: Record<string, CredentialField[]> = {
  sms: [
    { key: 'endpoint', label: 'Endpoint', placeholder: 'HTTP delivery endpoint', secret: false, required: true },
    { key: 'apiKey', label: 'API key', placeholder: 'Provider API key', secret: true, required: true },
  ],
  whatsapp: [
    { key: 'endpoint', label: 'Graph API base URL', placeholder: 'Defaults to the WhatsApp Graph API', secret: false, required: false },
    { key: 'phoneNumberId', label: 'Phone number ID', placeholder: 'WhatsApp phone number ID', secret: false, required: true },
    { key: 'accessToken', label: 'Access token', placeholder: 'WhatsApp access token', secret: true, required: true },
  ],
  telegram: [
    { key: 'botToken', label: 'Bot token', placeholder: 'The BotFather token', secret: true, required: true },
    { key: 'webhookSecret', label: 'Webhook secret', placeholder: 'Secret token for webhook updates', secret: true, required: true },
  ],
}

const { apiFetch } = useApi()

const channels = ref<ChannelRow[]>([])
const loading = ref(false)
const error = ref('')
const activating = ref('')
const confirming = ref('')
const saving = ref(false)
const formError = ref('')
const form = reactive({ credentials: {} as Record<string, string>, code: '' })

const activeFields = computed(() => driverFields[activating.value] ?? [])

async function refresh(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    channels.value = await apiFetch<ChannelRow[]>('/api/channels')
  } catch (cause) {
    error.value =
      cause instanceof Error ? cause.message : 'Loading the channels failed. Try again.'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void refresh()
})

function openActivate(channel: string): void {
  activating.value = channel
  confirming.value = ''
  formError.value = ''
  form.credentials = {}
  form.code = ''
}

function closeActivate(): void {
  // Write-only hygiene: the credential inputs never outlive the form.
  form.credentials = {}
  form.code = ''
  activating.value = ''
}

async function submitActivate(): Promise<void> {
  formError.value = ''
  const channel = activating.value
  const missing = (driverFields[channel] ?? []).filter(
    (field) => field.required && (form.credentials[field.key] ?? '').trim() === '',
  )
  if (missing.length > 0) {
    formError.value = `Enter ${missing.map((field) => field.label).join(', ')}.`
    return
  }
  if (form.code.trim() === '') {
    formError.value = 'Enter a fresh authenticator code.'
    return
  }
  saving.value = true
  try {
    await apiFetch(`/api/channels/${channel}/activate`, {
      method: 'POST',
      body: { credentials: form.credentials, code: form.code.trim() },
    })
    closeActivate()
    await refresh()
  } catch (cause) {
    // api's refusal — a bad credential set, an unknown channel — is
    // exactly the sentence the operator needs; render it verbatim.
    formError.value =
      cause instanceof Error ? cause.message : 'Activating the channel failed. Try again.'
  } finally {
    saving.value = false
  }
}

async function deactivate(channel: string): Promise<void> {
  error.value = ''
  try {
    await apiFetch(`/api/channels/${channel}/deactivate`, { method: 'POST', body: {} })
    confirming.value = ''
    await refresh()
  } catch (cause) {
    confirming.value = ''
    error.value =
      cause instanceof Error ? cause.message : 'Deactivating the channel failed. Try again.'
  }
}

useHead({ title: 'Code channels' })
</script>

<style scoped lang="scss">
.channels {
  .head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 16px;

    h1 {
      font-size: 20px;
      font-weight: 600;
      margin: 0 0 4px;
    }

    .hint {
      color: var(--text-secondary);
      font-size: 13px;
      max-width: 640px;
      margin: 0;
    }
  }

  .error {
    color: var(--el-color-danger, #f56c6c);
    font-size: 13px;
    margin: 0 0 12px;
  }

  .confirm {
    color: var(--text-secondary);
    font-size: 13px;
    margin-right: 8px;
  }

  .activate-card {
    margin-top: 16px;
    max-width: 520px;

    .hint {
      color: var(--text-secondary);
      font-size: 13px;
      margin: 0 0 12px;
    }

    .actions {
      display: flex;
      gap: 8px;
    }
  }
}
</style>
