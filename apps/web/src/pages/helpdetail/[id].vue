<template>
  <div class="helpdetail">
    <div class="route-wrap">
      <NuxtLink to="/help">Help center</NuxtLink>
      <span class="sep">&gt;</span>
      <NuxtLink v-if="categoryTitle !== ''" :to="`/helplist/${article?.cate ?? cate}`">
        {{ categoryTitle }}
      </NuxtLink>
      <span v-else>Help articles</span>
    </div>
    <div class="main">
      <aside class="menu">
        <p class="menu-title">Other articles in this category</p>
        <div class="titles">
          <NuxtLink
            v-for="item in siblings"
            :key="item.id"
            class="route"
            :class="{ active: item.id === article?.id }"
            :to="`/helpdetail/${item.id}`"
          >
            <span>{{ item.title }}</span>
          </NuxtLink>
        </div>
        <NuxtLink v-if="categoryTitle !== ''" class="more" :to="`/helplist/${cate}`">
          View all articles
        </NuxtLink>
      </aside>
      <article class="content-wrap">
        <p v-if="loading" class="state">Loading this article…</p>
        <p v-else-if="article === null" class="state">
          This help article does not exist or is no longer available.
        </p>
        <template v-else>
          <h3 class="title">{{ article.title }}</h3>
          <p class="time">{{ formatDate(article.createTime) }}</p>
          <!-- CMS-stored HTML from the api; sanitized before it reaches v-html. -->
          <div class="content" v-html="sanitizedBody"></div>
        </template>
      </article>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref, watch } from 'vue'

import {
  useContent,
  type HelpArticle,
  type HelpCategorySummary,
  type ContentSummary,
} from '~/composables/useContent'
import { formatDate } from '~/utils/format'
import { sanitizeCmsHtml } from '~/utils/sanitize'

// Help article detail, ported from the legacy pages/cms/HelpDetail.vue:
// the category sidebar beside the article. The contract cuts the legacy
// pinned-article (置顶) sidebar — the overview's category items ARE the
// siblings — and the QR share + app-download band are cut (spec #24 cut
// list). Anonymous-readable — no route middleware; a 404 (including the
// degraded-admin-api case) renders the not-found sentence, never an
// error.

const props = defineProps<{ id: string }>()

const { helpArticle, helpCategories } = useContent()

const article = ref<HelpArticle | null>(null)
const categories = ref<HelpCategorySummary[]>([])
const loading = ref(true)

const cate = computed(() => article.value?.cate ?? '')
const category = computed(() =>
  categories.value.find((entry) => entry.cate === cate.value) ?? null,
)
const categoryTitle = computed(() => category.value?.title ?? '')
const siblings = computed<ContentSummary[]>(() => category.value?.items ?? [])
const sanitizedBody = computed(() =>
  article.value === null ? '' : sanitizeCmsHtml(article.value.content),
)

useHead({ title: computed(() => article.value?.title ?? 'Help article') })

onMounted(() => {
  void load()
  void loadOverview()
})

watch(
  () => props.id,
  () => void load(),
)

async function load(): Promise<void> {
  loading.value = true
  try {
    article.value = await helpArticle(props.id)
  } catch {
    article.value = null
  } finally {
    loading.value = false
  }
}

async function loadOverview(): Promise<void> {
  try {
    categories.value = await helpCategories()
  } catch {
    categories.value = []
  }
}
</script>

<style scoped lang="scss">
.helpdetail {
  padding: 40px 0 60px;

  .route-wrap {
    width: 90%;
    max-width: 1400px;
    margin: 0 auto 20px;
    font-size: 14px;

    a {
      color: var(--accent);
    }

    .sep {
      color: var(--text-muted);
      margin: 0 8px;
    }

    span {
      color: var(--text-secondary);
    }
  }

  .main {
    display: flex;
    gap: 30px;
    width: 90%;
    max-width: 1400px;
    margin: 0 auto;
    align-items: flex-start;
  }

  .menu {
    flex: none;
    width: 22%;
    background: var(--panel-bg);
    border-radius: 6px;
    padding: 20px;

    .menu-title {
      color: var(--text-secondary);
      font-size: 14px;
      margin: 0 0 10px;
    }

    .titles .route {
      display: block;
      padding: 12px 0;
      font-size: 14px;
      line-height: 1.4;
      color: var(--text-secondary);
      border-bottom: 1px solid var(--panel-border);

      &:hover {
        color: var(--accent);
      }

      &.active {
        color: var(--accent);
      }
    }

    .more {
      display: block;
      margin-top: 12px;
      font-size: 14px;
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

    .title {
      margin: 0 0 10px;
      font-size: 24px;
      color: var(--text);
    }

    .time {
      color: var(--text-muted);
      font-size: 14px;
      margin: 0 0 20px;
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
  }
}

@media screen and (max-width: 768px) {
  .helpdetail .main {
    flex-direction: column;

    .menu {
      width: 100%;
    }
  }
}
</style>
