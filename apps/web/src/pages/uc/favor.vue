<template>
  <div class="uc-favor">
    <h1>Favorite Markets</h1>
    <el-table :data="rows" :row-key="(row: FavorRow) => row.market">
      <el-table-column prop="market" label="Market" min-width="140" />
      <el-table-column label="Last price" min-width="130">
        <template #default="{ row }: { row: FavorRow }">{{ row.last }}</template>
      </el-table-column>
      <el-table-column label="Action" width="120">
        <template #default="{ row }: { row: FavorRow }">
          <el-button link type="danger" size="small" @click="remove(row.market)">Remove</el-button>
          <NuxtLink :to="row.path" class="uc-favor__trade">Trade</NuxtLink>
        </template>
      </el-table-column>
    </el-table>
    <p v-if="rows.length === 0" class="uc-favor__muted">
      No favorite Markets yet. Star a Market from the home page or the trading page.
    </p>
  </div>
</template>

<script setup lang="ts">
definePageMeta({ middleware: 'member' })

interface FavorRow {
  market: string
  last: string
  path: string
}

const favorites = useFavorites()
const { apiFetch } = useApi()

const rows = ref<FavorRow[]>([])

useHead({ title: 'Favorites' })

function remove(market: string): void {
  favorites.remove(market)
  rows.value = rows.value.filter((row) => row.market !== market)
}

onMounted(() => {
  favorites.load()
  void (async () => {
    let tickers: Record<string, { last?: string }> = {}
    try {
      tickers = await apiFetch<Record<string, { last?: string }>>('/market/tickers')
    } catch {
      tickers = {}
    }
    rows.value = useState<string[]>('favorites:markets').value.map((market) => {
      const [base = '', quote = ''] = market.split('/')
      return {
        market,
        last: tickers[market]?.last ?? '—',
        path: `/exchange/${base}_${quote}`.toLowerCase(),
      }
    })
  })()
})
</script>

<style scoped lang="scss">
.uc-favor {
  h1 {
    font-size: 20px;
    margin: 0 0 16px;
  }
}

.uc-favor__muted {
  color: var(--text-secondary);
  font-size: 14px;
}

.uc-favor__trade {
  margin-left: 12px;
}
</style>
