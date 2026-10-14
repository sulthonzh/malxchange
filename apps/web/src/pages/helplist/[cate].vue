<template>
  <div class="helplist">
    <div class="route-wrap">
      <NuxtLink to="/help">Help center</NuxtLink>
      <span class="sep">&gt;</span>
      <span>{{ categoryTitle }}</span>
    </div>
    <div class="container">
      <h1 v-if="categoryTitle !== ''">{{ categoryTitle }}</h1>
      <p v-if="loading" class="state">Loading articles…</p>
      <p v-else-if="articles.length === 0" class="state">
        There are no articles in this category yet.
      </p>
      <template v-else>
        <div class="list">
          <NuxtLink
            v-for="item in articles"
            :key="item.id"
            class="item"
            :to="`/helpdetail/${item.id}`"
          >
            <span class="text">{{ item.title }}</span>
            <span class="time">{{ formatDate(item.createTime) }}</span>
          </NuxtLink>
        </div>
        <div class="page">
          <el-pagination
            v-if="totalElements > pageSize"
            background
            layout="prev, pager, next"
            :total="totalElements"
            :page-size="pageSize"
            :current-page="pageNo"
            @current-change="go"
          />
        </div>
      </template>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref, watch } from 'vue'

import { useContent, type ContentSummary } from '~/composables/useContent'
import { formatDate } from '~/utils/format'

// One help category's article listing, ported from the legacy
// pages/cms/HelpList.vue: the "Help center > category" breadcrumb, the
// category heading, dated rows, and right-aligned pagination. The legacy
// cate/cateTitle query pair dies with the contract: the route param is
// the opaque category id, and the title is looked up in the help
// overview (the listing endpoint answers rows, not names). Anonymous-
// readable — no route middleware; empty or unreachable renders the
// empty state, never an error (content contract).

definePageMeta({ props: true })

const props = defineProps<{ cate: string }>()

const pageSize = 10

const { helpCategories, helpCategory } = useContent()

const articles = ref<ContentSummary[]>([])
const totalElements = ref(0)
const pageNo = ref(1)
const categoryTitle = ref('')
const loading = ref(true)

useHead({ title: computed(() => categoryTitle.value || 'Help articles') })

onMounted(() => {
  void loadTitle()
  void load()
})

watch(
  () => props.cate,
  () => {
    pageNo.value = 1
    void loadTitle()
    void load()
  },
)

async function loadTitle(): Promise<void> {
  try {
    const categories = await helpCategories()
    categoryTitle.value = categories.find((entry) => entry.cate === props.cate)?.title ?? ''
  } catch {
    categoryTitle.value = ''
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const page = await helpCategory(props.cate, pageNo.value, pageSize)
    articles.value = page.content
    totalElements.value = page.totalElements
  } catch {
    articles.value = []
    totalElements.value = 0
  } finally {
    loading.value = false
  }
}

function go(page: number): void {
  pageNo.value = page
  void load()
}
</script>

<style scoped lang="scss">
.helplist {
  width: 100%;
  margin: 0 auto;
  padding: 40px 20% 80px;
  min-height: 800px;

  .route-wrap {
    font-size: 14px;
    margin-bottom: 20px;

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

  .container {
    background: var(--panel-bg);
    border-radius: 6px;
    padding: 30px 50px;

    > h1 {
      text-align: center;
      margin: 30px 0 20px;
      font-size: 28px;
      color: var(--text);
    }

    .state {
      color: var(--text-secondary);
      font-size: 14px;
      text-align: center;
      padding: 60px 0;
    }
  }

  .list {
    font-size: 16px;

    .item {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 20px;
      line-height: 40px;
      border-bottom: 1px solid var(--panel-border);
      cursor: pointer;
      color: var(--text-secondary);

      &:hover {
        color: var(--accent);
      }

      .text {
        display: inline-block;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }

      .time {
        flex: none;
        color: var(--text-muted);
        font-size: 14px;
      }
    }
  }

  .page {
    text-align: right;
    margin-top: 20px;
  }
}

@media screen and (max-width: 768px) {
  .helplist {
    padding: 20px 4% 40px;

    .container {
      padding: 20px;
    }
  }
}
</style>
