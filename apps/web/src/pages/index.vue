<template>
  <div>
    <div v-if="announcements.length > 0" class="notice-bar">
      <div class="topnav">
        <div class="carl">
          <div class="notice-list">
            <div v-for="item in announcements" :key="item.id" class="notice-item">
              <NuxtLink :to="`/announcement/${item.id}`">
                [ {{ formatDate(item.createTime) }} ] {{ item.title }}
              </NuxtLink>
            </div>
          </div>
          <div class="more">
            <NuxtLink to="/notice">More</NuxtLink>
          </div>
        </div>
      </div>
    </div>

    <div class="banner-panel">
      <img class="banner-img" src="~/assets/images/bannerbg.png" alt="" />
      <p class="slogan">Exchange The Best</p>
      <p class="subslogan">Trade every listed Coin on one open Market.</p>
    </div>

    <div class="agent-panel-wrapper">
      <div class="agent-panel">
        <div class="title">
          <div class="gettingstarted">Get started</div>
          <div class="tips">Four steps to your first order.</div>
        </div>
        <div class="agent-list">
          <NuxtLink v-for="step in steps" :key="step.title" :to="step.to" class="agent-item">
            <div class="agent-img">
              <img :src="step.image" alt="" />
            </div>
            <div class="agent-detail">
              <p class="agent-name">{{ step.title }}</p>
              <p class="agent-count">{{ step.tip }}</p>
            </div>
          </NuxtLink>
        </div>
      </div>
    </div>

    <div class="markets">
      <div class="page2nav">
        <ul class="brclearfix">
          <li
            v-for="tab in tabs"
            :key="tab"
            :class="{ active: tab === activeTab }"
            @click="activeTab = tab"
          >
            {{ tab }}
          </li>
          <li class="search-box">
            <el-input
              v-model="searchKey"
              placeholder="Enter a Coin symbol..."
              clearable
            />
          </li>
        </ul>
      </div>
      <div class="ptjy">
        <el-table :data="visibleRows" :empty-text="emptyText">
          <el-table-column width="60" align="center">
            <template #header>
              <el-icon :size="18" color="#f0a70a"><Star /></el-icon>
            </template>
            <template #default="{ row }">
              <el-icon
                :size="18"
                :color="favorites.has(row.market) ? '#f0a70a' : '#61688a'"
                class="favor-star"
                @click.stop="toggleFavor(row.market)"
              >
                <StarFilled v-if="favorites.has(row.market)" />
                <Star v-else />
              </el-icon>
            </template>
          </el-table-column>
          <el-table-column label="Market" align="center" min-width="110">
            <template #default="{ row }">
              <NuxtLink class="market-name" :to="`/exchange/${row.exchangePath}`">
                {{ row.base }}<span class="market-quote">/{{ row.quote }}</span>
              </NuxtLink>
            </template>
          </el-table-column>
          <el-table-column label="Last price" align="center" min-width="150" sortable :sort-method="byLast">
            <template #default="{ row }">
              {{ priceText(row.last, row.quoteScale) }}
            </template>
          </el-table-column>
          <el-table-column label="24h high" align="center" sortable :sort-method="byHigh">
            <template #default="{ row }">
              {{ priceText(row.high, row.quoteScale) }}
            </template>
          </el-table-column>
          <el-table-column label="24h low" align="center" sortable :sort-method="byLow">
            <template #default="{ row }">
              {{ priceText(row.low, row.quoteScale) }}
            </template>
          </el-table-column>
          <el-table-column label="24h turnover" align="center" sortable :sort-method="byTurnover">
            <template #default="{ row }">
              {{ priceText(row.turnover, row.quoteScale) }}
            </template>
          </el-table-column>
          <el-table-column align="center" width="100">
            <template #default="{ row }">
              <NuxtLink class="trade-link" :to="`/exchange/${row.exchangePath}`">Trade</NuxtLink>
            </template>
          </el-table-column>
        </el-table>
        <p v-if="loadError" class="load-error">{{ loadError }}</p>
      </div>
    </div>

    <div class="section brand">
      <p class="title">About the exchange</p>
      <p class="subtitle">Fair, open, and exact to the last digit.</p>
      <div class="detail">
        The matching engine records every price, amount, and fee as an exact
        decimal, so what you submit is what you get and no balance is ever
        rounded up for display.
      </div>
      <div class="detail">
        Members sign in with an email address and a per-device session, browse
        every Market anonymously before signing up, and stay in control of
        where their session stays valid.
      </div>
    </div>

    <div class="section features">
      <ul>
        <li v-for="feature in features" :key="feature.title">
          <div>
            <img :src="feature.image" alt="" />
          </div>
          <p class="feature-title">{{ feature.title }}</p>
          <p>{{ feature.text }}</p>
        </li>
      </ul>
    </div>
  </div>
</template>

<script setup lang="ts">
import { Star, StarFilled } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'

import depositImg from '~/assets/images/new_2.png'
import firstOrderImg from '~/assets/images/new_3.png'
import accountImg from '~/assets/images/new_1usd.png'
import recordsImg from '~/assets/images/new_4.png'
import chooseImg from '~/assets/images/feature_choose.png'
import fastImg from '~/assets/images/feature_fast.png'
import globalImg from '~/assets/images/feature_global.png'
import safeImg from '~/assets/images/feature_safe.png'
import { formatDate, formatMoneyEnUs } from '~/utils/format'
import { compareMoney } from '~/utils/money'

// Ported home page from the legacy pages/index/Index.vue, kept-list only:
// the announcement strip, banner, getting-started row, market table,
// brand section, and features. The legacy activity swiper, app-download
// band, CNY conversions, and innovation tabs are cut (spec #24).

// Paths under the api host (spec #24 keeps the api the only public
// member surface; it proxies the market read surface). The announcements
// path is the pinned content route (content contract, ticket #45): a
// paged document, of which the strip shows the first page.
const MARKETS_PATH = '/market/markets'
const TICKERS_PATH = '/market/tickers'
const ANNOUNCEMENTS_PATH = '/announcements?pageNo=1&pageSize=5'

interface MarketInfo {
  market: string
  baseCoin: string
  quoteCoin: string
  zone: number
  sort: number
  featured: boolean
  baseScale: number
  quoteScale: number
  tickSize: string
  lotSize: string
}

interface Ticker {
  market: string
  last: string
  high: string
  low: string
  turnover: string
}

interface Announcement {
  id: string
  title: string
  createTime: string
}

interface AnnouncementPage {
  content: Announcement[]
  totalElements: number
}

// One row per Market: the static config joined with its rolling 24h
// summary. Every money field stays a decimal string end to end
// (ADR-0005) — only the render path formats it.
interface MarketRow {
  market: string
  base: string
  quote: string
  quoteScale: number
  last: string
  high: string
  low: string
  turnover: string
  exchangePath: string
}

const steps = [
  {
    to: '/register',
    image: accountImg,
    title: 'Open an account',
    tip: 'Sign up with your email address in a few minutes.',
  },
  {
    to: '/help',
    image: depositImg,
    title: 'Fund your account',
    tip: 'Request a deposit address for the Coin you want to trade.',
  },
  {
    to: '/help',
    image: firstOrderImg,
    title: 'Place your first order',
    tip: 'Buy or sell in any listed Market.',
  },
  {
    to: '/help',
    image: recordsImg,
    title: 'Follow your records',
    tip: 'Review your balances, orders, and fills in one place.',
  },
] as const

const features = [
  {
    image: safeImg,
    title: 'Security',
    text: 'Balances live in an audited Ledger, and every movement appends to a Journal you can review.',
  },
  {
    image: fastImg,
    title: 'Always open',
    text: 'Markets run around the clock, and every Fill reaches your order history as it happens.',
  },
  {
    image: globalImg,
    title: 'Global exchange',
    text: 'Trade every listed Coin with Members anywhere through one shared order Book.',
  },
  {
    image: chooseImg,
    title: 'Careful listings',
    text: 'Every Coin is reviewed before it is listed, so you trade known assets.',
  },
] as const

const { apiFetch, apiBaseUrl } = useApi()
const favorites = useFavorites()

const rows = ref<MarketRow[]>([])
const announcements = ref<Announcement[]>([])
const activeTab = ref('')
const searchKey = ref('')
const loading = ref(false)
const loadError = ref('')

const FAVORITES_TAB = 'Favorites'

const tabs = computed(() => {
  const quoteCoins = [...new Set(rows.value.map((row) => row.quote))]
  return [FAVORITES_TAB, ...quoteCoins]
})

const visibleRows = computed(() => {
  const tab = activeTab.value === '' ? (tabs.value[1] ?? FAVORITES_TAB) : activeTab.value
  const source =
    tab === FAVORITES_TAB
      ? rows.value.filter((row) => favorites.has(row.market))
      : rows.value.filter((row) => row.quote === tab)
  const key = searchKey.value.trim().toUpperCase()
  if (key === '') {
    return source
  }
  return source.filter(
    (row) => row.base.toUpperCase().startsWith(key) || row.market.toUpperCase().startsWith(key),
  )
})

const emptyText = computed(() => {
  if (loading.value) {
    return 'Loading Markets…'
  }
  if (searchKey.value.trim() !== '') {
    return 'No Markets match the Coin symbol you entered.'
  }
  if (activeTab.value === FAVORITES_TAB) {
    return 'You have not added any Markets to your favorites yet.'
  }
  return 'No Markets are listed yet.'
})

useHead({ title: 'Home' })

onMounted(() => {
  favorites.load()
  if (apiBaseUrl === '') {
    loadError.value =
      'The api host is not configured, so live market data is unavailable right now.'
    return
  }
  loading.value = true
  void loadMarkets()
  void loadAnnouncements()
})

async function loadMarkets(): Promise<void> {
  try {
    const [markets, tickers] = await Promise.all([
      apiFetch<MarketInfo[]>(MARKETS_PATH),
      apiFetch<Record<string, Ticker>>(TICKERS_PATH),
    ])
    rows.value = markets.map((info) => {
      const ticker = tickers[info.market]
      return {
        market: info.market,
        base: info.baseCoin,
        quote: info.quoteCoin,
        quoteScale: info.quoteScale,
        last: ticker?.last ?? '',
        high: ticker?.high ?? '',
        low: ticker?.low ?? '',
        turnover: ticker?.turnover ?? '0',
        exchangePath: `${info.baseCoin}_${info.quoteCoin}`.toLowerCase(),
      }
    })
    if (activeTab.value === '' && rows.value.length > 0) {
      activeTab.value = rows.value[0]!.quote
    }
    loadError.value = ''
  } catch (error) {
    loadError.value =
      error instanceof Error ? error.message : 'The market list could not be loaded.'
  } finally {
    loading.value = false
  }
}

async function loadAnnouncements(): Promise<void> {
  try {
    const page = await apiFetch<AnnouncementPage>(ANNOUNCEMENTS_PATH)
    announcements.value = page.content
  } catch {
    // The strip stays hidden when announcements cannot be loaded.
  }
}

// Money renders only through the decimal helpers (ADR-0005 / ADR-0013):
// floored to the Market's quote scale with en-US grouping, and an em dash
// while a Market has no in-window Fills to summarize.
function priceText(value: string, scale: number): string {
  return value === '' ? '—' : formatMoneyEnUs(value, scale)
}

function toggleFavor(market: string): void {
  const isFavorite = favorites.toggle(market)
  ElMessage({
    message: isFavorite
      ? `${market} was added to your favorites.`
      : `${market} was removed from your favorites.`,
    grouping: true,
  })
}

// el-table sort handlers compare decimal strings by value, never by
// string order (ADR-0005).
const byLast = (a: MarketRow, b: MarketRow): number => compareMoney(a.last || '0', b.last || '0')
const byHigh = (a: MarketRow, b: MarketRow): number => compareMoney(a.high || '0', b.high || '0')
const byLow = (a: MarketRow, b: MarketRow): number => compareMoney(a.low || '0', b.low || '0')
const byTurnover = (a: MarketRow, b: MarketRow): number =>
  compareMoney(a.turnover || '0', b.turnover || '0')
</script>

<style scoped lang="scss">
.notice-bar {
  background: var(--home-section-bg);
  border-bottom: 1px solid rgb(28, 39, 58);
  padding: 0 10%;
  overflow: hidden;

  .topnav {
    width: 100%;
    line-height: 40px;
    height: 40px;
    margin: 0 auto;

    .carl {
      width: 100%;
      height: 40px;
      position: relative;
      display: flex;
      justify-content: space-between;

      .notice-list {
        width: 100%;
        display: flex;
        justify-content: center;
        height: 40px;

        .notice-item {
          max-width: 25%;
          padding: 0 30px;
          position: relative;

          a {
            color: var(--text-secondary);
            font-size: 12px;
          }

          &:not(:last-child)::after {
            content: '/';
            position: absolute;
            right: 0;
            top: 1px;
            color: #afafaf;
          }
        }
      }

      .more {
        position: absolute;
        right: 0;

        a {
          color: var(--accent);
          font-size: 12px;
          padding: 3px 12px;
          border-radius: 3px;
        }
      }
    }
  }
}

.banner-panel {
  height: 400px;
  background-color: var(--home-section-bg);
  overflow: hidden;
  position: relative;

  .banner-img {
    height: 100%;
    width: 100%;
    object-fit: cover;
    display: block;
  }

  .slogan {
    text-align: center;
    font-size: 40px;
    color: var(--text);
    position: absolute;
    top: 70px;
    width: 100%;
    letter-spacing: 5px;
    text-shadow: 0 0 10px #000000;
    margin: 0;
  }

  .subslogan {
    text-align: center;
    font-size: 20px;
    color: var(--text-secondary);
    position: absolute;
    top: 130px;
    width: 100%;
    letter-spacing: 2px;
    margin: 0;
  }
}

.agent-panel-wrapper {
  background: var(--site-header-bg);

  .agent-panel {
    display: flex;
    justify-content: center;
    min-width: 1200px;
    padding: 0 10%;

    .title {
      margin-right: 10px;
      width: 220px;
      padding: 10px 0;
      border-right: 1px solid rgb(28, 44, 72);
      letter-spacing: 3px;

      .gettingstarted {
        color: var(--text);
      }

      .tips {
        font-size: 10px;
        color: #869ec9;
        letter-spacing: 2px;
        margin-top: 5px;
      }
    }

    .agent-list {
      padding: 4px 0;
      height: 62px;
      display: flex;
      overflow: hidden;

      .agent-item {
        height: 54px;
        background: #151f2c;
        width: 210px;
        margin-left: 10px;
        padding-right: 15px;
        border: 1px solid #151f2c;
        transition: all 0.5s;
        display: flex;

        &:hover {
          border: 1px solid rgb(240, 185, 11);
        }

        .agent-img {
          padding-top: 7px;
          margin-left: 7px;

          img {
            height: 40px;
            width: 40px;
            border-radius: 40px;
          }
        }

        .agent-detail {
          padding-top: 10px;
          margin-left: 10px;
          max-width: 130px;

          .agent-name {
            font-size: 13px;
            color: var(--accent-border);
            overflow: hidden;
            text-overflow: ellipsis;
            white-space: nowrap;
            margin: 0;
          }

          .agent-count {
            font-size: 10px;
            color: rgb(103, 122, 153);
            margin-top: 5px;
            overflow: hidden;
            text-overflow: ellipsis;
            white-space: nowrap;
            margin-bottom: 0;
          }
        }
      }
    }
  }
}

.markets {
  background: var(--home-section-bg);
  min-height: 320px;
  padding: 40px 14%;

  .page2nav {
    line-height: 50px;
    font-size: 20px;
    background: var(--home-tabs-bg);
    min-width: 864px;
    display: flex;

    .brclearfix {
      width: 100%;
      margin: 0;
      padding: 0;
      display: flex;

      li {
        float: left;
        cursor: pointer;
        color: var(--text);
        background: var(--home-tabs-bg);
        list-style: none;
        font-size: 16px;
        padding: 5px 40px;

        &:hover {
          background: var(--home-tabs-hover-bg);
        }

        &.active {
          background: var(--panel-header-bg);
          color: var(--accent);
          border-bottom: 2px solid var(--accent);
        }

        &.search-box {
          margin-left: auto;
          padding: 8px 6px;
          line-height: normal;
          cursor: default;

          &:hover {
            background: var(--home-tabs-bg);
          }

          :deep(.el-input__wrapper) {
            border-radius: 20px;
            background: var(--home-tabs-bg);
            box-shadow: 0 0 0 1px var(--panel-border) inset;
          }

          :deep(.el-input__inner) {
            color: var(--text);
          }
        }
      }
    }
  }

  .ptjy {
    min-width: 864px;

    .favor-star {
      cursor: pointer;
    }

    .market-name {
      color: var(--text);

      .market-quote {
        color: var(--text-muted);
      }
    }

    .trade-link {
      color: var(--accent);
      display: inline-block;
      padding: 2px 8px;
    }

    .load-error {
      color: var(--text-secondary);
      font-size: 12px;
      margin: 10px 4px 0;
    }
  }
}

.section.brand {
  background: #222e3d;
  min-height: 460px;
  padding: 80px 14%;

  .title {
    font-size: 30px;
    text-align: center;
    width: 100%;
    letter-spacing: 6px;
    margin: 0 0 10px;
  }

  .subtitle {
    margin-bottom: 40px;
    color: var(--text-secondary);
    font-size: 13px;
    text-align: center;
    width: 100%;
  }

  .detail {
    line-height: 40px;
    letter-spacing: 2px;
    text-indent: 45px;
    font-size: 20px;
    margin-bottom: 20px;
    color: var(--text-secondary);
    text-align: justify;
  }
}

.section.features {
  background: var(--panel-bg);
  padding: 80px 0;

  ul {
    width: 88%;
    margin: 0 auto;
    padding: 0;
    list-style-type: none;

    li {
      display: inline-block;
      width: 24%;
      padding: 0 15px;
      vertical-align: top;

      div {
        width: 130px;
        height: 130px;
        border-radius: 50%;
        text-align: center;
        margin: 0 auto;

        img {
          height: 125px;
          margin-top: 8px;
        }
      }

      p {
        font-size: 14px;
        margin: 20px 0;
        text-align: center;
        color: var(--text-secondary);
      }

      .feature-title {
        color: var(--text);
        font-size: 18px;
        font-weight: 400;
      }
    }
  }
}

@media screen and (max-width: 768px) {
  .markets {
    padding: 20px 2%;
  }

  .agent-panel-wrapper .agent-panel {
    min-width: 0;
    flex-direction: column;

    .agent-list {
      flex-direction: column;
      height: auto;

      .agent-item {
        width: auto;
        margin: 4px 0;
      }
    }
  }

  .section.features ul li {
    width: 100%;
  }
}
</style>
