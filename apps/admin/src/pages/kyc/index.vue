<template>
  <section class="kyc">
    <h1>KYC review</h1>
    <p class="hint">
      Pending identity applications, oldest first. Compare the applicant against the document
      images before deciding; a rejection states a reason the member sees.
    </p>

    <p v-if="error" class="error" data-test="kyc-error">{{ error }}</p>

    <el-table
      v-loading="loading"
      :data="applications"
      data-test="kyc-table"
      empty-text="No pending applications."
    >
      <el-table-column prop="email" label="Member" min-width="220" />
      <el-table-column prop="fullName" label="Name on document" min-width="180" />
      <el-table-column prop="documentNumber" label="Document number" width="180" />
      <el-table-column prop="submittedAt" label="Submitted" width="200" />
      <el-table-column label="" width="110">
        <template #default="{ row }">
          <el-button size="small" type="primary" plain data-test="kyc-review-open" @click="open(row)">
            Review
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      v-model:current-page="pageNo"
      :page-size="pageSize"
      :total="total"
      layout="prev, pager, next, total"
      class="pager"
      @current-change="load"
    />

    <el-dialog
      v-model="reviewOpen"
      title="Review identity application"
      width="560px"
      data-test="kyc-review-dialog"
    >
      <template v-if="reviewing">
        <el-descriptions :column="1" border class="identity">
          <el-descriptions-item label="Member">{{ reviewing.email }}</el-descriptions-item>
          <el-descriptions-item label="Name">{{ reviewing.fullName }}</el-descriptions-item>
          <el-descriptions-item label="Document number">
            {{ reviewing.documentNumber }}
          </el-descriptions-item>
        </el-descriptions>
        <p v-if="evidenceError" class="error" data-test="kyc-evidence-error">{{ evidenceError }}</p>
        <div v-else class="evidence" v-loading="evidenceLoading">
          <figure data-test="kyc-front">
            <figcaption>Document front</figcaption>
            <img v-if="evidence?.frontImage" :src="src(evidence.frontImage)" alt="Document front" />
          </figure>
          <figure data-test="kyc-back">
            <figcaption>Document back</figcaption>
            <img v-if="evidence?.backImage" :src="src(evidence.backImage)" alt="Document back" />
          </figure>
        </div>
        <el-input
          v-model="reason"
          type="textarea"
          :rows="2"
          data-test="kyc-reason"
          placeholder="Reason (required to reject; recorded either way)"
        />
        <p v-if="reviewError" class="error" data-test="kyc-review-error">{{ reviewError }}</p>
      </template>
      <template #footer>
        <el-button @click="reviewOpen = false">Close</el-button>
        <el-button
          type="danger"
          data-test="kyc-reject"
          :loading="deciding"
          @click="decide(false)"
        >
          Reject
        </el-button>
        <el-button
          type="primary"
          data-test="kyc-approve"
          :loading="deciding"
          @click="decide(true)"
        >
          Approve
        </el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
// The compliance KYC queue (issue #58): api's pending applications,
// evidence fetched per application when the review opens (the queue
// itself carries no document images), approve/reject with a reason —
// every decision is audit-logged server-side.
definePageMeta({ middleware: 'employee' })

interface KycApplication {
  memberId: string
  email: string
  fullName: string
  documentNumber: string
  submittedAt: string
}

interface KycQueueResponse {
  total: number
  applications: KycApplication[]
}

interface KycEvidence {
  memberId: string
  fullName: string
  documentNumber: string
  frontImage: string
  backImage: string
}

const { apiFetch } = useApi()

const applications = ref<KycApplication[]>([])
const total = ref(0)
const pageNo = ref(1)
const pageSize = 20
const loading = ref(false)
const error = ref('')

const reviewOpen = ref(false)
const reviewing = ref<KycApplication | null>(null)
const evidence = ref<KycEvidence | null>(null)
const evidenceLoading = ref(false)
const evidenceError = ref('')
const reason = ref('')
const reviewError = ref('')
const deciding = ref(false)

useHead({ title: 'KYC review' })

onMounted(load)

async function load(): Promise<void> {
  error.value = ''
  loading.value = true
  try {
    const out = await apiFetch<KycQueueResponse>(
      `/api/kyc/applications?pageNo=${pageNo.value}&pageSize=${pageSize}`,
    )
    applications.value = out.applications
    total.value = out.total
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'The queue could not be loaded.'
  } finally {
    loading.value = false
  }
}

async function open(application: KycApplication): Promise<void> {
  reviewing.value = application
  evidence.value = null
  evidenceError.value = ''
  reviewError.value = ''
  reason.value = ''
  reviewOpen.value = true
  evidenceLoading.value = true
  try {
    evidence.value = await apiFetch<KycEvidence>(
      `/api/kyc/applications/${application.memberId}/evidence`,
    )
  } catch (cause) {
    evidenceError.value =
      cause instanceof Error ? cause.message : 'The document images could not be loaded.'
  } finally {
    evidenceLoading.value = false
  }
}

async function decide(approved: boolean): Promise<void> {
  if (reviewing.value === null) {
    return
  }
  reviewError.value = ''
  if (!approved && reason.value.trim() === '') {
    reviewError.value = 'A reason is required to reject a KYC application.'
    return
  }
  deciding.value = true
  try {
    await apiFetch(`/api/kyc/applications/${reviewing.value.memberId}/review`, {
      method: 'POST',
      body: { approved, reason: reason.value.trim() },
    })
    reviewOpen.value = false
    reviewing.value = null
    await load()
  } catch (cause) {
    reviewError.value = cause instanceof Error ? cause.message : 'The decision failed. Try again.'
  } finally {
    deciding.value = false
  }
}

function src(base64: string): string {
  return `data:image/jpeg;base64,${base64}`
}
</script>

<style scoped lang="scss">
.kyc {
  h1 {
    font-size: 20px;
    font-weight: 600;
    margin: 0 0 8px;
  }

  .hint {
    color: var(--text-secondary);
    font-size: 13px;
    margin: 0 0 16px;
  }

  .error {
    color: var(--el-color-danger, #f56c6c);
    font-size: 13px;
    margin: 0 0 8px;
  }

  .pager {
    margin-top: 12px;
    justify-content: flex-end;
  }

  .identity {
    margin-bottom: 12px;
  }

  .evidence {
    display: flex;
    gap: 12px;
    margin-bottom: 12px;

    figure {
      flex: 1;
      margin: 0;

      figcaption {
        color: var(--text-muted);
        font-size: 12px;
        margin-bottom: 4px;
      }

      img {
        width: 100%;
        border-radius: 6px;
        border: 1px solid var(--panel-header-bg, #333);
      }
    }
  }
}
</style>
