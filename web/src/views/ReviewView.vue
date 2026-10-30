<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import AppHeader from '@/components/AppHeader.vue'
import { useContentStore, useStudyStore } from '@/stores'
import {
  GRADE_ACTIONS,
  capacityNote,
  capLineHeight,
  dueLabel,
  finishedHeadline,
  loadHeights,
  nextLabel,
  overloadWarning,
  queueHeadline,
  stageLabel,
  todayPercent,
} from '@/utils/review'
import type { GradeAction } from '@/utils/review'
import { LIBRARY_LABEL } from '@/utils/plan'
import type { ReviewGrade, ReviewDayLoad, ReviewPlan } from '@/types'

/** 各库默认每日复习额度，和服务端 DefaultLibCap 保持一致。 */
const DEFAULT_LIB_CAP: Record<string, number> = { interview: 30, algo: 6, ai: 15 }

const content = useContentStore()
const study = useStudyStore()

const plan = ref<ReviewPlan | null>(null)
const loading = ref(true)
const error = ref('')
const grading = ref(false)
const done = ref(false)
/** 提前复习的天数窗口：0 = 只看今天到期的 */
const aheadWindow = ref(0)

async function load(ahead = aheadWindow.value) {
  loading.value = true
  error.value = ''
  try {
    plan.value = await study.loadReviewPlan(undefined, ahead)
    draftCap.value = plan.value.cap
    draftEnabled.value = plan.value.enabled
    draftLibCap.value = { ...DEFAULT_LIB_CAP, ...(plan.value.libCap ?? {}) }
    done.value = plan.value.queue.length === 0
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

/** 请求提前复习未来 N 天的内容。 */
async function pullAhead(days: number) {
  aheadWindow.value = days
  await load(days)
}

const settingsOpen = ref(false)
const draftCap = ref(40)
const draftEnabled = ref(true)
const draftLibCap = ref<Record<string, number>>({ ...DEFAULT_LIB_CAP })
const savingSettings = ref(false)

onMounted(async () => {
  await content.loadManifest()
  if (!study.loaded) await study.load()
  await load()
})

/** 当前正在复习的那一条。 */
const current = computed(() => {
  if (!plan.value || done.value) return null
  return plan.value.queue[0] ?? null
})

/** 找到条目所在文档与标题，用于显示来源。 */
const currentMeta = computed(() => {
  if (!current.value) return null
  const doc = content.metaOf(current.value.docId)
  return doc ?? null
})

const currentItem = computed(() => {
  if (!current.value) return null
  const doc = content.docs[current.value.docId]
  if (!doc) return null
  for (const s of doc.sections) {
    const found = s.items.find((i) => i.id === current.value!.itemId)
    if (found) return found
  }
  return null
})

/** 标题可能还没加载（只拿到 manifest），退化成条目 id。 */
const currentTitle = computed(
  () => currentItem.value?.title ?? currentMeta.value?.title ?? current.value?.itemId ?? '',
)

/**
 * 进入页面时把当前条目的正文拉下来。
 * 复习的关键是「看着题回想」，所以正文默认是折叠的 ——
 * 直接摊开就变成重读，不是复习。
 */
const revealBody = ref(false)
watch(current, async (it) => {
  revealBody.value = false
  if (!it) return
  try {
    await content.loadDoc(it.docId)
  } catch {
    // 正文加载失败不影响评分
  }
})

const doneCount = computed(() => plan.value?.doneToday ?? 0)
const totalToday = computed(() => (plan.value ? plan.value.doneToday + plan.value.queue.length : 0))
const percent = computed(() => (plan.value ? todayPercent(plan.value) : 0))

async function grade(action: GradeAction) {
  const it = current.value
  if (!it || grading.value) return
  grading.value = true
  try {
    await study.gradeReview(it.docId, it.itemId, action.grade as ReviewGrade)
    // 本地摘掉这一条，再刷新统计（deferred / doneToday 以服务端为准）
    const remaining = plan.value ? plan.value.queue.slice(1) : []
    const localDone = (plan.value?.doneToday ?? 0) + (action.grade !== 3 ? 1 : 0)
    const fresh = await study.loadReviewPlan(undefined, aheadWindow.value)
    // 用本地已推进的队列覆盖服务端返回值，避免刚做完的又被排回来。
    plan.value = { ...fresh, queue: remaining, doneToday: Math.max(fresh.doneToday, localDone) }
    if (remaining.length === 0) done.value = true
  } catch (e) {
    error.value = e instanceof Error ? e.message : '记录失败'
  } finally {
    grading.value = false
  }
}

/** 快捷键：1~4 对应四个评分。 */
function onKeydown(e: KeyboardEvent) {
  const t = e.target as HTMLElement | null
  if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA')) return
  const action = GRADE_ACTIONS.find((a) => a.key === e.key)
  if (action) {
    e.preventDefault()
    void grade(action)
    return
  }
  if (e.key === ' ') {
    e.preventDefault()
    revealBody.value = !revealBody.value
  }
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))

async function saveSettings() {
  savingSettings.value = true
  error.value = ''
  try {
    await study.saveReviewConfig(draftCap.value, draftEnabled.value, draftLibCap.value)
    settingsOpen.value = false
    done.value = false
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存失败'
  } finally {
    savingSettings.value = false
  }
}

// ---------- 负载预测 ----------
/**
 * 图表用的序列。
 *
 * 「今天」这一格不能直接用 dueTotal —— 提前复习时今天到期是 0 条、
 * 但确实有 20 条要做，柱子会塌成 0，跟上面的文字自相矛盾。
 * 所以今天取 max(到期数, 队列长度)，其余日子仍按实际排程。
 */
const chartLoads = computed<ReviewDayLoad[]>(() => {
  if (!plan.value) return []
  return plan.value.forecast.map((f, i) =>
    i === 0 ? { ...f, count: Math.max(f.count, plan.value!.queue.length) } : f,
  )
})

const heights = computed(() => (chartLoads.value ? loadHeights(chartLoads.value) : []))
const capLine = computed(() =>
  plan.value ? capLineHeight(chartLoads.value, plan.value.cap) : 0,
)

const warning = computed(() => (plan.value ? overloadWarning(plan.value) : ''))
const capacity = computed(() => (plan.value ? capacityNote(plan.value) : ''))
const headline = computed(() => (plan.value ? queueHeadline(plan.value) : ''))
const finished = computed(() => (plan.value ? finishedHeadline(plan.value) : ''))

/** 未来负载的日期简写（只显示月/日）。 */
function shortDay(day: string): string {
  const parts = day.split('-')
  return parts.length === 3 ? `${Number(parts[1])}/${Number(parts[2])}` : day
}

const toneClass: Record<GradeAction['tone'], string> = {
  red: 'bg-red-500/15 text-red-500 hover:bg-red-500/25 border-red-500/30',
  amber: 'bg-amber-glow/15 text-amber-glow hover:bg-amber-glow/25 border-amber-glow/30',
  mint: 'bg-mint-500/15 text-mint-600 dark:text-mint-400 hover:bg-mint-500/25 border-mint-500/30',
  ghost: 'bg-panel-soft text-fg-muted hover:bg-line border-line',
}
</script>

<template>
  <AppHeader />

  <main class="mx-auto max-w-3xl px-4 pb-24 pt-6 sm:px-6">
    <!-- 顶部总览 -->
    <div class="card animate-rise p-5 sm:p-6">
      <div class="flex flex-wrap items-start gap-3">
        <div class="min-w-0 flex-1">
          <h1 class="text-lg font-semibold text-fg">今日复习</h1>
          <p class="mt-1 text-sm text-fg-muted">{{ headline }}</p>
        </div>
        <button class="btn btn-outline px-2.5 py-1 text-[11px]" @click="settingsOpen = !settingsOpen">
          设置
        </button>
      </div>

      <!-- 今日进度 -->
      <div v-if="plan && plan.enabled" class="mt-4">
        <div class="flex items-baseline justify-between text-[11px] text-fg-dim">
          <span>
            今日已完成 {{ doneCount }} / {{ totalToday }}
            <span v-if="plan.aheadTotal > 0" class="text-accent">（含提前复习 {{ plan.aheadTotal }} 条）</span>
          </span>
          <span class="tabular-nums">{{ percent }}%</span>
        </div>
        <div class="mt-1.5 h-2 w-full overflow-hidden rounded-full bg-line">
          <div
            class="h-full rounded-full bg-accent transition-[width] duration-500"
            :style="{ width: `${percent}%` }"
          />
        </div>
      </div>

      <!-- 关键统计 -->
      <dl v-if="plan" class="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-4">
        <div class="rounded-lg bg-panel-soft px-2.5 py-2">
          <dt class="text-[10px] text-fg-dim">在籍内容</dt>
          <dd class="mt-0.5 text-base font-semibold tabular-nums text-fg">{{ plan.enrolled }}</dd>
        </div>
        <div class="rounded-lg bg-panel-soft px-2.5 py-2">
          <dt class="text-[10px] text-fg-dim">逾期</dt>
          <dd
            class="mt-0.5 text-base font-semibold tabular-nums"
            :class="plan.backlog > 0 ? 'text-amber-glow' : 'text-fg'"
          >
            {{ plan.backlog }}
          </dd>
        </div>
        <div class="rounded-lg bg-panel-soft px-2.5 py-2">
          <dt class="text-[10px] text-fg-dim">已毕业</dt>
          <dd class="mt-0.5 text-base font-semibold tabular-nums text-mint-600 dark:text-mint-400">
            {{ plan.graduated }}
          </dd>
        </div>
        <div class="rounded-lg bg-panel-soft px-2.5 py-2">
          <dt class="text-[10px] text-fg-dim">每日上限</dt>
          <dd class="mt-0.5 text-base font-semibold tabular-nums text-fg">{{ plan.cap }}</dd>
        </div>
      </dl>

      <!-- 负载预警 -->
      <p
        v-if="warning"
        class="mt-3 rounded-lg bg-amber-glow/10 px-3 py-2 text-[11px] leading-relaxed text-amber-glow"
      >
        ⚠ {{ warning }}
      </p>

      <!-- 容量说明：解释为什么轮数会被压缩 -->
      <p v-if="capacity" class="mt-2 text-[11px] leading-relaxed text-fg-dim">{{ capacity }}</p>

      <!-- 设置面板 -->
      <div v-if="settingsOpen" class="mt-4 border-t border-line pt-3">
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block">
            <span class="mb-1 block text-[11px] text-fg-dim">每天最多复习多少条</span>
            <input v-model.number="draftCap" type="number" min="1" max="500" class="field py-1.5 text-xs" />
          </label>
          <label class="flex items-end gap-2 pb-1.5">
            <input v-model="draftEnabled" type="checkbox" class="h-3.5 w-3.5 accent-[var(--color-accent)]" />
            <span class="text-[11px] text-fg-muted">启用复习计划（标记掌握时自动入列）</span>
          </label>
        </div>
        <p class="mt-2 text-[11px] leading-relaxed text-fg-dim">
          上限是整个模块的闸门：到期内容超过上限时，多出来的部分会被自动摊到未来几天，
          越牢固的内容排得越远。所以设多少，你每天就只做多少。
        </p>

        <!-- 各库单独额度：算法复习一条要十几分钟，用同一个额度会被八股挤死 -->
        <div class="mt-3">
          <p class="mb-1.5 text-[11px] font-medium text-fg-muted">
            各库每天的复习条数（算法慢，额度要小）
          </p>
          <div class="flex flex-wrap gap-3">
            <label
              v-for="lib in ['interview', 'algo', 'ai']"
              :key="lib"
              class="flex items-center gap-1.5"
            >
              <span class="text-[11px] text-fg-dim">{{ LIBRARY_LABEL[lib] }}</span>
              <input
                v-model.number="draftLibCap[lib]"
                type="number"
                min="1"
                max="500"
                step="1"
                class="field w-16 py-1 text-xs"
              />
              <span class="text-[10px] text-fg-dim">条/天</span>
            </label>
          </div>
          <p class="mt-1 text-[10px] leading-relaxed text-fg-dim">
            必须分开：算法一条复习要十几分钟，八股只要几分钟。共用一个额度时，
            八股会先占满队列，算法的复习永远排不上。
          </p>
        </div>
        <div class="mt-2 flex gap-2">
          <button class="btn btn-primary px-3 py-1.5 text-xs" :disabled="savingSettings" @click="saveSettings">
            {{ savingSettings ? '保存中…' : '保存' }}
          </button>
          <button class="btn btn-ghost px-3 py-1.5 text-xs" @click="settingsOpen = false">取消</button>
        </div>
      </div>
    </div>

    <p v-if="error" class="mt-3 rounded-lg bg-red-500/10 px-3 py-2 text-xs text-red-500">{{ error }}</p>

    <!-- 加载 / 关闭 / 空 -->
    <div v-if="loading" class="card mt-4 p-10 text-center text-sm text-fg-dim">加载中…</div>

    <div v-else-if="plan && !plan.enabled" class="card mt-4 p-8 text-center">
      <p class="text-sm text-fg-muted">复习计划已关闭</p>
      <p class="mt-2 text-xs leading-relaxed text-fg-dim">
        打开后，每标记一道「已掌握」的内容都会自动按遗忘曲线排进复习计划。
      </p>
      <button class="btn btn-primary mt-4 px-3 py-1.5 text-xs" @click="settingsOpen = true; draftEnabled = true">
        去开启
      </button>
    </div>

    <!-- 今天没有要复习的 -->
    <div v-else-if="done && (!plan || plan.queue.length === 0)" class="card mt-4 p-8 text-center">
      <p class="text-2xl">✓</p>
      <p class="mt-2 text-sm font-medium text-fg">{{ finished }}</p>
      <p v-if="plan && plan.nextDueDay && plan.dueTotal === 0" class="mt-2 text-xs text-fg-dim">
        下次复习：{{ plan.nextDueDay }}
      </p>
      <div class="mt-4 flex flex-wrap justify-center gap-2">
        <button
          v-if="plan && plan.enrolled > 0"
          class="btn btn-primary text-xs"
          :disabled="loading"
          @click="pullAhead(3)"
        >
          提前复习未来 3 天 →
        </button>
        <RouterLink to="/" class="btn btn-outline text-xs">去学新内容</RouterLink>
        <RouterLink to="/notes" class="btn btn-ghost text-xs">看我的笔记</RouterLink>
      </div>
      <p v-if="plan && plan.enrolled > 0" class="mt-3 text-[11px] leading-relaxed text-fg-dim">
        提前复习不会打乱节奏：仍按当前阶段推进，只是把未来几天到期的内容先做了，
        后面那几天自然就轻松了。
      </p>
    </div>

    <!-- 复习卡片 -->
    <div v-else-if="current" class="mt-4">
      <div class="card animate-rise overflow-hidden">
        <div class="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
          <span class="chip bg-accent-soft text-accent">{{ stageLabel(current, plan?.stages ?? 7) }}</span>
          <span
            class="chip"
            :class="current.overdue > 0 ? 'bg-amber-glow/15 text-amber-glow' : 'bg-line text-fg-dim'"
          >
            {{ dueLabel(current) }}
          </span>
          <span v-if="current.lapses > 0" class="chip bg-red-500/10 text-red-500">
            忘过 {{ current.lapses }} 次
          </span>
          <span v-if="aheadWindow > 0" class="chip bg-accent-soft text-accent">提前复习</span>
          <span class="ml-auto text-[11px] text-fg-dim">
            还剩 {{ plan?.queue.length ?? 0 }} 条
          </span>
        </div>

        <div class="px-4 py-4">
          <p v-if="currentMeta" class="text-[11px] text-fg-dim">{{ currentMeta.title }}</p>
          <h2 class="mt-1 text-lg font-semibold leading-snug text-fg">{{ currentTitle }}</h2>

          <!-- 正文默认折叠：复习是回想，不是重读 -->
          <button
            class="mt-3 flex items-center gap-1.5 text-[11px] text-accent hover:opacity-80"
            @click="revealBody = !revealBody"
          >
            {{ revealBody ? '收起正文 ↑' : '先回想一下，再看正文 ↓' }}
          </button>
          <div v-if="revealBody" class="mt-3 border-t border-line pt-3">
            <div v-if="currentItem" class="doc-body" v-html="currentItem.html" />
            <p v-else class="py-4 text-center text-xs text-fg-dim">正文加载中…</p>
          </div>
        </div>

        <!-- 评分按钮 -->
        <div class="border-t border-line px-4 py-3">
          <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <button
              v-for="a in GRADE_ACTIONS"
              :key="a.grade"
              class="rounded-xl border px-3 py-2.5 text-left transition-colors disabled:opacity-50"
              :class="toneClass[a.tone]"
              :disabled="grading"
              :title="a.hint"
              @click="grade(a)"
            >
              <span class="block text-sm font-medium">{{ a.label }}</span>
              <span class="mt-0.5 block text-[10px] opacity-80">{{ a.hint }}</span>
            </button>
          </div>
          <p class="mt-2 text-center text-[10px] text-fg-dim">
            快捷键 1 / 2 / 3 / 4 · 空格看正文
          </p>
        </div>
      </div>

      <!-- 原文入口 -->
      <div class="mt-3 flex justify-end">
        <RouterLink
          v-if="current"
          :to="{ name: 'reader', params: { slug: current.docId }, hash: `#item-${current.itemId}` }"
          class="btn btn-ghost text-[11px]"
        >
          在原文中打开 →
        </RouterLink>
      </div>
    </div>

    <!-- 未来负载预测 -->
    <div v-if="plan && plan.forecast.length" class="card mt-4 p-5">
      <div class="mb-3 flex items-center justify-between">
        <h2 class="text-sm font-semibold text-fg">未来 14 天</h2>
        <span class="text-[11px] text-fg-dim">每日上限 {{ plan.cap }} 条</span>
      </div>
      <div class="relative flex items-end gap-1" style="height: 80px">
        <!-- 上限参考线：只在真的会越过时才画 -->
        <div
          v-if="capLine > 0 && capLine < 1"
          class="pointer-events-none absolute inset-x-0 border-t border-dashed border-amber-glow/60"
          :style="{ bottom: `${capLine * 72}px` }"
          :title="`每日上限 ${plan.cap} 条`"
        />
        <div
          v-for="(f, i) in chartLoads"
          :key="f.day"
          class="group relative flex-1"
          :title="`${f.day}：${f.count} 条`"
        >
          <div class="flex h-20 flex-col justify-end">
            <div
              class="w-full rounded-t transition-[height] duration-300"
              :class="
                i === 0
                  ? 'bg-accent'
                  : f.count > plan.cap
                    ? 'bg-amber-glow'
                    : 'bg-accent/40'
              "
              :style="{ height: `${Math.max(f.count > 0 ? 3 : 0, heights[i] * 72)}px` }"
            />
          </div>
        </div>
      </div>
      <div class="mt-1 flex justify-between text-[10px] text-fg-dim">
        <span>今天</span>
        <span>{{ shortDay(plan.forecast[plan.forecast.length - 1].day) }}</span>
      </div>
      <p class="mt-2 text-[11px] leading-relaxed text-fg-dim">
        柱高按未来峰值归一化；超过每日上限的日子标为黄色，系统会在开盘时自动往前后摊一些。
      </p>
    </div>

    <footer class="mt-12 text-center text-[11px] leading-relaxed text-fg-dim">
      复习间隔按艾宾浩斯遗忘曲线排布（1、3、7、14、29、59、119 天），
      并根据你的实际学习量自动调节轮数，保证每天都做得完。
    </footer>
  </main>
</template>
