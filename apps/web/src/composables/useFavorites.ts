// Favorites (spec #24, story 18): the Member watchlist behind the market
// table's star column and the exchange page's Favorites tab.
//
// Seam — api-backed storage: the watchlist belongs on the api side, and
// those endpoints land with the member-center ticket. Until then this
// composable persists to localStorage only, so consumers work today and
// stay unchanged by the swap: give `load`/`add`/`remove` their api
// branches (useApi() calls against the member watchlist endpoints; the
// legacy routes were /exchange/favor/{find,add,delete}) keyed on
// useSession().loggedIn, keeping localStorage as the logged-out fallback.
const FAVORITES_KEY = 'web.favorites'

export function useFavorites() {
  const symbols = useState<string[]>('favorites:symbols', () => [])
  const loaded = useState('favorites:loaded', () => false)

  function load(): void {
    if (loaded.value || !import.meta.client) {
      return
    }
    symbols.value = readStored()
    loaded.value = true
  }

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
    window.localStorage.setItem(FAVORITES_KEY, JSON.stringify(symbols.value))
  }

  function has(symbol: string): boolean {
    return symbols.value.includes(symbol)
  }

  function add(symbol: string): void {
    if (has(symbol)) {
      return
    }
    symbols.value = [...symbols.value, symbol]
    persist()
  }

  function remove(symbol: string): void {
    symbols.value = symbols.value.filter((item) => item !== symbol)
    persist()
  }

  /** Returns true when the symbol is a favorite after the toggle. */
  function toggle(symbol: string): boolean {
    if (has(symbol)) {
      remove(symbol)
      return false
    }
    add(symbol)
    return true
  }

  return { symbols, load, has, add, remove, toggle }
}
