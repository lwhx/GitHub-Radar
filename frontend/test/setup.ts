// vitest 全局 setup：jsdom 在部分平台（如 Windows 本地）不暴露 window.localStorage，
// 这里补一个内存实现，保证依赖持久化的测试在所有环境行为一致。
if (typeof window !== 'undefined' && !window.localStorage) {
  const memory = new Map<string, string>()
  const storage: Storage = {
    get length() {
      return memory.size
    },
    clear: () => memory.clear(),
    getItem: (key) => (memory.has(key) ? (memory.get(key) as string) : null),
    key: (index) => Array.from(memory.keys())[index] ?? null,
    removeItem: (key) => {
      memory.delete(key)
    },
    setItem: (key, value) => {
      memory.set(key, String(value))
    },
  }
  Object.defineProperty(window, 'localStorage', { value: storage, configurable: true })
}

export {}
