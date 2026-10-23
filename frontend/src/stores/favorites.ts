import { ref } from 'vue'

export interface FavoriteRepo {
  full_name: string
  owner: string
  name: string
  html_url: string
  description: string | null
  language: string | null
  stars_count: number
  favorited_at: string
}

const storageKey = 'radar-favorites'

// jsdom 与部分内嵌 WebView 没有可用的 localStorage；与主题 store 一样回退到内存 Map。
const memoryStorage = new Map<string, string>()

function safeStorage(): Pick<Storage, 'getItem' | 'setItem'> {
  try {
    if (typeof window !== 'undefined' && window.localStorage) return window.localStorage
  } catch {
    // locked-down 环境访问 localStorage 可能抛错，退回内存存储
  }
  return {
    getItem: (key) => (memoryStorage.has(key) ? (memoryStorage.get(key) as string) : null),
    setItem: (key, value) => {
      memoryStorage.set(key, value)
    },
  }
}

function isFavoriteRepo(value: unknown): value is FavoriteRepo {
  if (typeof value !== 'object' || value === null) return false
  const candidate = value as FavoriteRepo
  return (
    typeof candidate.full_name === 'string' &&
    typeof candidate.owner === 'string' &&
    typeof candidate.name === 'string' &&
    typeof candidate.stars_count === 'number'
  )
}

function readAll(): FavoriteRepo[] {
  try {
    const raw = safeStorage().getItem(storageKey)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter(isFavoriteRepo)
  } catch {
    return []
  }
}

function writeAll(list: FavoriteRepo[]) {
  safeStorage().setItem(storageKey, JSON.stringify(list))
}

const favorites = ref<FavoriteRepo[]>(readAll())

function isFavorited(fullName: string): boolean {
  return favorites.value.some((item) => item.full_name === fullName)
}

function favoriteCount(): number {
  return favorites.value.length
}

function toggleFavorite(repo: Omit<FavoriteRepo, 'favorited_at'>): boolean {
  if (isFavorited(repo.full_name)) {
    removeFavorite(repo.full_name)
    return false
  }
  favorites.value = [{ ...repo, favorited_at: new Date().toISOString() }, ...favorites.value]
  writeAll(favorites.value)
  return true
}

function removeFavorite(fullName: string) {
  favorites.value = favorites.value.filter((item) => item.full_name !== fullName)
  writeAll(favorites.value)
}

export { favoriteCount, favorites, isFavorited, removeFavorite, toggleFavorite }
