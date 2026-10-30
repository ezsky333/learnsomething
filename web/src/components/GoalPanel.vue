<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useContentStore, useStudyStore } from '@/stores'
import { computeGoalProgress, daysBetween, formatDay, paceOf, today } from '@/utils/goal'
import type { ModuleProgress, PaceLevel } from '@/utils/goal'
import type { DocMeta } from '@/types'

const content = useContentStore()
const study = useStudyStore()

const open = ref(false)
const saving = ref(false)
const error = ref('')

/** 编辑中的草稿 */
const draftSlugs = ref<string[]>([])
const draftDue = ref('')
const draftDaily = ref(0)

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
  },
  { immediate: true },
)

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

const draftDays = computed(() => {
  if (!draftDue.value) return 0
  return Math.max(1, daysBetween(today(), draftDue.value) + 1)
})

const draftDailyAuto = computed(() =>
  draftDays.value > 0 ? draftTotal.value / draftDays.value : 0,
)

const draftDailyEffective = computed(() =>
  draftDaily.value > 0 ? draftDaily.value : draftDailyAuto.value,
)

const draftEstimateDays = computed(() => {
  if (draftDailyEffective.value <= 0) return 0
  return Math.ceil(draftTotal.value / draftDailyEffective.value)
})

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
  computeGoalProgress(study.goal, content.docMetas, doneOf),
)

const pace = computed(() => paceOf(progress.value))

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
      <span v-if="study.goal" class="ml-auto text-[11px] font-medium" :class="paceColor">
        {{ paceText }}
      </span>
    </div>

    <!-- 已设置任务：显示今日应有 / 差距 -->
    <template v-if="study.goal && !open">
      <div class="flex items-end justify-between gap-3">
        <div>
          <p class="text-[11px] text-fg-dim">今日应有进度</p>
          <p class="text-xl font-semibold text-fg tabular-nums">
            {{ Math.round(progress.expectedDone) }}
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
          :title="`今天应有 ${Math.round(progress.expectedDone)} 条`"
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

      <!-- 分模块进度：每个模块单独算「今天到哪儿、差多少」 -->
      <div v-if="progress.modules.length" class="mt-4 border-t border-line pt-3">
        <p class="mb-2 text-[11px] font-medium text-fg-muted">分模块进度</p>
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
            <!-- 每模块一条双刻度进度条 -->
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
                :title="`今天应有 ${Math.round(m.expectedDone)} 条`"
              />
            </div>
            <p class="mt-0.5 text-[10px] text-fg-dim">
              今天应到 {{ Math.round(m.expectedDone) }} 条
              <span v-if="m.todayRemaining > 0" class="text-amber-glow">· 还差 {{ m.todayRemaining }} 条</span>
              <span v-else class="text-mint-500 dark:text-mint-400">· 已达标</span>
              <span class="text-fg-dim/70">（每天 {{ m.dailyTarget.toFixed(1) }} 条）</span>
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
        <label class="block">
          <span class="mb-1 block text-[11px] text-fg-dim">
            每日目标（留 0 = 自动均摊）
          </span>
          <input
            v-model.number="draftDaily"
            type="number"
            min="0"
            step="1"
            placeholder="0"
            class="field py-1.5 text-xs"
          />
        </label>
      </div>

      <div class="mt-2 rounded-lg bg-panel-soft px-3 py-2 text-[11px] leading-relaxed text-fg-muted">
        已选 <span class="font-semibold text-fg">{{ selectedItems.length }}</span> 个模块，
        共 <span class="font-semibold text-fg">{{ draftTotal }}</span> 条 ·
        剩余 <span class="font-semibold text-fg">{{ draftDays }}</span> 天 →
        每天约 <span class="font-semibold text-fg">{{ draftDailyEffective.toFixed(1) }}</span> 条
        <span v-if="draftDaily > 0" class="text-amber-glow">
          （自定目标，按此进度约 {{ draftEstimateDays }} 天完成）
        </span>
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
