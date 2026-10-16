<template>
  <div class="help">
    <div class="banner">
      <span>Help center</span>
    </div>
    <div class="help-container">
      <p v-if="loading" class="state">Loading the help center…</p>
      <p v-else-if="categories.length === 0" class="state">
        The help center has no articles yet.
      </p>
      <div v-else class="main">
        <section
          v-for="category in categories"
          :key="category.cate"
          class="section"
        >
          <h3>{{ category.title }}</h3>
          <div class="list-wrap">
            <p v-if="category.items.length === 0" class="empty-category">
              There are no articles in this category yet.
            </p>
            <NuxtLink
              v-for="item in category.items"
              :key="item.id"
              class="item"
              :title="item.title"
              :to="`/helpdetail/${item.id}`"
            >
              <span class="text">{{ item.title }}</span>
            </NuxtLink>
          </div>
          <div class="route-wrap">
            <NuxtLink :to="`/helplist/${category.cate}`">View all articles</NuxtLink>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { onMounted, ref } from 'vue'

import { useContent, type HelpCategorySummary } from '~/composables/useContent'

// Help center landing, ported from the legacy pages/cms/Help.vue: the
// banner band and the two-column card grid — one card per category with
// its article links and a link into the full listing. The legacy photo
// banner becomes the ported gradient band (the cut list drops its
// image-only chrome); the lang-forked titles die with English-only v1
// (ADR-0013). Anonymous-readable — no route middleware. An absent
// admin-api answers [] (content contract), so no error UI exists here.

const { helpCategories } = useContent()

const categories = ref<HelpCategorySummary[]>([])
const loading = ref(true)

useHead({ title: 'Help center' })

onMounted(() => {
  void load()
})

async function load(): Promise<void> {
  loading.value = true
  try {
    categories.value = await helpCategories()
  } catch {
    categories.value = []
  } finally {
    loading.value = false
  }
}
</script>

<style scoped lang="scss">
.help {
  padding-bottom: 50px;

  .banner {
    display: flex;
    justify-content: center;
    align-items: center;
    height: 320px;
    background: linear-gradient(to right, #f8fafc, #e2e8f0);
    color: var(--text);
    font-size: 40px;
  }

  .help-container {
    width: 76%;
    margin: -50px auto 0;
    background: var(--panel-bg);
    border-radius: 6px;
    padding: 60px 6%;

    .state {
      color: var(--text-secondary);
      font-size: 14px;
      padding: 40px 0;
    }
  }

  .main {
    display: flex;
    justify-content: space-between;
    flex-direction: row;
    flex-wrap: wrap;

    .section {
      width: 40%;
      font-size: 16px;
      text-align: left;
      margin: 0 20px;
      position: relative;
      padding-bottom: 30px;
      margin-bottom: 50px;

      &:nth-last-child(-n + 2) {
        margin-bottom: 0;
      }

      > h3 {
        font-size: 24px;
        line-height: 1;
        padding: 30px 0;
        color: var(--text);
        margin: 0;
      }

      .item {
        display: block;
        position: relative;
        padding: 16px 0;
        line-height: 1.4;
        color: var(--text-secondary);
        border-bottom: 1px solid var(--panel-border);

        .text {
          display: inline-block;
          max-width: calc(100% - 25px);
          white-space: nowrap;
          text-overflow: ellipsis;
          overflow: hidden;
        }
      }

      .empty-category {
        color: var(--text-muted);
        font-size: 14px;
        padding: 16px 0;
        border-bottom: 1px solid var(--panel-border);
      }

      .route-wrap {
        position: absolute;
        bottom: 0;
        right: 0;
        font-size: 14px;

        a {
          color: var(--accent);
        }
      }
    }
  }
}

@media screen and (max-width: 768px) {
  .help .help-container {
    width: 90%;
    padding: 30px 5%;

    .main .section {
      width: 100%;
      margin: 0 0 40px;
    }
  }
}
</style>
