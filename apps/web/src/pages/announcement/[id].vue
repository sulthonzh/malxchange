<template>
  <div class="notice-item">
    <div class="main">
      <aside class="sidebar">
        <h2>Announcements</h2>
        <NuxtLink
          v-for="item in recent"
          :key="item.id"
          :class="item.id === info?.id ? 'active' : 'item'"
          :to="`/announcement/${item.id}`"
        >
          <span class="text">{{ item.title }}</span>
        </NuxtLink>
      </aside>
      <article class="content-wrap">
        <p v-if="loading" class="state">Loading this announcement…</p>
        <p v-else-if="info === null" class="state">
          This announcement does not exist or is no longer available.
        </p>
        <template v-else>
          <header class="header">
            <h2>{{ info.title }}</h2>
            <span class="time">{{ formatDate(info.createTime) }}</span>
          </header>
          <!-- CMS-stored HTML from the api; sanitized before it reaches v-html. -->
          <div class="content" v-html="sanitizedBody"></div>
          <div class="nav-bottom">
            <NuxtLink v-if="back" class="prev" :to="`/announcement/${back.id}`">
              <span>Previous announcement</span>
              <p>{{ back.title }}</p>
            </NuxtLink>
            <NuxtLink v-if="next" class="next" :to="`/announcement/${next.id}`">
              <span>Next announcement</span>
              <p>{{ next.title }}</p>
            </NuxtLink>
          </div>
        </template>
      </article>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref, watch } from 'vue'

import {
  useContent,
  type AnnouncementDetail,
  type ContentSummary,
} from '~/composables/useContent'
import { formatDate } from '~/utils/format'
import { sanitizeCmsHtml } from '~/utils/sanitize'

// Announcement detail, ported from the legacy pages/cms/NoticeItem.vue:
// the recent-announcements sidebar beside the article, and the contract's
// back/next adjacent summaries under the body. The legacy QR share,
// duplicated bottom list, and app-download band are cut (spec #24 cut
// list). Anonymous-readable — no route middleware; a 404 (including the
// degraded-admin-api case) renders the not-found sentence, never an error.

const props = defineProps<{ id: string }>()

const { announcement: fetchAnnouncement, announcements: fetchAnnouncements } = useContent()

const detail = ref<AnnouncementDetail | null>(null)
const recent = ref<ContentSummary[]>([])
const loading = ref(true)

const info = computed(() => detail.value?.info ?? null)
const back = computed(() => detail.value?.back ?? null)
const next = computed(() => detail.value?.next ?? null)
const sanitizedBody = computed(() =>
  detail.value === null ? '' : sanitizeCmsHtml(detail.value.info.content),
)

useHead({ title: computed(() => info.value?.title ?? 'Announcement') })

onMounted(() => {
  void load()
  void loadRecent()
})

watch(
  () => props.id,
  () => void load(),
)

async function load(): Promise<void> {
  loading.value = true
  try {
    detail.value = await fetchAnnouncement(props.id)
  } catch {
    detail.value = null
  } finally {
    loading.value = false
  }
}

async function loadRecent(): Promise<void> {
  try {
    const page = await fetchAnnouncements(1, 10)
    recent.value = page.content
  } catch {
    recent.value = []
  }
}
</script>

<style scoped lang="scss">
.notice-item {
  padding: 40px 0 60px;

  .main {
    display: flex;
    gap: 30px;
    width: 90%;
    max-width: 1400px;
    margin: 0 auto;
    align-items: flex-start;
  }

  .sidebar {
    flex: none;
    width: 22%;
    background: var(--panel-bg);
    border-radius: 6px;
    padding: 20px;

    h2 {
      font-size: 18px;
      margin: 0 0 10px;
      color: var(--text);
    }

    .item,
    .active {
      display: block;
      padding: 12px 0;
      line-height: 1.4;
      font-size: 14px;
      color: var(--text-secondary);
      border-bottom: 1px solid var(--panel-border);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;

      &:hover {
        color: var(--accent);
      }
    }

    .active {
      color: var(--accent);
    }
  }

  .content-wrap {
    flex: 1;
    min-width: 0;
    background: var(--panel-bg);
    border-radius: 6px;
    padding: 30px 40px;

    .state {
      color: var(--text-secondary);
      font-size: 14px;
      text-align: center;
      padding: 60px 0;
    }

    .header {
      h2 {
        margin: 0 0 10px;
        font-size: 24px;
        color: var(--text);
      }

      .time {
        color: var(--text-muted);
        font-size: 14px;
      }

      border-bottom: 1px solid var(--panel-border);
      padding-bottom: 20px;
      margin-bottom: 20px;
    }

    .content {
      color: var(--text);
      font-size: 14px;
      line-height: 1.8;
      word-break: break-word;

      :deep(img) {
        max-width: 100%;
      }
    }

    .nav-bottom {
      display: flex;
      justify-content: space-between;
      gap: 20px;
      margin-top: 30px;
      border-top: 1px solid var(--panel-border);
      padding-top: 20px;

      span {
        font-size: 14px;
        color: var(--text-secondary);
      }

      p {
        margin: 6px 0 0;
        color: var(--accent);
        font-size: 14px;
      }

      .next {
        margin-left: auto;
        text-align: right;
      }
    }
  }
}

@media screen and (max-width: 768px) {
  .notice-item .main {
    flex-direction: column;

    .sidebar {
      width: 100%;
    }
  }
}
</style>
