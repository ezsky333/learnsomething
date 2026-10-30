<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import AppHeader from '@/components/AppHeader.vue'
import GoalPanel from '@/components/GoalPanel.vue'
import Heatmap from '@/components/Heatmap.vue'
import ProgressBar from '@/components/ProgressBar.vue'
import ProgressRing from '@/components/ProgressRing.vue'
import { useContentStore, useStudyStore } from '@/stores'
import type { DocMeta, Library } from '@/types'

const content = useContentStore()
const study = useStudyStore()
const route = useRoute()
const router = useRouter()

const LIB_KEY = 'ls-library'
const LIBRARIES: Library[] = ['interview', 'algo', 'ai']
const library = ref<Library>(readLibrary())

function readLibrary(): Library {
  const q = route.query.lib
  if (q === 'algo' || q === 'interview' || q === 'ai') return q
  const saved = localStorage.getItem(LIB_KEY)
  return LIBRARIES.includes(saved as Library) ? (saved as Library) : 'interview'
}

watch(library, (v) => {
  localStorage.setItem(LIB_KEY, v)
  const query = { ...route.query }
  if (v === 'interview') delete query.lib
  else query.lib = v
  void router.replace({ query })
})

onMounted(async () => {
  await content.loadManifest()
  if (!study.loaded) await study.load()
})

const scopeDocs = computed(() => {
  if (library.value === 'algo') return content.algoDocs
  if (library.value === 'ai') return content.aiDocs
  return content.interviewDocs
})

const scopeGroups = computed(() => {
  if (library.value === 'algo') return content.algoGroups
  if (library.value === 'ai') return content.aiGroups
  return content.interviewGroups
})

interface DocRow extends DocMeta {
  done: number
  learning: number
  starred: number
  notes: number
  percent: number
}

const rows = computed<DocRow[]>(() =>
  scopeDocs.value.map((meta) => {
    const stats = study.docStats(meta.slug, meta.questionCount)
    return {
      ...meta,
      done: stats.done,
      learning: stats.learning,
      starred: stats.starred,
      notes: study.noteCountByDoc[meta.slug] ?? 0,
      percent: stats.percent,
    }
  }),
)

const grouped = computed(() => {
  const bySlug = new Map(rows.value.map((r) => [r.slug, r]))
  return scopeGroups.value.map((g) => ({
    title: g.title,
    items: g.items.map((d) => bySlug.get(d.slug)).filter((r): r is DocRow => !!r),
  }))
})

const overall = computed(() => {
  const total = scopeDocs.value.reduce((s, d) => s + d.questionCount, 0)
  const done = rows.value.reduce((s, r) => s + r.done, 0)
  return {
    total,
    done,
    learning: rows.value.reduce((s, r) => s + r.learning, 0),
    starred: rows.value.reduce((s, r) => s + r.starred, 0),
    percent: total > 0 ? Math.round((done / total) * 100) : 0,
  }
})

/** 两个库各自的进度，用于切换按钮上的小徽标。 */
const libraryStats = computed(() => {
  const calc = (docs: DocMeta[]) => {
    const total = docs.reduce((s, d) => s + d.questionCount, 0)
    let done = 0
    for (const d of docs) done += study.docStats(d.slug, d.questionCount).done
    return { total, done, percent: total ? Math.round((done / total) * 100) : 0 }
  }
  return {
    interview: calc(content.interviewDocs),
    algo: calc(content.algoDocs),
    ai: calc(content.aiDocs),
  }
})

const estimatedHours = computed(() => {
  const chars = scopeDocs.value.reduce((s, d) => s + d.charCount, 0)
  const minutes = (chars / 400) * 2.2
  return Math.max(1, Math.round(minutes / 60))
})

const nextUp = computed<DocRow | undefined>(() => {
  const unfinished = rows.value.filter((r) => r.percent < 100)
  return unfinished.sort((a, b) => b.percent - a.percent || a.order - b.order)[0]
})

const motivation = computed(() => {
  const p = overall.value.percent
  const algo = library.value === 'algo'
  const ai = library.value === 'ai'
  if (p >= 100) {
    if (algo) return '算法全通了，去刷 Hot100 检验 🎯'
    if (ai) return '大模型题库全刷完了，去准备项目吧 🎯'
    return '全部拿下，接下来去刷真题吧 🎯'
  }
  if (p >= 85) return '只剩最后几块硬骨头，别停 💪'
  if (p >= 60) return '过半了，这个节奏很稳 🚀'
  if (p >= 30) return '已经把大头啃下来了，继续 ✊'
  if (p > 0) return algo ? '开了个头最重要，今天再推一篇 🌱' : '开了个头最重要，今天再推一题 🌱'
  if (algo) return '建议从「数组」开始，一题一题把框架搭起来 🌤'
  if (ai) return '建议从 Agent 第 1 题开始，先把概念讲清楚 🌤'
  return '选一篇开始吧：Java 基础是最稳的起点 🌤'
})
</script>

<template>
  <AppHeader />

  <main class="mx-auto max-w-[1600px] px-4 pb-20 pt-6 sm:px-6">
    <!-- 学习区切换 -->
    <div class="mb-5 flex flex-wrap items-center gap-2">
      <div class="flex items-center gap-0.5 rounded-xl bg-panel-soft p-1">
        <button
          v-for="lib in [
            { k: 'interview', label: '面试八股', hint: '小林coding' },
            { k: 'algo', label: '算法教程', hint: '代码随想录' },
            { k: 'ai', label: '大模型面试', hint: '小林面试笔记' },
          ]"
          :key="lib.k"
          class="group flex items-center gap-2 rounded-lg px-3 py-1.5 text-xs transition-colors"
          :class="
            library === lib.k
              ? 'bg-panel font-semibold text-fg shadow-sm'
              : 'text-fg-dim hover:text-fg'
          "
          @click="library = lib.k as Library"
        >
          {{ lib.label }}
          <span
            class="rounded-full px-1.5 py-0.5 text-[10px] tabular-nums"
            :class="library === lib.k ? 'bg-accent-soft text-accent' : 'bg-line text-fg-dim'"
          >
            {{ libraryStats[lib.k as Library].percent }}%
          </span>
        </button>
      </div>
      <span class="text-[11px] text-fg-dim">
        {{
          library === 'algo'
            ? '一篇长文 = 一条学习内容，读完再标记'
            : library === 'ai'
              ? '一道面试题 = 一条学习内容'
              : '一道题 = 一条学习内容'
        }}
      </span>
    </div>

    <section class="grid gap-4 lg:grid-cols-[1.15fr_1fr]">
      <div class="card animate-rise p-5 sm:p-6">
        <div class="flex items-start gap-5">
          <ProgressRing :value="overall.percent" :size="92" :stroke="7" />
          <div class="min-w-0 flex-1">
            <p class="text-xs font-medium uppercase tracking-wider text-fg-dim">总进度</p>
            <p class="mt-1 text-2xl font-semibold text-fg">
              {{ overall.done }}
              <span class="text-base font-normal text-fg-dim">/ {{ overall.total }}
                {{ library === 'algo' ? '篇' : '题' }}</span>
            </p>
            <p class="mt-2 text-sm text-fg-muted">{{ motivation }}</p>
          </div>
        </div>

        <dl class="mt-5 grid grid-cols-2 gap-3 sm:grid-cols-4">
          <div class="rounded-xl bg-panel-soft px-3 py-2.5">
            <dt class="text-[11px] text-fg-dim">学习中</dt>
            <dd class="mt-0.5 text-lg font-semibold text-amber-glow">{{ overall.learning }}</dd>
          </div>
          <div class="rounded-xl bg-panel-soft px-3 py-2.5">
            <dt class="text-[11px] text-fg-dim">已加星</dt>
            <dd class="mt-0.5 text-lg font-semibold text-accent">{{ overall.starred }}</dd>
          </div>
          <div class="rounded-xl bg-panel-soft px-3 py-2.5">
            <dt class="text-[11px] text-fg-dim">今日动作</dt>
            <dd class="mt-0.5 text-lg font-semibold text-mint-500 dark:text-mint-400">
              {{ study.todayCount }}
            </dd>
          </div>
          <div class="rounded-xl bg-panel-soft px-3 py-2.5">
            <dt class="text-[11px] text-fg-dim">连续天数</dt>
            <dd class="mt-0.5 text-lg font-semibold text-fg">{{ study.streak }}</dd>
          </div>
        </dl>

        <div
          v-if="nextUp"
          class="mt-5 flex flex-wrap items-center gap-3 rounded-xl bg-accent-soft px-3.5 py-3"
        >
          <span class="text-[11px] font-medium uppercase tracking-wider text-accent">继续</span>
          <RouterLink
            :to="{ name: 'reader', params: { slug: nextUp.slug } }"
            class="min-w-0 flex-1 truncate text-sm font-medium text-fg hover:text-accent"
          >
            {{ nextUp.title }}
          </RouterLink>
          <span class="text-xs text-fg-dim">{{ nextUp.done }}/{{ nextUp.questionCount }}</span>
        </div>
      </div>

      <div class="card animate-rise p-5 sm:p-6">
        <div class="mb-3 flex items-center justify-between">
          <h2 class="text-sm font-semibold text-fg">学习热力</h2>
          <span class="text-[11px] text-fg-dim">约 {{ estimatedHours }} 小时过完本区</span>
        </div>
        <Heatmap :counts="study.heatmap" />
      </div>
    </section>

    <!-- 学习任务：今日应有进度 / 超前落后 -->
    <section class="mt-4">
      <GoalPanel />
    </section>

    <section v-for="group in grouped" :key="group.title" class="mt-8">
      <h2 class="mb-3 flex items-center gap-2 text-sm font-semibold text-fg">
        <span class="h-3.5 w-0.5 rounded-full bg-accent" />
        {{ group.title }}
      </h2>
      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <RouterLink
          v-for="row in group.items"
          :key="row.slug"
          :to="{ name: 'reader', params: { slug: row.slug } }"
          class="card group relative overflow-hidden p-4 transition-colors hover:border-line-strong hover:bg-panel-raise"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <h3 class="truncate font-medium text-fg group-hover:text-accent">{{ row.title }}</h3>
              <p class="mt-1 text-[11px] text-fg-dim">
                <template v-if="row.library === 'algo'">
                  {{ row.questionCount }} 篇 · 约
                  {{ Math.max(1, Math.round((row.charCount / 400) * 2.2 / 60)) }} 小时
                </template>
                <template v-else-if="row.library === 'ai'">
                  {{ row.questionCount }} 题 · 约
                  {{ Math.max(1, Math.round((row.charCount / 400) * 2.2 / 60)) }} 小时
                </template>
                <template v-else>
                  {{ row.sectionCount }} 章节 · {{ row.questionCount }} 题 · 约
                  {{ Math.max(1, Math.round((row.charCount / 400) * 2.2 / 60)) }} 小时
                </template>
              </p>
            </div>
            <ProgressRing
              :value="row.percent"
              :size="40"
              :stroke="3.5"
              :tone="row.percent >= 100 ? 'mint' : 'brand'"
            />
          </div>

          <div class="mt-3">
            <ProgressBar
              :model-value="row.done"
              :total="row.questionCount"
              :tone="row.percent >= 100 ? 'mint' : 'brand'"
            />
            <div class="mt-2 flex items-center gap-3 text-[11px] text-fg-dim">
              <span class="tabular-nums">{{ row.done }}/{{ row.questionCount }}</span>
              <span v-if="row.learning" class="text-amber-glow">{{ row.learning }} 学习中</span>
              <span v-if="row.starred" class="text-accent">★ {{ row.starred }}</span>
              <span v-if="row.notes" class="ml-auto">✎ {{ row.notes }}</span>
            </div>
          </div>
        </RouterLink>
      </div>
    </section>

    <p
      v-if="!scopeDocs.length && !content.loading"
      class="py-20 text-center text-sm text-fg-dim"
    >
      {{
        library === 'algo'
          ? '算法内容为空，请先运行 go run ./cmd/algocrawler'
          : library === 'ai'
            ? '大模型题库为空，请先运行 go run ./cmd/aicrawler'
            : '内容为空，请先运行 go run ./cmd/crawler'
      }}
    </p>

    <footer class="mt-14 border-t border-line pt-6 text-center text-[11px] leading-relaxed text-fg-dim">
      <template v-if="library === 'algo'">
        内容抓取自
        <a
          href="https://programmercarl.com/algo/"
          target="_blank"
          rel="noopener noreferrer"
          class="text-accent hover:underline"
        >programmercarl.com</a>
        ，版权归原作者「代码随想录」所有，仅供个人学习。
      </template>
      <template v-else-if="library === 'ai'">
        内容抓取自
        <a
          href="https://xiaolinnote.com/ai/"
          target="_blank"
          rel="noopener noreferrer"
          class="text-accent hover:underline"
        >xiaolinnote.com</a>
        ，版权归原作者「小林面试笔记」所有，仅供个人学习。
      </template>
      <template v-else>
        内容抓取自
        <a
          href="https://xiaolincoding.com/interview/"
          target="_blank"
          rel="noopener noreferrer"
          class="text-accent hover:underline"
        >xiaolincoding.com</a>
        ，版权归原作者小林coding所有，仅供个人学习。
      </template>
    </footer>
  </main>
</template>
