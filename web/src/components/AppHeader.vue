<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores'
import ThemeToggle from '@/components/ThemeToggle.vue'

const auth = useAuthStore()
const route = useRoute()
const menuOpen = ref(false)

const links = [
  { name: 'dashboard', label: '看板', icon: '◎' },
  { name: 'review', label: '今日复习', icon: '↻' },
  { name: 'notes', label: '我的笔记', icon: '✎' },
]

async function onLogout() {
  await auth.logout()
  window.location.href = '/login'
}

watch(
  () => route.fullPath,
  () => {
    menuOpen.value = false
  },
)
</script>

<template>
  <header
    class="sticky top-0 z-30 border-b border-line bg-app/85 backdrop-blur-md"
    style="padding-top: env(safe-area-inset-top)"
  >
    <div class="mx-auto flex h-14 max-w-[1600px] items-center gap-3 px-4 sm:px-6">
      <RouterLink to="/" class="flex items-center gap-2 font-semibold text-fg">
        <span
          class="grid h-7 w-7 place-items-center rounded-lg bg-gradient-to-br from-brand-400 to-brand-600 text-sm font-bold text-white"
        >
          学
        </span>
        <span class="hidden sm:inline">学习进度</span>
      </RouterLink>

      <nav class="ml-2 hidden items-center gap-1 md:flex">
        <RouterLink
          v-for="link in links"
          :key="link.name"
          :to="{ name: link.name }"
          class="btn btn-ghost"
          active-class="bg-panel-soft text-fg"
        >
          <span class="text-xs opacity-70">{{ link.icon }}</span>
          {{ link.label }}
        </RouterLink>
      </nav>

      <div class="ml-auto flex items-center gap-1">
        <a
          href="https://xiaolincoding.com/interview/"
          target="_blank"
          rel="noopener noreferrer"
          class="btn btn-ghost hidden text-xs sm:inline-flex"
          title="内容来源：小林coding"
        >
          原文
        </a>
        <ThemeToggle />
        <button class="btn btn-ghost text-xs" title="退出登录" @click="onLogout">退出</button>
        <button
          class="btn btn-ghost md:hidden"
          :aria-expanded="menuOpen"
          aria-label="菜单"
          @click="menuOpen = !menuOpen"
        >
          ☰
        </button>
      </div>
    </div>

    <div v-if="menuOpen" class="border-t border-line px-4 pb-3 pt-2 md:hidden">
      <RouterLink
        v-for="link in links"
        :key="link.name"
        :to="{ name: link.name }"
        class="block rounded-lg px-3 py-2 text-sm text-fg-muted hover:bg-panel-soft"
      >
        {{ link.label }}
      </RouterLink>
      <a
        href="https://xiaolincoding.com/interview/"
        target="_blank"
        rel="noopener noreferrer"
        class="block rounded-lg px-3 py-2 text-sm text-fg-muted hover:bg-panel-soft"
      >
        原文（xiaolincoding.com）
      </a>
    </div>
  </header>
</template>
