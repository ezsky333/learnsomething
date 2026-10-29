<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useThemeStore } from '@/stores/theme'

const theme = useThemeStore()
const open = ref(false)
const root = ref<HTMLElement | null>(null)

const modes = [
  { key: 'light', label: '日间', icon: '☀' },
  { key: 'dark', label: '夜间', icon: '☾' },
  { key: 'system', label: '跟随系统', icon: '⚙' },
] as const

function pick(key: (typeof modes)[number]['key']) {
  theme.set(key)
  open.value = false
}

function onDocClick(e: MouseEvent) {
  if (root.value && !root.value.contains(e.target as Node)) open.value = false
}

onMounted(() => document.addEventListener('click', onDocClick))
onUnmounted(() => document.removeEventListener('click', onDocClick))

const currentIcon = computed(() => (theme.isDark ? '☾' : '☀'))
</script>

<template>
  <div ref="root" class="relative">
    <button
      class="btn btn-ghost px-2"
      :title="theme.isDark ? '切换到日间' : '切换到夜间'"
      aria-label="切换主题"
      @click.stop="theme.toggle()"
    >
      <span class="text-base leading-none">{{ currentIcon }}</span>
    </button>
    <button
      class="btn btn-ghost px-1 text-[10px]"
      title="主题设置"
      aria-label="主题设置"
      :aria-expanded="open"
      @click.stop="open = !open"
    >
      ▾
    </button>

    <div
      v-if="open"
      class="card absolute right-0 top-full z-50 mt-2 w-36 overflow-hidden p-1 shadow-xl"
      role="menu"
    >
      <button
        v-for="m in modes"
        :key="m.key"
        class="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs transition-colors"
        :class="theme.mode === m.key ? 'bg-panel-soft font-medium text-fg' : 'text-fg-dim hover:bg-panel-soft'"
        role="menuitemradio"
        :aria-checked="theme.mode === m.key"
        @click="pick(m.key)"
      >
        <span class="w-3 text-center">{{ m.icon }}</span>
        {{ m.label }}
      </button>
    </div>
  </div>
</template>
