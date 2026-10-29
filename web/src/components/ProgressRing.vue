<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    value: number
    size?: number
    stroke?: number
    label?: string
    tone?: 'brand' | 'mint' | 'amber'
  }>(),
  { size: 44, stroke: 4, label: '', tone: 'brand' },
)

const radius = computed(() => (props.size - props.stroke) / 2)
const circumference = computed(() => 2 * Math.PI * radius.value)
const clamped = computed(() => Math.max(0, Math.min(100, props.value)))
const dash = computed(() => (clamped.value / 100) * circumference.value)

const strokeColor = computed(() => {
  switch (props.tone) {
    case 'mint':
      return 'var(--color-mint-400)'
    case 'amber':
      return 'var(--color-amber-glow)'
    default:
      return 'var(--color-accent)'
  }
})
</script>

<template>
  <div
    class="relative inline-flex items-center justify-center"
    :style="{ width: `${size}px`, height: `${size}px` }"
  >
    <svg :width="size" :height="size" class="-rotate-90">
      <circle
        :cx="size / 2"
        :cy="size / 2"
        :r="radius"
        fill="none"
        stroke="var(--color-line-strong)"
        :stroke-width="stroke"
      />
      <circle
        :cx="size / 2"
        :cy="size / 2"
        :r="radius"
        fill="none"
        :stroke="strokeColor"
        :stroke-width="stroke"
        stroke-linecap="round"
        :stroke-dasharray="`${dash} ${circumference}`"
        class="transition-[stroke-dasharray] duration-500"
      />
    </svg>
    <span class="absolute text-[11px] font-semibold tabular-nums text-fg">
      {{ label || `${clamped}%` }}
    </span>
  </div>
</template>
