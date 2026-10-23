import { computed, ref } from 'vue'

export type Theme = 'light' | 'dark'

const storageKey = 'radar-theme'

// jsdom and some embedded WebViews expose no working localStorage; fall back
// to an in-memory map so theme state never crashes the app.
const memoryStorage = new Map<string, string>()

function safeStorage(): Pick<Storage, 'getItem' | 'setItem'> {
  try {
    if (typeof window !== 'undefined' && window.localStorage) return window.localStorage
  } catch {
    // access can throw in locked-down environments; use memory storage
  }
  return {
    getItem: (key) => (memoryStorage.has(key) ? (memoryStorage.get(key) as string) : null),
    setItem: (key, value) => {
      memoryStorage.set(key, value)
    },
  }
}

export function readSavedTheme(): string | null {
  return safeStorage().getItem(storageKey)
}

export function resolveInitialTheme(
  storage: Pick<Storage, 'getItem'>,
  prefersDark: boolean,
): Theme {
  const saved = storage.getItem(storageKey)
  if (saved === 'light' || saved === 'dark') return saved
  return prefersDark ? 'dark' : 'light'
}

const systemPrefersDark =
  typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)').matches
    : false

export const theme = ref<Theme>(resolveInitialTheme(safeStorage(), systemPrefersDark))
export const isDark = computed(() => theme.value === 'dark')

export function applyTheme(value: Theme): void {
  theme.value = value
  document.documentElement.classList.toggle('dark', value === 'dark')
  safeStorage().setItem(storageKey, value)
}

export function initTheme(): void {
  applyTheme(theme.value)
}

export function toggleTheme(): void {
  applyTheme(theme.value === 'dark' ? 'light' : 'dark')
}
