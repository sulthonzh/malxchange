// Favorites (spec #24, story 18): the Member watchlist behind the market
// table's star column and the exchange page's Favorites tab. Signed-in
// members get the api-owned watchlist (GET/PUT /uc/favorites, full-list
// sync); anonymous visitors keep localStorage, which also survives as
// the pre-login view. The two stores never merge — signing in replaces
// whatever the device had with the member's list, the same full-sync
// contract the api exposes.
import { watch } from 'vue'

const FAVORITES_KEY = 'web.favorites'

export function useFavorites() {
  const markets = useState<string[]>('favorites:markets', () => [])
  const loaded = useState('favorites:loaded', () => false)
  const syncing = useState('favorites:syncing', () => false)
  const { token, loggedIn } = useSession()
  const { apiFetch } = useApi()

  function readStored(): string[] {
    try {
      const raw = window.localStorage.getItem(FAVORITES_KEY)
      if (raw === null) {
        return []
      }
      const parsed: unknown = JSON.parse(raw)
      if (!Array.isArray(parsed)) {
        return []
      }
      return parsed.filter((item): item is string => typeof item === 'string')
    } catch {
      // Corrupt or unreadable storage behaves like an empty watchlist.
      return []
    }
  }

  function persist(): void {
    window.localStorage.setItem(FAVORITES_KEY, JSON.stringify(markets.value))
  }

  async function load(): Promise<void> {
    if (loaded.value || !import.meta.client) {
      return
    }
    loaded.value = true
    if (!loggedIn.value) {
      markets.value = readStored()
      return
    }
    try {
      const body = await apiFetch<{ markets: string[] }>('/uc/favorites')
      markets.value = body.markets
    } catch {
      // The watchlist is decorative; an unreachable api keeps the
      // device-local list rather than blanking the star column.
      markets.value = readStored()
    }
  }

  /** Pushes the whole list — the api contract is full-sync, not merge. */
  async function sync(): Promise<void> {
    if (!loggedIn.value || syncing.value) {
      return
    }
    syncing.value = true
    try {
      await apiFetch('/uc/favorites', { method: 'PUT', body: { markets: markets.value } })
    } catch {
      // A failed sync keeps the local view; the next change retries.
    } finally {
      syncing.value = false
    }
  }

  function has(market: string): boolean {
    return markets.value.includes(market)
  }

  function add(market: string): void {
    if (has(market)) {
      return
    }
    markets.value = [...markets.value, market]
    if (loggedIn.value) {
      void sync()
    } else {
      persist()
    }
  }

  function remove(market: string): void {
    markets.value = markets.value.filter((item) => item !== market)
    if (loggedIn.value) {
      void sync()
    } else {
      persist()
    }
  }

  /** Returns true when the market is a favorite after the toggle. */
  function toggle(market: string): boolean {
    if (has(market)) {
      remove(market)
      return false
    }
    add(market)
    return true
  }

  if (import.meta.client) {
    // A session appearing mid-visit (login) adopts the member's list;
    // the token arriving also flips apiFetch onto the authed header.
    watch(token, (now, before) => {
      if (now && !before) {
        void loadFreshAfterLogin()
      }
    })
  }

  async function loadFreshAfterLogin(): Promise<void> {
    loaded.value = false
    await load()
  }

  return { markets, load, has, add, remove, toggle }
}
