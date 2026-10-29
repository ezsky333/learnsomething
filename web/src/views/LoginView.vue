<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores'
import ThemeToggle from '@/components/ThemeToggle.vue'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const password = ref('')
const loading = ref(false)
const error = ref('')

async function submit() {
  if (!password.value.trim() || loading.value) return
  loading.value = true
  error.value = ''
  try {
    await auth.login(password.value)
    const redirect = route.query.redirect
    const target = typeof redirect === 'string' && redirect.startsWith('/') ? redirect : '/'
    await router.replace(target)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '登录失败'
    password.value = ''
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="relative grid min-h-dvh place-items-center px-4 py-10">
    <div class="absolute right-4 top-4">
      <ThemeToggle />
    </div>

    <div class="w-full max-w-sm animate-rise">
      <div class="mb-8 text-center">
        <div
          class="mx-auto mb-4 grid h-14 w-14 place-items-center rounded-2xl bg-gradient-to-br from-brand-400 to-brand-600 text-2xl font-bold text-white shadow-lg shadow-brand-500/20"
        >
          学
        </div>
        <h1 class="text-xl font-semibold text-fg">学习进度</h1>
        <p class="mt-1 text-sm text-fg-dim">小林coding 面试八股 · 私人在线笔记</p>
      </div>

      <form class="card p-5" @submit.prevent="submit">
        <label for="pw" class="mb-2 block text-xs font-medium text-fg-muted">访问密码</label>
        <input
          id="pw"
          v-model="password"
          type="password"
          autocomplete="current-password"
          placeholder="请输入密码"
          class="field py-2.5"
        />
        <p v-if="error" class="mt-2 text-xs text-red-500">{{ error }}</p>
        <button type="submit" class="btn btn-primary mt-4 w-full py-2.5" :disabled="loading">
          {{ loading ? '登录中…' : '进入学习' }}
        </button>
      </form>

      <p class="mt-6 text-center text-[11px] leading-relaxed text-fg-dim">
        内容版权归
        <a
          href="https://xiaolincoding.com/"
          target="_blank"
          rel="noopener noreferrer"
          class="text-accent hover:underline"
          >小林coding</a
        >
        所有，仅供个人学习使用。
      </p>
    </div>
  </main>
</template>
