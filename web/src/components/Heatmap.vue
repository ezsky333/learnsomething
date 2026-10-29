<script setup lang="ts">
import { computed } from 'vue'

/** 26 周 × 7 天的学习热力图。 */
const props = defineProps<{ counts: Map<string, number> }>()

const DAY = 24 * 60 * 60 * 1000

function fmt(d: Date): string {
  const y = d.getFullYear()
  const m = `${d.getMonth() + 1}`.padStart(2, '0')
  const dd = `${d.getDate()}`.padStart(2, '0')
  return `${y}-${m}-${dd}`
}

interface Cell {
  key: string
  count: number
  level: number
  label: string
}

const weeks = computed<Cell[][]>(() => {
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const end = new Date(today)
  const start = new Date(today.getTime() - (26 * 7 - 1) * DAY)
  const offset = (start.getDay() + 6) % 7
  start.setTime(start.getTime() - offset * DAY)

  const out: Cell[][] = []
  let cursor = new Date(start)
  while (cursor <= end) {
    const week: Cell[] = []
    for (let i = 0; i < 7; i++) {
      const key = fmt(cursor)
      const count = props.counts.get(key) ?? 0
      week.push({
        key,
        count,
        level: count === 0 ? 0 : count < 3 ? 1 : count < 6 ? 2 : count < 10 ? 3 : 4,
        label: `${key} · ${count} 条`,
      })
      cursor = new Date(cursor.getTime() + DAY)
    }
    out.push(week)
  }
  return out
})

const levelClass = [
  'bg-line',
  'bg-brand-500/25',
  'bg-brand-500/45',
  'bg-brand-500/70',
  'bg-brand-500',
]

const total = computed(() => [...props.counts.values()].reduce((a, b) => a + b, 0))
</script>

<template>
  <div>
    <div class="flex items-end justify-between gap-3 pb-2">
      <p class="text-xs text-fg-dim">
        近 26 周共 <span class="font-semibold text-fg">{{ total }}</span> 次学习动作
      </p>
      <div class="hidden items-center gap-1 text-[10px] text-fg-dim sm:flex">
        <span>少</span>
        <span v-for="(cls, i) in levelClass" :key="i" class="h-2.5 w-2.5 rounded-sm" :class="cls" />
        <span>多</span>
      </div>
    </div>
    <div class="flex gap-[3px] overflow-x-auto pb-1">
      <div v-for="(week, wi) in weeks" :key="wi" class="flex flex-col gap-[3px]">
        <div
          v-for="cell in week"
          :key="cell.key"
          class="h-2.5 w-2.5 rounded-sm transition-colors"
          :class="levelClass[cell.level]"
          :title="cell.label"
        />
      </div>
    </div>
  </div>
</template>
