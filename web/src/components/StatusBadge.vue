<script setup lang="ts">
import { computed } from 'vue'

/** 学习状态徽标：0 未学 / 1 学习中 / 2 已掌握 */
const props = defineProps<{ status: number; compact?: boolean }>()

const config = computed(() => {
  switch (props.status) {
    case 2:
      return { text: '已掌握', cls: 'bg-mint-500/15 text-mint-500 dark:text-mint-400', dot: 'bg-mint-500' }
    case 1:
      return {
        text: '学习中',
        cls: 'bg-amber-glow/15 text-amber-glow',
        dot: 'bg-amber-glow',
      }
    default:
      return { text: '未开始', cls: 'bg-panel-soft text-fg-dim', dot: 'bg-line-strong' }
  }
})
</script>

<template>
  <span class="chip" :class="config.cls">
    <span class="h-1.5 w-1.5 rounded-full" :class="config.dot" />
    <span v-if="!compact">{{ config.text }}</span>
  </span>
</template>
