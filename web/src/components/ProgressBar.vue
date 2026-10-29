<script setup lang="ts">
withDefaults(
  defineProps<{
    modelValue: number
    total: number
    tone?: 'brand' | 'mint'
  }>(),
  { tone: 'brand' },
)

const shades = {
  brand: 'bg-accent',
  mint: 'bg-mint-500',
} as const
</script>

<template>
  <div
    class="h-1.5 w-full overflow-hidden rounded-full bg-line"
    role="progressbar"
    :aria-valuenow="modelValue"
    :aria-valuemin="0"
    :aria-valuemax="total"
  >
    <div
      class="h-full rounded-full transition-[width] duration-500 ease-out"
      :class="shades[tone]"
      :style="{ width: `${total > 0 ? Math.min(100, (modelValue / total) * 100) : 0}%` }"
    />
  </div>
</template>
