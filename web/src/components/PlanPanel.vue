<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useStudyStore } from '@/stores'
import {
  DEFAULT_MINUTES,
  LIBRARY_LABEL,
  adviceText,
  dayLabel,
  dayLoads,
  perLibPace,
  planSummary,
  restDayNote,
  reviewShareOf,
  suggestionText,
  verdictText,
} from '@/utils/plan'
import type { PlanDay, StudyPlanResponse } from '@/types'
import type { DayLoad } from '@/utils/plan'

const study = useStudyStore()

const data = ref<StudyPlanResponse | null>(null)
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await study.refreshStudyPlan()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '计划推算失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)

const plan = computed(() => data.value?.plan ?? null)
const advice = computed(() => data.value?.advice ?? null)
const goal = computed(() => study.goal)

const verdict = computed(() =>
  plan.value && advice.value ? verdictText(plan.value, advice.value) : '',
)
const adviceLine = computed(() =>
  plan.value && advice.value ? adviceText(plan.value, advice.value) : '',
)
const suggestion = computed(() => (advice.value ? suggestionText(advice.value) : ''))
const summary = computed(() =>
  plan.value && data.value?.libs
    ? planSummary(
        plan.value,
        Object.entries(data.value.libs).map(([library, remaining]) => ({
          library,
          remaining,
          minutesPerItem: goal.value?.minutesPerItem?.[library] ?? DEFAULT_MINUTES[library] ?? 5,
        })),
      )
    : '',
)
const reviewShare = computed(() => (plan.value ? reviewShareOf(plan.value) : 0))
const restNote = computed(() =>
  plan.value && goal.value
    ? restDayNote(plan.value.restCount, goal.value.weeklyRest ?? [])
    : '',
)

/** 前 21 天的日历格子（三周就够看出节奏，全排出来太长）。 */
const calendar = computed<PlanDay[]>(() => (plan.value ? plan.value.days.slice(0, 21) : []))

/** 每天的总用时相对预算的比例（0..1），用于画压力条。 */
function loadRatio(d: DayLoad): number {
  const cap = plan.value?.dailyMinutes ?? 120
  return cap > 0 ? Math.min(1, d.total / cap) : 0
}

/** 当天新学与复习各占多少（用于堆叠条）。 */
function newRatio(d: DayLoad): number {
  return d.total > 0 ? d.newMinutes / d.total : 0
}

/** 各库在计划里的日均速度，用来解释「算法为什么学得慢」。 */
const paceRows = computed(() => {
  if (!plan.value || !data.value?.libs) return []
  return Object.keys(data.value.libs).map((lib) => ({
    lib,
    label: LIBRARY_LABEL[lib] ?? lib,
    remaining: data.value!.libs![lib],
    pace: perLibPace(plan.value!, lib),
    minutes: goal.value?.minutesPerItem?.[lib] ?? DEFAULT_MINUTES[lib] ?? 5,
  }))
})

const loads = computed(() => (plan.value ? dayLoads(plan.value, 14) : []))

/** 结论颜色：可行绿、偏紧黄、不可能红。 */
const verdictTone = computed(() => {
  if (!advice.value) return 'text-fg-dim'
  if (advice.value.feasible) return 'text-mint-600 dark:text-mint-400'
  if (advice.value.requiredDailyMinutes > 300) return 'text-red-500'
  return 'text-amber-glow'
})
</script>

<template>
  <div class="card animate-rise p-5 sm:p-6">
    <div class="mb-3 flex flex-wrap items-center gap-2">
      <h2 class="text-sm font-semibold text-fg">学习计划推算</h2>
      <button class="btn btn-outline px-2 py-0.5 text-[11px]" :disabled="loading" @click="load">
        {{ loading ? '推算中…' : '重新推算' }}
      </button>
      <span v-if="goal" class="text-[11px] text-fg-dim">
        截止 {{ goal.dueDay }} · 每天 {{ goal.dailyMinutes || 120 }} 分钟
      </span>
    </div>

    <p v-if="!goal" class="py-3 text-xs leading-relaxed text-fg-dim">
      先设置学习任务（选模块 + 截止日期 + 每天可用时间），这里会按「学习和复习抢同一份时间」
      逐日推演出一份可执行的排期，并告诉你目标是否现实、该调整哪一头。
    </p>

    <p v-else-if="error" class="py-2 text-xs text-red-500">{{ error }}</p>

    <p v-else-if="loading && !plan" class="py-3 text-xs text-fg-dim">正在推算…</p>

    <template v-else-if="plan && advice">
      <!-- 结论 -->
      <p class="text-sm leading-relaxed" :class="verdictTone">{{ verdict }}</p>
      <p v-if="adviceLine" class="mt-1 text-[11px] leading-relaxed text-fg-dim">{{ adviceLine }}</p>
      <p v-if="suggestion" class="mt-1 text-[11px] font-medium leading-relaxed text-accent">
        {{ suggestion }}
      </p>
      <p v-if="summary" class="mt-2 text-[11px] leading-relaxed text-fg-dim">{{ summary }}</p>
      <p v-if="restNote" class="mt-1 text-[11px] leading-relaxed text-fg-dim">{{ restNote }}</p>

      <!-- 各库速度：算法和八股就是不一样快 -->
      <div v-if="paceRows.length" class="mt-3 grid gap-2 sm:grid-cols-2">
        <div
          v-for="row in paceRows"
          :key="row.lib"
          class="rounded-lg bg-panel-soft px-3 py-2"
        >
          <div class="flex items-baseline justify-between">
            <span class="text-[11px] font-medium text-fg-muted">{{ row.label }}</span>
            <span class="text-[10px] text-fg-dim">剩 {{ row.remaining }} 条</span>
          </div>
          <p class="mt-0.5 text-[11px] tabular-nums text-fg">
            每天约 <span class="font-semibold">{{ row.pace.toFixed(1) }}</span> 条
            <span class="text-fg-dim">（每条约 {{ row.minutes }} 分钟）</span>
          </p>
        </div>
      </div>

      <!-- 未来 14 天：新学 vs 复习的堆叠条 -->
      <div v-if="loads.length" class="mt-4 border-t border-line pt-3">
        <div class="mb-2 flex items-center justify-between">
          <p class="text-[11px] font-medium text-fg-muted">前 14 天的时间分配</p>
          <span class="text-[10px] text-fg-dim">
            复习占 {{ Math.round(reviewShare * 100) }}%
          </span>
        </div>
        <div class="flex items-end gap-1" style="height: 72px">
          <div
            v-for="d in loads"
            :key="d.day"
            class="group relative flex-1"
            :title="`${d.day}：新学 ${d.newMinutes} 分 + 复习 ${d.reviewMinutes} 分`"
          >
            <div class="flex h-[72px] flex-col justify-end">
              <div
                v-if="d.rest"
                class="w-full rounded-t bg-line-strong/60"
                :style="{ height: '6px' }"
                title="停滞日"
              />
              <template v-else>
                <!-- 复习段（底部，实心深色） -->
                <div
                  class="w-full bg-brand-600 dark:bg-brand-400"
                  :style="{ height: `${loadRatio(d) * 72 * (1 - newRatio(d))}px` }"
                  title="复习"
                />
                <!-- 新学段（顶部，浅色） -->
                <div
                  class="w-full rounded-t bg-brand-300 dark:bg-brand-500"
                  :style="{ height: `${loadRatio(d) * 72 * newRatio(d)}px` }"
                  title="新学"
                />
              </template>
            </div>
          </div>
        </div>
        <p class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] text-fg-dim">
          <span class="inline-flex items-center gap-1">
            <span class="inline-block h-2 w-2 rounded-sm bg-brand-600 dark:bg-brand-400" /> 复习
          </span>
          <span class="inline-flex items-center gap-1">
            <span class="inline-block h-2 w-2 rounded-sm bg-brand-300 dark:bg-brand-500" /> 新学
          </span>
          <span>· 柱高对应当天用时占预算的比例</span>
        </p>
      </div>

      <!-- 前 21 天日历 -->
      <div v-if="calendar.length" class="mt-4 border-t border-line pt-3">
        <p class="mb-2 text-[11px] font-medium text-fg-muted">逐日安排（前 21 天）</p>
        <div class="grid gap-1.5 sm:grid-cols-2 lg:grid-cols-3">
          <div
            v-for="d in calendar"
            :key="d.day"
            class="rounded-lg px-2.5 py-1.5 text-[11px]"
            :class="d.rest ? 'bg-line/40 text-fg-dim' : 'bg-panel-soft'"
          >
            <div class="flex items-center justify-between">
              <span class="font-medium text-fg-muted">{{ dayLabel(d.day) }}</span>
              <span v-if="d.rest" class="text-[10px]">停滞</span>
              <span v-else class="text-[10px] tabular-nums text-fg-dim">{{ d.minutesUsed }} 分</span>
            </div>
            <template v-if="!d.rest">
              <p v-if="d.newMinutes === 0 && d.reviewMinutes === 0" class="mt-0.5 text-[10px] text-fg-dim">
                无任务
              </p>
              <p v-else class="mt-0.5 text-[10px] text-fg-dim">
                <span v-if="d.newMinutes > 0" class="text-accent">
                  新学 {{ Object.values(d.new).reduce((a, b) => a + b, 0) }} 条
                </span>
                <span v-if="d.reviewMinutes > 0">
                  {{ d.newMinutes > 0 ? ' · ' : '' }}复习
                  {{ Object.values(d.review).reduce((a, b) => a + b, 0) }} 条
                </span>
              </p>
            </template>
          </div>
        </div>
      </div>

      <p class="mt-3 text-[11px] leading-relaxed text-fg-dim">
        计划会随实际进度自动重算：你今天多学几条，后面的排期立刻变松；落后了也会重新摊平，
        不会让你第二天面对一笔还不完的账。
      </p>
    </template>
  </div>
</template>
