<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <main class="grid min-h-dvh place-items-center px-4">
    <div class="text-center">
      <p class="text-5xl font-bold text-fg-dim/40">404</p>
      <p class="mt-3 text-sm text-fg-dim">这个页面不存在</p>
      <RouterLink to="/" class="btn btn-outline mt-5">返回看板</RouterLink>
    </div>
  </main>
</template>
