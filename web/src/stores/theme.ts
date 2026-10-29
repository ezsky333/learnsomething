import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

export type ThemeMode = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'ls-theme'

/** 解析系统偏好，仅在 mode === 'system' 时使用。 */
function systemPrefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

export const useThemeStore = defineStore('theme', () => {
  const mode = ref<ThemeMode>(readInitialMode())
  const isDark = ref(false)

  function readInitialMode(): ThemeMode {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (saved === 'light' || saved === 'dark' || saved === 'system') return saved
    return 'system'
  }

  function apply() {
    const dark = mode.value === 'dark' || (mode.value === 'system' && systemPrefersDark())
    isDark.value = dark
    const root = document.documentElement
    root.classList.toggle('dark', dark)
    root.style.colorScheme = dark ? 'dark' : 'light'
    // 移动端地址栏配色跟随主题
    const meta = document.querySelector('meta[name="theme-color"]')
    if (meta) meta.setAttribute('content', dark ? '#070a14' : '#f5f7fb')
  }

  /** 在亮/暗之间切换（system 视作当前实际效果的反面）。 */
  function toggle() {
    mode.value = isDark.value ? 'light' : 'dark'
  }

  function set(next: ThemeMode) {
    mode.value = next
  }

  watch(mode, (v) => {
    localStorage.setItem(STORAGE_KEY, v)
    apply()
  })

  // 系统主题变化时，只有 system 模式才跟随
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (mode.value === 'system') apply()
  })

  apply()

  return { mode, isDark, toggle, set }
})
