<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useContentStore, useStudyStore } from '@/stores'
import { computeGoalProgress, daysBetween, formatDay, paceOf, today } from '@/utils/goal'
import type { ModuleProgress, PaceLevel } from '@/utils/goal'
import { DEFAULT_MINUTES, LIBRARY_LABEL, WEEKDAYS } from '@/utils/plan'
import type { DocMeta, PlanDay, PlanWindow } from '@/types'

const content = useContentStore()
const study = useStudyStore()

const open = ref(false)
const saving = ref(false)
const error = ref('')

/** 编辑中的草稿 */
const draftSlugs = ref<string[]>([])
const draftDue = ref('')
const draftDaily = ref(0)
const draftMinutes = ref(120)
const draftRestDays = ref<string[]>([])
const draftWeeklyRest = ref<number[]>([])
const draftPerItem = ref<Record<string, number>>({ ...DEFAULT_MINUTES })
const newRestDay = ref('')
const advancedOpen = ref(false)

/** 时间窗：上午只学算法、下午复习+八股。 */
const draftWindows = ref<PlanWindow[]>([])
const windowsOpen = ref(false)

/** 默认时间窗：上午 60 分（只算法）、下午 90 分（复习+其余）。 */
function defaultWindows(): PlanWindow[] {
  return [
    { name: 'morning', minutes: 60, libs: ['algo'], reviewOnly: false },
    { name: 'evening', minutes: 90, libs: ['interview', 'ai'], reviewOnly: false },
  ]
}

/** 总可用时间：有窗时取各窗之和。 */
const totalMinutes = computed(() =>
  draftWindows.value.length > 0
    ? draftWindows.value.reduce((s, w) => s + (w.minutes || 0), 0)
    : draftMinutes.value,
)

// 默认截止日：今天 + 30 天
function defaultDue(): string {
  const d = new Date()
  d.setDate(d.getDate() + 30)
  return formatDay(d)
}

watch(
  () => [open.value, study.goal] as const,
  ([isOpen, g]) => {
    if (!isOpen) return
    draftSlugs.value = g ? [...g.docSlugs] : []
    draftDue.value = g?.dueDay ?? defaultDue()
    draftDaily.value = g?.dailyTarget ?? 0
    draftMinutes.value = g?.dailyMinutes && g.dailyMinutes > 0 ? g.dailyMinutes : 120
    draftRestDays.value = g ? [...(g.restDays ?? [])] : []
    draftWeeklyRest.value = g ? [...(g.weeklyRest ?? [])] : []
    draftPerItem.value = { ...DEFAULT_MINUTES, ...(g?.minutesPerItem ?? {}) }
    draftWindows.value = g?.windows && g.windows.length > 0
      ? g.windows.map((w) => ({ ...w, libs: [...(w.libs ?? [])] }))
      : []
  },
  { immediate: true },
)

/** 启用时间窗模式（把当前的总时间拆成上午/下午）。 */
function enableWindows() {
  draftWindows.value = defaultWindows()
  windowsOpen.value = true
}

function disableWindows() {
  draftWindows.value = []
  windowsOpen.value = false
}

function addWindow() {
  draftWindows.value.push({ name: 'new', minutes: 60, libs: [], reviewOnly: false })
}

function removeWindow(i: number) {
  draftWindows.value.splice(i, 1)
}

function toggleWindowLib(w: PlanWindow, lib: string) {
  const i = w.libs.indexOf(lib)
  if (i >= 0) w.libs.splice(i, 1)
  else w.libs.push(lib)
}

/** 选中的模块涉及哪些内容库（决定要暴露几条「每条分钟数」的设置）。 */
const selectedLibraries = computed(() => {
  const set = new Set<string>()
  for (const doc of selectedItems.value) set.add(doc.library)
  return [...set]
})

/** 预计的总工作量（分钟）= Σ 剩余条数 × 单价。 */
const totalMinutesEstimate = computed(() => {
  let sum = 0
  for (const doc of selectedItems.value) {
    const done = study.docStats(doc.slug, doc.questionCount).done
    const left = Math.max(0, doc.questionCount - done)
    const per = draftPerItem.value[doc.library] ?? DEFAULT_MINUTES[doc.library] ?? 5
    sum += left * per
  }
  return sum
})

/** 按每天可用时间粗估需要多少天（不含复习，用于给用户一个直觉）。 */
const estimateDays = computed(() => {
  if (draftMinutes.value <= 0) return 0
  return Math.ceil(totalMinutesEstimate.value / draftMinutes.value)
})

/** 有效学习天数：扣掉停滞日与每周休息日。 */
const effectiveStudyDays = computed(() => {
  if (!draftDue.value) return 0
  const total = Math.max(1, daysBetween(today(), draftDue.value) + 1)
  const restSet = new Set(draftRestDays.value)
  const wk = new Set(draftWeeklyRest.value)
  let usable = 0
  for (let i = 0; i < total; i++) {
    const d = new Date()
    d.setDate(d.getDate() + i)
    const key = formatDay(d)
    if (restSet.has(key) || wk.has(d.getDay())) continue
    usable++
  }
  return usable
})

function addRestDay() {
  const d = newRestDay.value
  if (!d || draftRestDays.value.includes(d)) return
  draftRestDays.value = [...draftRestDays.value, d].sort()
  newRestDay.value = ''
}

function removeRestDay(d: string) {
  draftRestDays.value = draftRestDays.value.filter((x) => x !== d)
}

function toggleWeekday(w: number) {
  const i = draftWeeklyRest.value.indexOf(w)
  if (i >= 0) draftWeeklyRest.value.splice(i, 1)
  else draftWeeklyRest.value.push(w)
}

/** 按库分组列出可勾选的模块 */
const moduleGroups = computed(() => {
  const groups = new Map<string, DocMeta[]>()
  for (const doc of content.docMetas) {
    const list = groups.get(doc.group) ?? []
    list.push(doc)
    groups.set(doc.group, list)
  }
  return [...groups.entries()].map(([title, docs]) => ({ title, docs }))
})

const selectedItems = computed(() => {
  const set = new Set(draftSlugs.value)
  return content.docMetas.filter((d) => set.has(d.slug) && !d.readingList)
})

const draftTotal = computed(() =>
  selectedItems.value.reduce((sum, d) => sum + d.questionCount, 0),
)

function toggleDoc(slug: string) {
  const i = draftSlugs.value.indexOf(slug)
  if (i >= 0) draftSlugs.value.splice(i, 1)
  else draftSlugs.value.push(slug)
}

function toggleGroup(docs: DocMeta[], selectAll: boolean) {
  for (const d of docs) {
    if (d.readingList) continue
    const i = draftSlugs.value.indexOf(d.slug)
    if (selectAll && i < 0) draftSlugs.value.push(d.slug)
    if (!selectAll && i >= 0) draftSlugs.value.splice(i, 1)
  }
}

function groupState(docs: DocMeta[]): 'all' | 'some' | 'none' {
  const usable = docs.filter((d) => !d.readingList)
  if (!usable.length) return 'none'
  const selected = usable.filter((d) => draftSlugs.value.includes(d.slug)).length
  if (selected === 0) return 'none'
  if (selected === usable.length) return 'all'
  return 'some'
}

async function save() {
  if (!draftSlugs.value.length) {
    error.value = '至少选一个模块'
    return
  }
  if (!draftDue.value) {
    error.value = '请选择截止日期'
    return
  }
  if (draftDue.value < today()) {
    error.value = '截止日期不能早于今天'
    return
  }
  saving.value = true
  error.value = ''
  try {
    await study.saveGoal({
      docSlugs: draftSlugs.value,
      dueDay: draftDue.value,
      dailyTarget: Math.max(0, Math.round(draftDaily.value)),
      dailyMinutes: Math.max(0, Math.round(totalMinutes.value)),
      restDays: draftRestDays.value,
      weeklyRest: draftWeeklyRest.value,
      minutesPerItem: draftPerItem.value,
      windows: draftWindows.value,
    })
    open.value = false
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存失败'
  } finally {
    saving.value = false
  }
}

async function clear() {
  saving.value = true
  try {
    await study.clearGoal()
    open.value = false
  } catch (e) {
    error.value = e instanceof Error ? e.message : '删除失败'
  } finally {
    saving.value = false
  }
}

// ---------- 展示用：当前任务进度 ----------
const doneOf = (slug: string) => {
  const meta = content.metaOf(slug)
  if (!meta) return 0
  return study.docStats(slug, meta.questionCount).done
}

const progress = computed(() =>
  computeGoalProgress(
    study.goal,
    content.docMetas,
    doneOf,
    undefined,
    study.goal?.minutesPerItem,
  ),
)

const pace = computed(() => paceOf(progress.value))

/**
 * 今天的排期（服务端逐日推算，唯一权威）。
 *
 * 为什么必须有它：界面上曾经同时出现「每天应完成 9.3 条」「今天该学 22 条」
 * 「每天约 2.0 条」三个数字，各自算法不同 —— 9.3 是「总条数 ÷ 总天数」，
 * 把 25 分钟一条的算法和 5 分钟一条的八股混在一起平均，本身就没有意义。
 * 用户看到自然不知道该信哪个。
 *
 * 现在的原则：有排期就用排期，所有「今天该多少」都从这一个来源推导。
 */
const planToday = computed<PlanDay | null>(() => {
  const plan = study.studyPlan?.plan
  if (!plan || !plan.days.length) return null
  const todayStr = today()
  return plan.days.find((d) => d.day === todayStr) ?? plan.days[0] ?? null
})

/** 今天该新学多少条（各库合计）。 */
const planTodayNew = computed(() => {
  if (!planToday.value) return 0
  return Object.values(planToday.value.new ?? {}).reduce((a, b) => a + b, 0)
})

/** 今天该复习多少条。 */
const planTodayReview = computed(() => {
  if (!planToday.value) return 0
  return Object.values(planToday.value.review ?? {}).reduce((a, b) => a + b, 0)
})

/**
 * 今天已经复习了多少条 / 目标多少条。
 *
 * 没有这个数字，用户做完 40 条复习后界面还是「今天新学 22 条待完成」，
 * 看起来像什么都没干。复习是今天真实发生的劳动，必须显示出来。
 */
const todayReviewDone = computed(() => study.studyPlan?.today?.reviewDone ?? 0)
const todayReviewTarget = computed(() => {
  const t = study.studyPlan?.today
  if (!t) return planTodayReview.value
  // 今日复习目标 = 上午已做 + 剩余队列；都有上限约束，取两者能覆盖的最大口径
  return Math.max(planTodayReview.value, t.reviewDone)
})

/** 今天已经花掉的分钟数。 */
const minutesSpent = computed(() => study.studyPlan?.today?.minutesSpent ?? 0)

/** 复习进度的颜色：做完了变绿。 */
const reviewTone = computed(() =>
  todayReviewDone.value >= todayReviewTarget.value && todayReviewDone.value > 0
    ? 'text-mint-600 dark:text-mint-400'
    : 'text-fg',
)

/** 距离任务开始已经几天（1 表示今天刚开始）。 */
const startedAgo = computed(() => {
  const start = study.goal?.startDay
  if (!start) return 0
  return daysBetween(start, today()) + 1
})

/** 有排期时用它，没有则退回线性估算。 */
const hasPlan = computed(() => planToday.value !== null)

/**
 * 节奏结论。
 *
 * 排期模式下不再用「实际 − 线性应有」算超前落后（那个基准本身不成立），
 * 改成「按当前节奏能不能在截止日前学完」，这才是能行动的信息。
 */
const planPaceText = computed(() => {
  const data = study.studyPlan
  if (!data?.plan || !data.advice || !study.goal) return ''
  if (!data.advice.feasible) return '目标偏紧，学不完'
  const finish = data.plan.finishDay
  if (!finish) return '目标可行'
  const early = daysBetween(finish, study.goal.dueDay)
  if (early > 0) return `比截止日早 ${early} 天`
  if (early === 0) return '正好卡在截止日'
  return '目标可行'
})

/** 排期模式下的节奏颜色。 */
const planPaceColor = computed(() => {
  const data = study.studyPlan
  if (!data?.advice) return 'text-fg-dim'
  if (!data.advice.feasible) return 'text-amber-glow'
  return 'text-mint-500 dark:text-mint-400'
})

onMounted(async () => {
  try {
    await study.refreshStudyPlan()
  } catch {
    // 推算失败时退回线性估算，不影响其余信息
  }
})

const paceText = computed(() => {
  const p = progress.value
  if (pace.value === 'none') return '还没设置学习任务'
  if (pace.value === 'done') return '目标已完成 🎉'
  if (pace.value === 'expired') return '已超过截止日期'
  const diff = Math.round(p.aheadBy)
  if (pace.value === 'ahead') return `超前 ${diff} 条`
  if (pace.value === 'onTrack') return diff === 0 ? '正好跟上进度' : `略有出入 ${diff > 0 ? '+' : ''}${diff} 条`
  return `落后 ${Math.abs(diff)} 条`
})

const paceColor = computed(() => {
  switch (pace.value) {
    case 'ahead':
    case 'done':
      return 'text-mint-500 dark:text-mint-400'
    case 'onTrack':
      return 'text-accent'
    case 'slightlyBehind':
      return 'text-amber-glow'
    case 'behind':
    case 'expired':
      return 'text-red-500'
    default:
      return 'text-fg-dim'
  }
})

/** 进度条上「今天应有」的刻度位置 */
const expectedMark = computed(() => Math.min(100, progress.value.expectedPercent))

// ---------- 分模块展示的辅助 ----------

/** 单个模块「今天应有」的百分比位置 */
function moduleMark(m: ModuleProgress): number {
  if (m.totalItems <= 0) return 0
  return Math.min(100, (m.expectedDone / m.totalItems) * 100)
}

function modulePaceClass(pace: PaceLevel): string {
  switch (pace) {
    case 'ahead':
    case 'done':
      return 'text-mint-500 dark:text-mint-400'
    case 'onTrack':
      return 'text-accent'
    case 'slightlyBehind':
      return 'text-amber-glow'
    case 'behind':
    case 'expired':
      return 'text-red-500'
    default:
      return 'text-fg-dim'
  }
}

function moduleBarClass(pace: PaceLevel): string {
  switch (pace) {
    case 'ahead':
    case 'done':
    case 'onTrack':
      return 'bg-accent'
    case 'slightlyBehind':
      return 'bg-amber-glow'
    case 'behind':
    case 'expired':
      return 'bg-red-500'
    default:
      return 'bg-accent'
  }
}

function modulePaceText(m: ModuleProgress): string {
  if (m.pace === 'done') return '已完成'
  if (m.pace === 'expired') return '已过期'
  const diff = Math.round(m.aheadBy)
  if (m.pace === 'ahead') return `超前 ${diff}`
  if (m.pace === 'onTrack') return diff === 0 ? '正常' : `${diff > 0 ? '+' : ''}${diff}`
  return `落后 ${Math.abs(diff)}`
}
</script>

<template>
  <div class="card animate-rise p-5 sm:p-6">
    <div class="mb-3 flex flex-wrap items-center gap-2">
      <h2 class="text-sm font-semibold text-fg">学习任务</h2>
      <button
        class="btn btn-outline px-2 py-0.5 text-[11px]"
        @click="open = !open"
      >
        {{ study.goal ? '调整' : '设置任务' }}
      </button>
      <span v-if="study.goal" class="text-[11px] text-fg-dim">
        截止 {{ study.goal.dueDay }}
      </span>
      <span
        v-if="study.goal"
        class="ml-auto text-[11px] font-medium"
        :class="hasPlan ? planPaceColor : paceColor"
      >
        {{ hasPlan ? planPaceText : paceText }}
      </span>
    </div>

    <!-- 已设置任务：显示今日安排 / 差距 -->
    <template v-if="study.goal && !open">
      <!-- 有逐日排期时：一切以排期为准（唯一权威） -->
      <template v-if="hasPlan">
        <div class="grid grid-cols-3 gap-2 text-center">
          <div class="rounded-lg bg-panel-soft px-2 py-2">
            <p class="text-[10px] text-fg-dim">今天新学</p>
            <p class="mt-0.5 text-lg font-semibold tabular-nums text-accent">
              {{ planTodayNew }}
              <span class="text-[10px] font-normal text-fg-dim">条</span>
            </p>
          </div>
          <div class="rounded-lg bg-panel-soft px-2 py-2">
            <p class="text-[10px] text-fg-dim">今天复习</p>
            <p class="mt-0.5 text-lg font-semibold tabular-nums" :class="reviewTone">
              {{ todayReviewDone }}
              <span class="text-[10px] font-normal text-fg-dim">
                / {{ todayReviewTarget }} 条
              </span>
            </p>
          </div>
          <div class="rounded-lg bg-panel-soft px-2 py-2">
            <p class="text-[10px] text-fg-dim">今天用时</p>
            <p class="mt-0.5 text-lg font-semibold tabular-nums text-fg">
              {{ planToday?.minutesUsed ?? 0 }}
              <span class="text-[10px] font-normal text-fg-dim">分钟</span>
            </p>
          </div>
        </div>

        <!-- 今天复习完的提示：不显示的话，用户会觉得「今天什么都没做」 -->
        <p
          v-if="todayReviewDone > 0"
          class="mt-2 rounded-lg bg-mint-500/10 px-3 py-1.5 text-[11px] leading-relaxed text-mint-600 dark:text-mint-400"
        >
          ✓ 今天已完成 {{ todayReviewDone }} 条复习<template v-if="minutesSpent > 0">（约 {{ minutesSpent }} 分钟）</template>{{ todayReviewDone >= todayReviewTarget ? '，复习额度用完' : '' }}
          <span v-if="planTodayNew > 0" class="text-fg-dim">
            · 剩余时间够学 {{ planTodayNew }} 条新内容
          </span>
        </p>

        <div class="mt-3 flex items-baseline justify-between text-[11px] text-fg-dim">
          <span>
            累计完成 {{ progress.doneItems }} / {{ progress.totalItems }} 条
            （{{ Math.round(progress.percent) }}%）
            <span v-if="startedAgo > 0" class="text-fg-dim/70">· 已坚持 {{ startedAgo }} 天</span>
          </span>
          <span class="tabular-nums">剩余 {{ progress.daysLeft }} 天</span>
        </div>
        <div class="mt-1.5 h-2 w-full overflow-hidden rounded-full bg-line">
          <div
            class="h-full rounded-full bg-accent transition-[width] duration-500"
            :style="{ width: `${progress.percent}%` }"
          />
        </div>
        <p class="mt-2 text-[11px] leading-relaxed text-fg-dim">
          按当前节奏预计
          <span class="font-medium text-fg-muted">{{ study.studyPlan?.plan?.finishDay }}</span>
          全部学完
          <span v-if="study.studyPlan?.plan?.finishDay">
            （{{ planPaceText }}）
          </span>
        </p>
      </template>

      <!-- 没有排期时退回线性估算 -->
      <template v-else>
        <div class="flex items-end justify-between gap-3">
          <div>
            <p class="text-[11px] text-fg-dim">今日应有进度</p>
            <p class="text-xl font-semibold text-fg tabular-nums">
              {{ Math.ceil(progress.expectedDone) }}
              <span class="text-xs font-normal text-fg-dim">/ {{ progress.totalItems }} 条累计</span>
            </p>
          </div>
          <div class="text-right">
            <p class="text-[11px] text-fg-dim">实际完成</p>
            <p class="text-xl font-semibold text-fg tabular-nums">
              {{ progress.doneItems }}
              <span class="text-xs font-normal text-fg-dim">条</span>
            </p>
          </div>
        </div>

        <!-- 双刻度进度条：实心=实际，刻度线=应有 -->
        <div class="relative mt-3">
          <div class="h-2 w-full overflow-hidden rounded-full bg-line">
            <div
              class="h-full rounded-full bg-accent transition-[width] duration-500"
              :style="{ width: `${progress.percent}%` }"
            />
          </div>
          <div
            v-if="expectedMark > 0 && expectedMark < 100"
            class="absolute top-[-2px] h-3 w-0.5 -translate-x-1/2 rounded-full bg-fg-muted/70"
            :style="{ left: `${expectedMark}%` }"
            :title="`今天应有 ${Math.ceil(progress.expectedDone)} 条`"
          />
        </div>

        <div class="mt-3 grid grid-cols-3 gap-2 text-center">
          <div class="rounded-lg bg-panel-soft px-2 py-2">
            <p class="text-[10px] text-fg-dim">每天应完成</p>
            <p class="mt-0.5 text-sm font-semibold text-fg tabular-nums">
              {{ progress.dailyTarget.toFixed(1) }}
            </p>
          </div>
          <div class="rounded-lg bg-panel-soft px-2 py-2">
            <p class="text-[10px] text-fg-dim">今天还差</p>
            <p class="mt-0.5 text-sm font-semibold tabular-nums" :class="progress.todayRemaining > 0 ? 'text-amber-glow' : 'text-mint-500 dark:text-mint-400'">
              {{ progress.todayRemaining > 0 ? progress.todayRemaining : '达标' }}
            </p>
          </div>
          <div class="rounded-lg bg-panel-soft px-2 py-2">
            <p class="text-[10px] text-fg-dim">剩余天数</p>
            <p class="mt-0.5 text-sm font-semibold text-fg tabular-nums">{{ progress.daysLeft }}</p>
          </div>
        </div>

        <p class="mt-2 text-[11px] leading-relaxed text-fg-dim">
          第 {{ progress.daysElapsed }} / {{ progress.totalDays }} 天 ·
          按当前节奏预计 {{ progress.projectedEndDay }} 完成
          <span v-if="progress.doneToday > 0">· 今天已完成 {{ progress.doneToday }} 条</span>
        </p>
      </template>

      <!-- 进度：按内容库聚合（时间窗模式下）或按模块（普通模式） -->
      <div v-if="progress.modules.length" class="mt-4 border-t border-line pt-3">
        <p class="mb-2 text-[11px] font-medium text-fg-muted">
          {{ progress.modules[0]?.byLibrary ? '进度（按内容库）' : '分模块进度' }}
        </p>
        <div class="space-y-2.5">
          <div v-for="m in progress.modules" :key="m.slug">
            <div class="flex items-baseline gap-2">
              <span class="min-w-0 flex-1 truncate text-[11px] text-fg-muted" :title="m.title">
                {{ m.title }}
              </span>
              <span class="shrink-0 text-[10px] tabular-nums" :class="modulePaceClass(m.pace)">
                {{ modulePaceText(m) }}
              </span>
              <span class="w-16 shrink-0 text-right text-[10px] tabular-nums text-fg-dim">
                {{ Math.round(m.doneItems) }}/{{ m.totalItems }}
              </span>
            </div>
            <!-- 每行一条双刻度进度条 -->
            <div class="relative mt-1">
              <div class="h-1.5 w-full overflow-hidden rounded-full bg-line">
                <div
                  class="h-full rounded-full transition-[width] duration-500"
                  :class="moduleBarClass(m.pace)"
                  :style="{ width: `${m.percent}%` }"
                />
              </div>
              <div
                v-if="moduleMark(m) > 0 && moduleMark(m) < 100"
                class="absolute top-[-1px] h-2.5 w-0.5 -translate-x-1/2 rounded-full bg-fg-muted/60"
                :style="{ left: `${moduleMark(m)}%` }"
                :title="`今天应有 ${Math.ceil(m.expectedDone)} 条`"
              />
            </div>
            <p class="mt-0.5 text-[10px] text-fg-dim">
              <template v-if="m.byLibrary && planToday">
                今天新学
                <span class="font-medium text-fg-muted">{{ planToday.new?.[m.library] ?? 0 }}</span> 条
                <span v-if="(planToday.review?.[m.library] ?? 0) > 0" class="text-fg-dim/80">
                  · 复习 {{ planToday.review?.[m.library] }} 条
                </span>
                <span class="text-fg-dim/60">（{{ m.minutesPerDay }} 分钟）</span>
              </template>
              <template v-else>
                今天应到 {{ Math.ceil(m.expectedDone) }} 条
                <span v-if="m.todayRemaining > 0" class="text-amber-glow">· 还差 {{ m.todayRemaining }} 条</span>
                <span v-else class="text-mint-500 dark:text-mint-400">· 已达标</span>
                <span class="text-fg-dim/70">
                  （每天 {{ m.dailyTarget.toFixed(1) }} 条<template
                    v-if="m.minutesPerDay"
                  >，{{ m.minutesPerDay }} 分钟</template
                  >）
                </span>
              </template>
            </p>
          </div>
        </div>
      </div>
    </template>

    <!-- 未设置 -->
    <p v-else-if="!study.goal && !open" class="py-3 text-xs leading-relaxed text-fg-dim">
      选几个模块，设定截止日期，就能看到「今天理论上应该学到哪儿」以及自己是超前还是落后。
    </p>

    <!-- 编辑面板 -->
    <div v-if="open" class="mt-3 border-t border-line pt-3">
      <p class="mb-2 text-[11px] font-medium text-fg-muted">选择要学的模块</p>
      <div class="max-h-64 space-y-3 overflow-y-auto pr-1">
        <div v-for="group in moduleGroups" :key="group.title">
          <div class="mb-1 flex items-center gap-2">
            <p class="text-[10px] font-semibold uppercase tracking-wider text-fg-dim">
              {{ group.title }}
            </p>
            <button
              class="text-[10px] text-accent hover:underline"
              @click="toggleGroup(group.docs, groupState(group.docs) !== 'all')"
            >
              {{ groupState(group.docs) === 'all' ? '取消全选' : '全选' }}
            </button>
          </div>
          <div class="grid gap-1 sm:grid-cols-2">
            <label
              v-for="doc in group.docs"
              :key="doc.slug"
              class="flex cursor-pointer items-center gap-2 rounded px-1.5 py-1 text-[11px] transition-colors hover:bg-panel-soft"
              :class="doc.readingList ? 'opacity-50' : ''"
            >
              <input
                type="checkbox"
                class="h-3 w-3 accent-[var(--color-accent)]"
                :checked="draftSlugs.includes(doc.slug)"
                :disabled="doc.readingList"
                @change="toggleDoc(doc.slug)"
              />
              <span class="min-w-0 flex-1 truncate text-fg-muted">{{ doc.title }}</span>
              <span class="shrink-0 text-[10px] text-fg-dim tabular-nums">
                {{ doc.readingList ? '清单' : `${doc.questionCount} 条` }}
              </span>
            </label>
          </div>
        </div>
      </div>

      <div class="mt-3 grid gap-2 sm:grid-cols-2">
        <label class="block">
          <span class="mb-1 block text-[11px] text-fg-dim">预计完成日期</span>
          <input v-model="draftDue" type="date" :min="today()" class="field py-1.5 text-xs" />
        </label>
        <label v-if="draftWindows.length === 0" class="block">
          <span class="mb-1 block text-[11px] text-fg-dim">
            每天能投入多少分钟
          </span>
          <input
            v-model.number="draftMinutes"
            type="number"
            min="1"
            max="1440"
            step="5"
            class="field py-1.5 text-xs"
          />
        </label>
        <div v-else class="block">
          <span class="mb-1 block text-[11px] text-fg-dim">每天合计（按时间窗）</span>
          <p class="field flex items-center py-1.5 text-xs text-fg-muted">
            {{ totalMinutes }} 分钟
          </p>
        </div>
      </div>

      <!-- 时间窗：上午学算法、下午复习+八股 -->
      <div class="mt-3 rounded-lg bg-panel-soft px-3 py-2">
        <div class="flex items-center justify-between">
          <span class="text-[11px] font-medium text-fg-muted">
            按时间段分开安排（上午学算法 / 下午复习+八股）
          </span>
          <button
            v-if="draftWindows.length === 0"
            class="text-[10px] text-accent hover:underline"
            @click="enableWindows"
          >
            启用
          </button>
          <button v-else class="text-[10px] text-fg-dim hover:text-fg" @click="disableWindows">
            取消分段
          </button>
        </div>

        <div v-if="draftWindows.length > 0" class="mt-2 space-y-2">
          <div
            v-for="(w, i) in draftWindows"
            :key="i"
            class="rounded-lg border border-line bg-panel px-2.5 py-2"
          >
            <div class="flex flex-wrap items-center gap-1.5">
              <input
                v-model="w.name"
                class="field w-20 py-0.5 text-[11px]"
                placeholder="名称"
              />
              <input
                v-model.number="w.minutes"
                type="number"
                min="0"
                max="1440"
                step="5"
                class="field w-16 py-0.5 text-[11px]"
              />
              <span class="text-[10px] text-fg-dim">分钟</span>
              <label class="ml-1 flex items-center gap-1 text-[10px] text-fg-dim">
                <input v-model="w.reviewOnly" type="checkbox" class="h-3 w-3 accent-[var(--color-accent)]" />
                只复习
              </label>
              <button
                class="ml-auto text-[10px] text-fg-dim hover:text-red-500"
                @click="removeWindow(i)"
              >
                删除
              </button>
            </div>
            <div v-if="!w.reviewOnly" class="mt-1 flex flex-wrap items-center gap-1">
              <span class="text-[10px] text-fg-dim">学哪些：</span>
              <button
                v-for="lib in ['interview', 'algo', 'ai']"
                :key="lib"
                class="rounded px-1.5 py-0.5 text-[10px] transition-colors"
                :class="
                  w.libs.includes(lib)
                    ? 'bg-accent text-accent-fg'
                    : 'bg-line text-fg-muted hover:bg-line-strong'
                "
                @click="toggleWindowLib(w, lib)"
              >
                {{ LIBRARY_LABEL[lib] }}
              </button>
              <span v-if="w.libs.length === 0" class="text-[10px] text-fg-dim">（不限）</span>
            </div>
          </div>
          <button class="text-[10px] text-accent hover:underline" @click="addWindow">
            + 加一个时间段
          </button>
          <p class="text-[10px] leading-relaxed text-fg-dim">
            只有「学哪些」里勾选的库能在这个时间段学新内容。算法建议只放上午 ——
            下午脑子钝的时候啃算法，效率会很低。
          </p>
        </div>
      </div>

      <!-- 各库每条耗时：算法和八股根本不是一个速度 -->
      <div v-if="selectedLibraries.length" class="mt-3 rounded-lg bg-panel-soft px-3 py-2">
        <p class="mb-1.5 text-[11px] font-medium text-fg-muted">
          学一条大概要多久（算法一篇长文和一道八股题差很多）
        </p>
        <div class="flex flex-wrap gap-3">
          <label v-for="lib in selectedLibraries" :key="lib" class="flex items-center gap-1.5">
            <span class="text-[11px] text-fg-dim">{{ LIBRARY_LABEL[lib] ?? lib }}</span>
            <input
              v-model.number="draftPerItem[lib]"
              type="number"
              min="1"
              max="600"
              step="1"
              class="field w-16 py-1 text-xs"
            />
            <span class="text-[10px] text-fg-dim">分钟</span>
          </label>
        </div>
      </div>

      <!-- 停滞日 -->
      <div class="mt-3 rounded-lg bg-panel-soft px-3 py-2">
        <button
          class="flex w-full items-center justify-between text-[11px] font-medium text-fg-muted"
          @click="advancedOpen = !advancedOpen"
        >
          <span>停滞日：哪天不学（可留空）</span>
          <span class="text-fg-dim">{{ advancedOpen ? '收起 ↑' : '展开 ↓' }}</span>
        </button>

        <div v-if="advancedOpen" class="mt-2 space-y-2">
          <div class="flex flex-wrap items-center gap-1.5">
            <span class="text-[10px] text-fg-dim">每周固定休</span>
            <button
              v-for="(w, i) in WEEKDAYS"
              :key="i"
              class="rounded px-1.5 py-0.5 text-[10px] transition-colors"
              :class="
                draftWeeklyRest.includes(i)
                  ? 'bg-accent text-accent-fg'
                  : 'bg-line text-fg-muted hover:bg-line-strong'
              "
              @click="toggleWeekday(i)"
            >
              {{ w }}
            </button>
          </div>

          <div class="flex flex-wrap items-center gap-1.5">
            <input v-model="newRestDay" type="date" :min="today()" class="field w-32 py-1 text-xs" />
            <button class="btn btn-outline px-2 py-0.5 text-[10px]" @click="addRestDay">加一天</button>
          </div>

          <div v-if="draftRestDays.length" class="flex flex-wrap gap-1">
            <button
              v-for="d in draftRestDays"
              :key="d"
              class="chip bg-line text-[10px] text-fg-muted hover:bg-red-500/20 hover:text-red-500"
              :title="`点击移除 ${d}`"
              @click="removeRestDay(d)"
            >
              {{ d }} ×
            </button>
          </div>
          <p class="text-[10px] leading-relaxed text-fg-dim">
            加了停滞日，计划会自动按剩下的有效天数重排 —— 不会让你第二天补双份。
          </p>
        </div>
      </div>

      <div class="mt-2 rounded-lg bg-panel-soft px-3 py-2 text-[11px] leading-relaxed text-fg-muted">
        已选 <span class="font-semibold text-fg">{{ selectedItems.length }}</span> 个模块，
        待学 <span class="font-semibold text-fg">{{ draftTotal }}</span> 条 ·
        有效学习日 <span class="font-semibold text-fg">{{ effectiveStudyDays }}</span> 天
        <br />
        预计需 <span class="font-semibold text-fg">{{ Math.round(totalMinutesEstimate / 60) }}</span> 小时，
        按每天 {{ draftMinutes }} 分钟约
        <span class="font-semibold text-fg">{{ estimateDays }}</span> 天学完
        <span v-if="estimateDays > effectiveStudyDays" class="text-amber-glow">
          （超出可用天数 {{ estimateDays - effectiveStudyDays }} 天，建议延后截止日或加时间）
        </span>
        <span class="text-fg-dim">· 未计复习</span>
      </div>

      <p v-if="error" class="mt-2 text-[11px] text-red-500">{{ error }}</p>

      <div class="mt-3 flex items-center gap-2">
        <button class="btn btn-primary px-3 py-1.5 text-xs" :disabled="saving" @click="save">
          {{ saving ? '保存中…' : '保存任务' }}
        </button>
        <button class="btn btn-ghost px-3 py-1.5 text-xs" @click="open = false">取消</button>
        <button
          v-if="study.goal"
          class="btn btn-ghost ml-auto px-2 py-1 text-[11px] text-red-500 hover:bg-red-500/10"
          :disabled="saving"
          @click="clear"
        >
          删除任务
        </button>
      </div>
    </div>
  </div>
</template>
