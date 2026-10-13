<template>
  <div class="notice">
    <div class="banner">
      <span>Announcements</span>
    </div>
    <div class="main">
      <p v-if="loading" class="state">Loading announcements…</p>
      <p v-else-if="announcements.length === 0" class="state">
        No announcements have been published yet.
      </p>
      <template v-else>
        <div class="list">
          <NuxtLink
            v-for="item in announcements"
            :key="item.id"
            class="item"
            :to="`/announcement/${item.id}`"
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
import { onMounted, ref } from 'vue'

import { useContent, type ContentSummary } from '~/composables/useContent'
import { formatDate } from '~/utils/format'

// Announcements list, ported from the legacy pages/cms/Notice.vue: the
// gradient banner, the overlapping #192330 panel, dated rows, and the
// right-aligned pagination. Anonymous-readable — no route middleware.
// The api answers an absent admin-api as an empty page (content
// contract), so this page has no error UI at all: unreachable and empty
// both render the empty state.

const pageSize = 10

const { announcements: fetchAnnouncements } = useContent()

const announcements = ref<ContentSummary[]>([])
const totalElements = ref(0)
const pageNo = ref(1)
const loading = ref(true)

useHead({ title: 'Announcements' })

onMounted(() => {
  void load()
})

async function load(): Promise<void> {
  loading.value = true
  try {
    const page = await fetchAnnouncements(pageNo.value, pageSize)
    announcements.value = page.content
    totalElements.value = page.totalElements
  } catch {
    announcements.value = []
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
.notice {
  .banner {
    display: flex;
    justify-content: center;
    align-items: center;
    height: 320px;
    background: linear-gradient(to right, #001a40, #000109);
    color: var(--text);
    font-size: 40px;
  }

  .main {
    width: 70%;
    margin: -50px auto 50px;
    background-color: var(--panel-bg);
    color: var(--text);
    border-radius: 6px;
    padding: 50px 100px;
  }

  .state {
    color: var(--text-secondary);
    font-size: 14px;
    padding: 20px 0;
  }

  .list {
    font-size: 14px;

    .item {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 20px;
      line-height: 50px;
      height: 50px;
      border-bottom: 1px solid var(--panel-border);
      cursor: pointer;

      .text {
        display: inline-block;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        max-width: 70%;

        &:hover {
          color: var(--accent);
        }
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
  .notice .main {
    width: 90%;
    padding: 30px 20px;
  }
}
</style>
