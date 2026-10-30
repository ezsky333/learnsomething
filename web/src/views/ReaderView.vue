<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import AppHeader from '@/components/AppHeader.vue'
import NotePanel from '@/components/NotePanel.vue'
import ProgressBar from '@/components/ProgressBar.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { useContentStore, useStudyStore } from '@/stores'
import type { Doc, Item, Status } from '@/types'

const route = useRoute()
const router = useRouter()
const content = useContentStore()
const study = useStudyStore()

const slug = computed(() => String(route.params.slug ?? ''))
const doc = ref<Doc | null>(null)
const loading = ref(true)
const loadError = ref('')

const MODE_KEY = 'ls-read-mode'
type ReadMode = 'list' | 'focus'

// ---------- UI 状态 ----------
const tocOpen = ref(false)
const filter = ref<'all' | 'todo' | 'starred'>('all')
const openNotes = ref<Record<string, boolean>>({})
const focusedId = ref('')
const introOpen = ref(false)
const readMode = ref<ReadMode>(
  (localStorage.getItem(MODE_KEY) as ReadMode) === 'focus' ? 'focus' : 'list',
)
const cursor = ref(0)
const lightbox = ref('')
const toast = ref('')
const notesCollapsed = ref(false)
const mobileNotesOpen = ref(false)
let toastTimer: number | undefined

function showToast(msg: string) {
  toast.value = msg
  window.clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => (toast.value = ''), 1600)
}

watch(readMode, (v) => localStorage.setItem(MODE_KEY, v))

onMounted(async () => {
  await content.loadManifest()
  if (!study.loaded) await study.load()
  await loadDoc()
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('scroll', onScroll, { passive: true })
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('scroll', onScroll)
  window.clearTimeout(toastTimer)
})

watch(slug, async () => {
  tocOpen.value = false
  cursor.value = 0
  await loadDoc()
})

// 同一篇文档内 hash 变化（比如从笔记列表再次点进来）也要重新定位。
watch(
  () => route.hash,
  async () => {
    if (!doc.value || loading.value) return
    await applyHash()
  },
)

async function loadDoc() {
  loading.value = true
  loadError.value = ''
  doc.value = null
  try {
    doc.value = await content.loadDoc(slug.value)
    await applyHash()
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

/**
 * 把 URL 里的 hash 解成条目 id。
 * 浏览器会把中文等非 ASCII 字符百分号编码（#item-java%E4%B8%BA...），
 * 直接拿去 getElementById 永远找不到，必须先解码；产出的 id 还可能和 DOM id 不完全一致，
 * 所以再回查一次真实条目列表。
 */
function resolveHashItemId(): string {
  const raw = route.hash.replace(/^#/, '')
  if (!raw) return ''
  let decoded = raw
  try {
    decoded = decodeURIComponent(raw)
  } catch {
    // hash 里有孤立的 % 之类，保持原样
  }
  const candidate = decoded.replace(/^item-/, '')
  if (allRows.value.some((r) => r.item.id === candidate)) return candidate
  // 兜底：大小写/空格差异时做一次宽松匹配
  const loose = allRows.value.find(
    (r) => r.item.id.toLowerCase() === candidate.toLowerCase(),
  )
  return loose?.item.id ?? candidate
}

/**
 * 处理「进入文档」时的定位。
 *
 * - URL 带 hash：跳到指定条目（来自加星 / 笔记 / 站内链接）。
 * - URL 不带 hash：跳到**第一个没学完的条目**，也就是接着上次的进度继续，
 *   而不是永远从第 1 条开始。
 */
async function applyHash() {
  const raw = route.hash.replace(/^#/, '')
  const itemId = raw ? resolveHashItemId() : resumeItemId()
  const idx = itemId ? allRows.value.findIndex((r) => r.item.id === itemId) : -1

  if (raw && idx < 0) {
    // 内容更新后旧笔记/加星可能指向已不存在的条目。不要静默停在第 1 条，
    // 明确告诉用户，并把地址里的 hash 清掉以免反复触发。
    window.scrollTo({ top: 0 })
    showToast('这条内容在当前版本里已不存在')
    void router.replace({ name: 'reader', params: { slug: slug.value } })
    return
  }

  if (idx < 0) {
    window.scrollTo({ top: 0 })
    return
  }

  cursor.value = idx
  // 筛选条件只影响列表模式，这里复位以免下次切回列表时看不到目标。
  filter.value = 'all'
  readMode.value = 'focus'
  focusedId.value = itemId

  await nextTick()
  window.scrollTo({ top: 0, behavior: 'auto' })
}

/**
 * 「接着学」的位置：第一个未掌握的条目。
 *
 * 全都学完时退回最后一条（让用户看到自己学完的结果，而不是又跳回第 1 条）。
 * 全都没开始时返回第一条。
 */
function resumeItemId(): string {
  if (!doc.value || !allRows.value.length) return ''
  const docSlug = doc.value.slug
  const firstUndone = allRows.value.find((r) => study.statusOf(docSlug, r.item.id) !== 2)
  if (firstUndone) return firstUndone.item.id
  return allRows.value[allRows.value.length - 1].item.id
}

// ---------- 题目列表 ----------
interface Row {
  sectionId: string
  sectionTitle: string
  item: Item
}

/** 全部题目（不受筛选影响），学习模式按这个顺序走。 */
const allRows = computed<Row[]>(() => {
  if (!doc.value) return []
  const out: Row[] = []
  for (const section of doc.value.sections) {
    for (const item of section.items) out.push({ sectionId: section.id, sectionTitle: section.title, item })
  }
  return out
})

/** 列表模式显示的题目（受筛选影响）。 */
const visibleRows = computed<Row[]>(() => {
  if (filter.value === 'all') return allRows.value
  return allRows.value.filter((r) => {
    if (!doc.value) return false
    const st = study.statusOf(doc.value.slug, r.item.id)
    const starred = study.starredOf(doc.value.slug, r.item.id)
    if (filter.value === 'todo') return st !== 2
    return starred
  })
})

const currentRow = computed<Row | null>(() => allRows.value[cursor.value] ?? null)

/** 右侧笔记栏关注的题目：学习模式看游标，列表模式看滚动位置。 */
const activeItem = computed<Item | null>(() => {
  if (readMode.value === 'focus') return currentRow.value?.item ?? null
  if (!doc.value || !focusedId.value) return allRows.value[0]?.item ?? null
  return allRows.value.find((r) => r.item.id === focusedId.value)?.item ?? null
})

const activeNotes = computed(() => {
  if (!doc.value || !activeItem.value) return []
  return study.notesOf(doc.value.slug, activeItem.value.id)
})

// ---------- 统计 ----------
const stats = computed(() => {
  if (!doc.value) return { done: 0, total: 0, percent: 0, learning: 0, starred: 0 }
  const total = doc.value.questionCount
  let done = 0
  let learning = 0
  let starred = 0
  for (const row of allRows.value) {
    const st = study.statusOf(doc.value.slug, row.item.id)
    if (st === 2) done++
    else if (st === 1) learning++
    if (study.starredOf(doc.value.slug, row.item.id)) starred++
  }
  return { done, total, learning, starred, percent: total ? Math.round((done / total) * 100) : 0 }
})

const sectionProgress = computed(() => {
  const map = new Map<string, { done: number; total: number }>()
  if (!doc.value) return map
  for (const section of doc.value.sections) {
    let done = 0
    for (const item of section.items) {
      if (study.statusOf(doc.value.slug, item.id) === 2) done++
    }
    map.set(section.id, { done, total: section.items.length })
  }
  return map
})

const readProgress = ref(0)

function onScroll() {
  const el = document.documentElement
  const max = el.scrollHeight - el.clientHeight
  readProgress.value = max > 0 ? Math.min(100, Math.round((el.scrollTop / max) * 100)) : 0
  if (readMode.value !== 'list') return

  const cards = document.querySelectorAll<HTMLElement>('[data-item-id]')
  for (const card of cards) {
    const rect = card.getBoundingClientRect()
    if (rect.top <= 140 && rect.bottom > 140) {
      const id = card.dataset.itemId ?? ''
      if (id && id !== focusedId.value) focusedId.value = id
      break
    }
  }
}

// ---------- 操作 ----------
const flashIds = ref<Record<string, boolean>>({})

async function setStatus(item: Item, status: Status) {
  if (!doc.value) return
  const cur = study.statusOf(doc.value.slug, item.id)
  const next: Status = cur === status ? 0 : status
  try {
    await study.setStatus(doc.value.slug, item.id, next)
    if (next === 2) {
      flashIds.value[item.id] = true
      window.setTimeout(() => delete flashIds.value[item.id], 900)
    }
  } catch (e) {
    showToast(e instanceof Error ? e.message : '保存失败')
  }
}

/** 「已掌握并下一题」：标记为已掌握后前进。 */
async function markDoneAndNext(item: Item) {
  if (!doc.value) return
  await study.setStatus(doc.value.slug, item.id, 2).catch((e) => {
    showToast(e instanceof Error ? e.message : '保存失败')
  })
  goNext()
  // 标记掌握会自动进入复习计划，明确告知下次什么时候要回头看 ——
  // 否则用户会以为「点完就结束了」，这一条再也不会被想起来。
  const rv = study.reviewOf(doc.value.slug, item.id)
  if (rv && rv.dueDay) {
    showToast(`${doneLabel.value}，${rv.dueDay} 会提醒复习 →`)
  } else {
    showToast(`${doneLabel.value}，下一${unit.value} →`)
  }
}

async function markLearningAndNext(item: Item) {
  if (!doc.value) return
  await study.setStatus(doc.value.slug, item.id, 1).catch((e) => {
    showToast(e instanceof Error ? e.message : '保存失败')
  })
  goNext()
}

async function clearStatus(item: Item) {
  if (!doc.value) return
  await study.setStatus(doc.value.slug, item.id, 0).catch(() => {})
  showToast('已清除状态')
}

/**
 * 学习模式下切换条目。
 *
 * 每次切换都要把页面滚回顶部 —— 否则在长文里点了「已掌握，下一题」后，
 * 新卡片仍停在上一篇的滚动位置，用户看到的是文章中间，以为没跳。
 */
function goNext() {
  if (cursor.value < allRows.value.length - 1) {
    cursor.value++
    scrollToTop()
  } else {
    showToast(`已经是最后一${unit.value}了 🎉`)
  }
}

function goPrev() {
  if (cursor.value > 0) {
    cursor.value--
    scrollToTop()
  }
}

/** 切换条目后回到顶部（等 DOM 更新完再滚，避免被旧内容高度影响）。 */
function scrollToTop() {
  void nextTick().then(() => {
    window.scrollTo({ top: 0, behavior: 'auto' })
    focusedId.value = currentRow.value?.item.id ?? ''
  })
}

async function toggleStar(item: Item) {
  if (!doc.value) return
  try {
    await study.toggleStar(doc.value.slug, item.id)
  } catch (e) {
    showToast(e instanceof Error ? e.message : '保存失败')
  }
}

async function addNote(item: Item, text: string) {
  if (!doc.value) return
  try {
    await study.addNote(doc.value.slug, item.id, text)
    openNotes.value[item.id] = true
    showToast('笔记已保存')
  } catch (e) {
    showToast(e instanceof Error ? e.message : '保存失败')
  }
}

async function editNote(id: number, text: string) {
  try {
    await study.editNote(id, text)
    showToast('已更新')
  } catch (e) {
    showToast(e instanceof Error ? e.message : '更新失败')
  }
}

async function removeNote(id: number) {
  try {
    await study.removeNote(id)
    showToast('已删除')
  } catch (e) {
    showToast(e instanceof Error ? e.message : '删除失败')
  }
}

const activeNoteCount = computed(() => activeNotes.value.length)

// ---------- 快捷键 ----------
function isTyping(e: KeyboardEvent): boolean {
  const t = e.target as HTMLElement | null
  return !!t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)
}

function onKeydown(e: KeyboardEvent) {
  if (isTyping(e)) {
    if (e.key === 'Escape') (e.target as HTMLElement).blur()
    return
  }
  if (e.key === 'Escape') {
    if (lightbox.value) lightbox.value = ''
    else if (mobileNotesOpen.value) mobileNotesOpen.value = false
    return
  }
  if (!doc.value) return

  if (readMode.value === 'focus') {
    const item = currentRow.value?.item
    switch (e.key) {
      case 'ArrowRight':
      case 'j':
        e.preventDefault()
        goNext()
        return
      case 'ArrowLeft':
      case 'k':
        e.preventDefault()
        goPrev()
        return
      case 'Enter':
        e.preventDefault()
        if (item) void markDoneAndNext(item)
        return
      case 's':
        e.preventDefault()
        if (item) void toggleStar(item)
        return
      case '1':
        e.preventDefault()
        if (item) void markLearningAndNext(item)
        return
      case '2':
        e.preventDefault()
        if (item) void setStatus(item, 2)
        return
      case '3':
        e.preventDefault()
        if (item) void clearStatus(item)
        return
      case 'n':
        e.preventDefault()
        mobileNotesOpen.value = !mobileNotesOpen.value
        return
      default:
        return
    }
  }

  const rows = visibleRows.value
  if (!rows.length) return
  const idx = rows.findIndex((r) => r.item.id === focusedId.value)
  const cur = rows[idx] ?? rows[0]

  switch (e.key) {
    case 'j': {
      e.preventDefault()
      const next = rows[Math.min(rows.length - 1, idx + 1)]
      if (next) scrollTo(next.item.id)
      break
    }
    case 'k': {
      e.preventDefault()
      const prev = rows[Math.max(0, idx - 1)]
      if (prev) scrollTo(prev.item.id)
      break
    }
    case '1':
    case '2':
    case '3':
      e.preventDefault()
      void setStatus(cur.item, (Number(e.key) - 1) as Status)
      break
    case 's':
      e.preventDefault()
      void toggleStar(cur.item)
      break
    case 'n':
      e.preventDefault()
      if (activeItem.value) openNotes.value[activeItem.value.id] = true
      break
    default:
      break
  }
}

function scrollTo(itemId: string) {
  const el = document.getElementById(`item-${itemId}`)
  if (!el) return
  focusedId.value = itemId
  const top = el.getBoundingClientRect().top + window.scrollY - 64
  window.scrollTo({ top, behavior: 'smooth' })
}

function jumpTo(itemId: string) {
  tocOpen.value = false
  const idx = allRows.value.findIndex((r) => r.item.id === itemId)
  if (idx < 0) return
  cursor.value = idx
  // 目录、站内链接、上一篇/下一篇一律切到学习模式，避免长列表里找不到目标。
  readMode.value = 'focus'
  focusedId.value = itemId
  void nextTick().then(() => window.scrollTo({ top: 0, behavior: 'auto' }))
}

function onContentClick(e: MouseEvent) {
  const el = e.target as HTMLElement
  if (el.tagName === 'IMG') {
    const src = (el as HTMLImageElement).getAttribute('src')
    if (src) lightbox.value = src
    return
  }
  // 正文里的站内文章链接（#/doc/xxx#item-yyy）走前端路由，不整页刷新。
  const anchor = el.closest('a')
  if (!anchor) return
  const href = anchor.getAttribute('href') ?? ''
  if (!href.startsWith('#/doc/')) return
  e.preventDefault()
  const raw = href.slice(2) // doc/slug#item-id
  const [path, hash] = raw.split('#')
  const targetSlug = path.replace(/^doc\//, '')
  if (!targetSlug) return
  if (targetSlug === slug.value && hash) {
    jumpTo(hash.replace(/^item-/, ''))
  } else {
    void router.push({ name: 'reader', params: { slug: targetSlug }, hash: hash ? `#${hash}` : undefined })
  }
}

const isEmpty = computed(() => !!doc.value && visibleRows.value.length === 0)
const remaining = computed(() => stats.value.total - stats.value.done)

/** 算法库的每篇是长篇教学，界面措辞要跟着变。 */
const isAlgo = computed(() => doc.value?.library === 'algo')
const unit = computed(() => (isAlgo.value ? '篇' : '题'))
const doneLabel = computed(() => (isAlgo.value ? '已学完' : '已掌握'))
/** 未完成状态的措辞，注意和 doneLabel 不能简单拼「未」字。 */
const todoLabel = computed(() => (isAlgo.value ? '未学完' : '未掌握'))

/** 上一篇 / 下一篇（算法专题内按顺序推进用）。 */
const prevRow = computed(() => allRows.value[cursor.value - 1] ?? null)
const nextRow = computed(() => allRows.value[cursor.value + 1] ?? null)

/** 复习状态的极简提示：逾期几天 / 还有几天。 */
function reviewHint(rv: { overdue: number; interval: number }): string {
  if (rv.overdue > 0) return `逾期 ${rv.overdue} 天`
  if (rv.interval <= 0) return '待复习'
  return `${rv.interval} 天后`
}
</script>

<template>
  <AppHeader />

  <!-- 阅读进度 -->
  <div v-if="readMode === 'list'" class="fixed left-0 top-14 z-40 h-0.5 w-full">
    <div class="h-full bg-accent/70 transition-[width] duration-150" :style="{ width: `${readProgress}%` }" />
  </div>

  <div class="mx-auto flex max-w-[1600px] gap-6 px-4 pb-24 pt-5 sm:px-6">
    <!-- 左：目录 -->
    <aside class="sticky top-20 hidden h-[calc(100dvh-6rem)] w-60 shrink-0 overflow-y-auto xl:block">
      <div v-if="doc" class="pb-8">
        <RouterLink to="/" class="mb-3 inline-flex items-center gap-1 text-xs text-fg-dim hover:text-fg">
          ← 返回看板
        </RouterLink>
        <h2 class="text-sm font-semibold leading-snug text-fg">{{ doc.title }}</h2>
        <div class="mt-2">
          <ProgressBar :model-value="stats.done" :total="stats.total" :tone="stats.percent >= 100 ? 'mint' : 'brand'" />
          <p class="mt-1.5 text-[11px] text-fg-dim">
            {{ stats.done }}/{{ stats.total }} {{ doneLabel }} · {{ stats.percent }}%
          </p>
        </div>

        <nav class="mt-4 space-y-3">
          <div v-for="section in doc.sections" :key="section.id">
            <p class="mb-1 flex items-center justify-between text-[11px] font-medium uppercase tracking-wider text-fg-dim">
              <span class="truncate">{{ section.title }}</span>
              <span class="ml-2 shrink-0 tabular-nums">
                {{ sectionProgress.get(section.id)?.done }}/{{ sectionProgress.get(section.id)?.total }}
              </span>
            </p>
            <ul class="space-y-0.5 border-l border-line pl-2">
              <li v-for="item in section.items" :key="item.id">
                <button
                  class="flex w-full items-start gap-1.5 rounded px-1.5 py-1 text-left text-[12px] leading-snug transition-colors"
                  :class="
                    (readMode === 'focus' ? currentRow?.item.id : focusedId) === item.id
                      ? 'bg-accent-soft text-accent'
                      : 'text-fg-dim hover:bg-panel-soft hover:text-fg-muted'
                  "
                  @click="jumpTo(item.id)"
                >
                  <span
                    class="mt-0.5 grid h-3 w-3 shrink-0 place-items-center rounded-full text-[8px]"
                    :class="
                      study.statusOf(doc.slug, item.id) === 2
                        ? 'bg-mint-500/25 text-mint-400'
                        : study.statusOf(doc.slug, item.id) === 1
                          ? 'bg-amber-glow/25 text-amber-glow'
                          : 'bg-line-strong/40 text-transparent'
                    "
                  >
                    ✓
                  </span>
                  <span class="line-clamp-2">{{ item.title }}</span>
                </button>
              </li>
            </ul>
          </div>
        </nav>
      </div>
    </aside>

    <!-- 中：正文 -->
    <main class="min-w-0 flex-1">
      <!-- 窄屏工具条 -->
      <div class="mb-3 flex items-center gap-2 xl:hidden">
        <RouterLink to="/" class="btn btn-ghost px-2 py-1 text-xs">← 看板</RouterLink>
        <button class="btn btn-outline px-2 py-1 text-xs" @click="tocOpen = !tocOpen">
          目录 {{ stats.done }}/{{ stats.total }}
        </button>
        <button
          class="btn btn-outline px-2 py-1 text-xs"
          :class="readMode === 'focus' ? 'border-accent text-accent' : ''"
          @click="readMode = readMode === 'focus' ? 'list' : 'focus'"
        >
          {{ readMode === 'focus' ? '退出学习模式' : '学习模式' }}
        </button>
        <button
          class="btn btn-outline ml-auto px-2 py-1 text-xs xl:hidden"
          @click="mobileNotesOpen = true"
        >
          ✎ 笔记{{ activeNoteCount ? ` ${activeNoteCount}` : '' }}
        </button>
      </div>

      <div v-if="tocOpen" class="card mb-4 max-h-80 overflow-y-auto p-3 xl:hidden">
        <div v-for="section in doc?.sections ?? []" :key="section.id" class="mb-2">
          <p class="text-[11px] font-semibold uppercase tracking-wider text-fg-dim">{{ section.title }}</p>
          <button
            v-for="item in section.items"
            :key="item.id"
            class="block w-full truncate rounded px-1.5 py-1 text-left text-[12px] text-fg-muted hover:bg-panel-soft"
            @click="jumpTo(item.id)"
          >
            {{ study.statusOf(doc!.slug, item.id) === 2 ? '✓ ' : '' }}{{ item.title }}
          </button>
        </div>
      </div>

      <div v-if="loading" class="py-20 text-center text-sm text-fg-dim">加载中…</div>
      <div v-else-if="loadError" class="card p-6 text-center">
        <p class="text-sm text-red-500">{{ loadError }}</p>
        <RouterLink to="/" class="btn btn-outline mt-3">返回看板</RouterLink>
      </div>

      <template v-else-if="doc">
        <!-- ===================== 学习模式 ===================== -->
        <template v-if="readMode === 'focus'">
          <div class="card mb-4 flex flex-wrap items-center gap-2 p-3">
            <span class="chip bg-accent-soft text-accent">学习模式</span>
            <span class="text-xs text-fg-dim tabular-nums">
              第 {{ cursor + 1 }} / {{ allRows.length }} {{ unit }} · 剩 {{ remaining }} {{ todoLabel }}
            </span>
            <ProgressBar
              class="hidden max-w-40 flex-1 sm:block"
              :model-value="stats.done"
              :total="stats.total"
              :tone="stats.percent >= 100 ? 'mint' : 'brand'"
            />
            <div class="ml-auto flex items-center gap-1">
              <button class="btn btn-ghost hidden px-2 py-1 text-[11px] sm:inline-flex" @click="readMode = 'list'">
                退出学习模式
              </button>
            </div>
          </div>

          <div v-if="currentRow" class="card animate-rise overflow-hidden">
            <div class="flex items-start gap-2 border-b border-line px-4 py-3">
              <span class="mt-0.5 shrink-0 text-[11px] font-medium tabular-nums text-fg-dim">
                {{ String(cursor + 1).padStart(2, '0') }}
              </span>
              <div class="min-w-0 flex-1">
                <h2 class="text-lg font-semibold leading-snug text-fg">{{ currentRow.item.title }}</h2>
                <p class="mt-0.5 text-[10px] text-fg-dim">
                  {{ currentRow.sectionTitle }} · {{ currentRow.item.charCount }} 字 · 约
                  {{ Math.max(1, Math.round((currentRow.item.charCount / 400) * 2.2)) }} 分钟
                </p>
              </div>
              <div class="flex shrink-0 items-center gap-1">
                <span
                  v-if="study.reviewOf(doc.slug, currentRow.item.id)"
                  class="chip hidden bg-accent-soft text-[10px] text-accent sm:inline-flex"
                  :title="`下次复习：${study.reviewOf(doc.slug, currentRow.item.id)!.dueDay}`"
                >
                  ↻ {{ reviewHint(study.reviewOf(doc.slug, currentRow.item.id)!) }}
                </span>
                <StatusBadge :status="study.statusOf(doc.slug, currentRow.item.id)" />
                <button
                  class="grid h-7 w-7 place-items-center rounded-md text-sm transition-colors hover:bg-panel-soft"
                  :class="study.starredOf(doc.slug, currentRow.item.id) ? 'text-amber-glow' : 'text-fg-dim'"
                  @click="toggleStar(currentRow.item)"
                >
                  {{ study.starredOf(doc.slug, currentRow.item.id) ? '★' : '☆' }}
                </button>
              </div>
            </div>

            <div class="doc-body px-4 py-4" v-html="currentRow.item.html" @click="onContentClick" />
            <p v-if="!currentRow.item.html" class="px-4 py-6 text-center text-xs text-fg-dim">这道题没有正文</p>
            <!-- 给底部悬浮操作条留出空间 -->
            <div class="h-16" />
          </div>

          <div v-else class="card p-10 text-center">
            <p class="text-sm text-fg-dim">这篇文档没有内容</p>
            <RouterLink to="/" class="btn btn-outline mt-3">返回看板</RouterLink>
          </div>

          <!-- 悬浮操作条：正文再长也不用滚到底 -->
          <div
            v-if="currentRow"
            class="fixed inset-x-0 bottom-0 z-30 px-3 sm:bottom-4"
            style="padding-bottom: env(safe-area-inset-bottom)"
          >
            <div
              class="card mx-auto flex max-w-3xl items-center gap-2 p-2 shadow-xl shadow-black/10 dark:shadow-black/50"
            >
              <button class="btn btn-outline shrink-0 px-2.5 py-2 text-xs" :title="`上一${unit}`" @click="goPrev">
                ←
              </button>
              <button class="btn btn-primary flex-1 py-2.5 text-sm" @click="markDoneAndNext(currentRow.item)">
                ✓ {{ doneLabel }}，下一{{ unit }}
              </button>
              <button class="btn btn-outline shrink-0 px-3 py-2.5 text-sm" @click="goNext">
                下一{{ unit }} →
              </button>
              <button
                class="btn btn-ghost hidden shrink-0 px-2.5 py-2 text-xs sm:inline-flex"
                :class="study.statusOf(doc.slug, currentRow.item.id) === 1 ? 'text-amber-glow' : ''"
                title="标记为学习中并前进"
                @click="markLearningAndNext(currentRow.item)"
              >
                ◐ 学习中
              </button>
              <button
                class="btn btn-ghost shrink-0 px-2.5 py-2 text-xs sm:hidden"
                title="标记为学习中并前进"
                :class="study.statusOf(doc.slug, currentRow.item.id) === 1 ? 'text-amber-glow' : ''"
                @click="markLearningAndNext(currentRow.item)"
              >
                ◐
              </button>
            </div>
          </div>
        </template>

        <!-- ===================== 列表模式 ===================== -->
        <template v-else>
          <header class="card animate-rise mb-5 p-5">
            <div class="flex flex-wrap items-center gap-2 text-[11px] text-fg-dim">
              <span class="chip bg-accent-soft text-accent">{{ doc.group }}</span>
              <span v-if="isAlgo">{{ doc.questionCount }} 篇 · 按顺序学</span>
              <span v-else-if="doc.sectionCount > 1">
                {{ doc.sectionCount }} 章节 · {{ doc.questionCount }} 题
              </span>
              <span v-else>{{ doc.questionCount }} 题</span>
              <span v-if="doc.sourceUpdated">· 原文更新 {{ doc.sourceUpdated }}</span>
            </div>
            <h1 class="mt-2 text-2xl font-semibold text-fg">{{ doc.title }}</h1>

            <div
              v-if="doc.intro"
              class="relative mt-4 overflow-hidden transition-[max-height] duration-300"
              :class="introOpen ? '' : 'max-h-40'"
            >
              <div class="doc-body" v-html="doc.intro" />
              <div
                v-if="!introOpen"
                class="pointer-events-none absolute inset-x-0 bottom-0 h-16 bg-gradient-to-t from-panel to-transparent"
              />
            </div>
            <button
              v-if="doc.intro"
              class="mt-2 text-[11px] text-accent hover:opacity-80"
              @click="introOpen = !introOpen"
            >
              {{ introOpen ? '收起导读 ↑' : '展开导读 ↓' }}
            </button>

            <div class="mt-4 flex flex-wrap items-center gap-2 border-t border-line pt-3">
              <div class="flex items-center gap-0.5 rounded-lg bg-panel-soft p-0.5">
                <button
                  v-for="f in [
                    { k: 'all', t: '全部' },
                    { k: 'todo', t: todoLabel },
                    { k: 'starred', t: '已加星' },
                  ]"
                  :key="f.k"
                  class="rounded-md px-2.5 py-1 text-[11px] transition-colors"
                  :class="filter === f.k ? 'bg-accent font-medium text-accent-fg' : 'text-fg-dim hover:text-fg'"
                  @click="filter = f.k as 'all' | 'todo' | 'starred'"
                >
                  {{ f.t }}
                </button>
              </div>
              <span class="text-[11px] text-fg-dim">{{ visibleRows.length }} {{ unit }}显示中</span>
              <button
                class="btn btn-outline px-2.5 py-1 text-[11px]"
                @click="readMode = 'focus'"
              >
                进入学习模式 →
              </button>
              <a
                :href="doc.sourceUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="btn btn-ghost ml-auto px-2 py-1 text-[11px]"
              >
                查看原文 ↗
              </a>
            </div>
          </header>

          <div class="space-y-3">
            <article
              v-for="(row, i) in visibleRows"
              :id="`item-${row.item.id}`"
              :key="row.item.id"
              :data-item-id="row.item.id"
              class="card scroll-mt-20 overflow-hidden transition-colors"
              :class="[
                focusedId === row.item.id ? 'border-accent/40' : '',
                flashIds[row.item.id] ? 'ring-2 ring-mint-500/50' : '',
              ]"
              @click="focusedId = row.item.id"
            >
              <div class="flex items-start gap-2 border-b border-line px-4 py-3">
                <span class="mt-0.5 shrink-0 text-[11px] font-medium tabular-nums text-fg-dim">
                  {{ String(i + 1).padStart(2, '0') }}
                </span>
                <div class="min-w-0 flex-1">
                  <h3 class="text-[15px] font-semibold leading-snug text-fg">{{ row.item.title }}</h3>
                  <p class="mt-0.5 text-[10px] text-fg-dim">
                    <template v-if="isAlgo">
                      {{ row.item.charCount }} 字 · 约
                      {{ Math.max(1, Math.round((row.item.charCount / 400) * 2.2)) }} 分钟
                    </template>
                    <template v-else>
                      {{ row.sectionTitle }} · {{ row.item.charCount }} 字 · 约
                      {{ Math.max(1, Math.round((row.item.charCount / 400) * 2.2)) }} 分钟
                    </template>
                  </p>
                </div>
                <div class="flex shrink-0 items-center gap-1">
                  <StatusBadge :status="study.statusOf(doc.slug, row.item.id)" compact />
                  <button
                    class="grid h-7 w-7 place-items-center rounded-md text-sm transition-colors hover:bg-panel-soft"
                    :class="study.starredOf(doc.slug, row.item.id) ? 'text-amber-glow' : 'text-fg-dim'"
                    :title="study.starredOf(doc.slug, row.item.id) ? '取消加星 (s)' : '加星 (s)'"
                    @click.stop="toggleStar(row.item)"
                  >
                    {{ study.starredOf(doc.slug, row.item.id) ? '★' : '☆' }}
                  </button>
                </div>
              </div>

              <div class="doc-body px-4 py-4" v-html="row.item.html" @click="onContentClick" />

              <div class="flex flex-wrap items-center gap-1.5 border-t border-line px-4 py-2.5">
                <button
                  class="btn px-2.5 py-1 text-[11px]"
                  :class="
                    study.statusOf(doc.slug, row.item.id) === 1
                      ? 'bg-amber-glow/20 text-amber-glow'
                      : 'btn-outline'
                  "
                  @click.stop="setStatus(row.item, 1)"
                >
                  ◐ 学习中
                </button>
                <button
                  class="btn px-2.5 py-1 text-[11px]"
                  :class="
                    study.statusOf(doc.slug, row.item.id) === 2
                      ? 'bg-mint-500/20 text-mint-400'
                      : 'btn-outline'
                  "
                  @click.stop="setStatus(row.item, 2)"
                >
                  ✓ {{ doneLabel }}
                </button>
                <button
                  class="btn btn-outline px-2.5 py-1 text-[11px]"
                  :class="openNotes[row.item.id] ? 'border-accent/50 text-accent' : ''"
                  @click.stop="openNotes[row.item.id] = !openNotes[row.item.id]"
                >
                  ✎ 笔记
                  <span
                    v-if="study.notesOf(doc.slug, row.item.id).length"
                    class="chip bg-accent-soft text-accent"
                  >
                    {{ study.notesOf(doc.slug, row.item.id).length }}
                  </span>
                </button>
              </div>

              <div v-if="openNotes[row.item.id]" class="border-t border-line px-4 py-3" @click.stop>
                <NotePanel
                  :doc-id="doc.slug"
                  :item-id="row.item.id"
                  :notes="study.notesOf(doc.slug, row.item.id)"
                  @add="(text) => addNote(row.item, text)"
                  @edit="editNote"
                  @remove="removeNote"
                />
              </div>
            </article>
          </div>

          <p v-if="isEmpty" class="py-16 text-center text-sm text-fg-dim">
            {{
              filter === 'starred'
                ? `还没有加星的${unit}`
                : filter === 'todo'
                  ? `这一${unit}已经全部${doneLabel} 🎉`
                  : '没有内容'
            }}
          </p>

          <div class="mt-8 flex flex-wrap items-center justify-between gap-3 border-t border-line pt-5">
            <RouterLink to="/" class="btn btn-outline text-xs">← 返回看板</RouterLink>
            <p class="text-right text-[11px] text-fg-dim">
              j/k 上下{{ unit }} · 1 学习中 · 2 {{ doneLabel }} · 3 清除 · s 加星 · n 笔记
            </p>
          </div>

          <!-- 专题内的顺序导航：算法教程按篇推进时用得上 -->
          <div v-if="isAlgo" class="mt-4 grid gap-3 sm:grid-cols-2">
            <button
              class="card flex items-center gap-2 p-3 text-left transition-colors hover:border-line-strong disabled:opacity-40"
              :disabled="!prevRow"
              @click="prevRow && jumpTo(prevRow.item.id)"
            >
              <span class="text-fg-dim">←</span>
              <span class="min-w-0">
                <span class="block text-[10px] uppercase tracking-wider text-fg-dim">上一篇</span>
                <span class="block truncate text-xs text-fg-muted">{{ prevRow?.item.title ?? '已是第一篇' }}</span>
              </span>
            </button>
            <button
              class="card flex items-center justify-end gap-2 p-3 text-right transition-colors hover:border-line-strong disabled:opacity-40"
              :disabled="!nextRow"
              @click="nextRow && jumpTo(nextRow.item.id)"
            >
              <span class="min-w-0">
                <span class="block text-[10px] uppercase tracking-wider text-fg-dim">下一篇</span>
                <span class="block truncate text-xs text-fg-muted">{{ nextRow?.item.title ?? '已是最后一篇' }}</span>
              </span>
              <span class="text-fg-dim">→</span>
            </button>
          </div>        </template>
      </template>
    </main>

    <!-- 右：笔记栏（桌面常驻） -->
    <aside
      v-if="doc"
      class="sticky top-20 hidden h-[calc(100dvh-6rem)] w-80 shrink-0 flex-col xl:flex"
      :class="notesCollapsed ? 'w-12' : ''"
    >
      <div class="card flex min-h-0 flex-1 flex-col">
        <div class="flex items-center gap-2 border-b border-line px-3 py-2.5">
          <template v-if="!notesCollapsed">
            <h2 class="text-xs font-semibold uppercase tracking-wider text-fg-dim">笔记</h2>
            <span v-if="activeNoteCount" class="chip bg-accent-soft text-accent">{{ activeNoteCount }}</span>
            <button
              class="ml-auto rounded p-1 text-fg-dim transition-colors hover:bg-panel-soft hover:text-fg"
              title="收起笔记栏"
              @click="notesCollapsed = true"
            >
              ⇥
            </button>
          </template>
          <button
            v-else
            class="mx-auto rounded p-1 text-fg-dim transition-colors hover:bg-panel-soft hover:text-fg"
            title="展开笔记栏"
            @click="notesCollapsed = false"
          >
            ⇤
          </button>
        </div>

        <div v-if="!notesCollapsed" class="min-h-0 flex-1 overflow-y-auto p-3">
          <template v-if="activeItem">
            <p class="mb-2 line-clamp-2 text-[11px] leading-snug text-fg-dim">
              正在看：<span class="text-fg-muted">{{ activeItem.title }}</span>
            </p>
            <NotePanel
              :key="`${doc.slug}-${activeItem.id}`"
              :doc-id="doc.slug"
              :item-id="activeItem.id"
              :notes="activeNotes"
              @add="(text) => addNote(activeItem!, text)"
              @edit="editNote"
              @remove="removeNote"
            />
            <div class="mt-3 rounded-xl bg-panel-soft/60 p-3">
              <p class="text-[11px] leading-relaxed text-fg-dim">
                笔记会立刻同步到服务端，换设备登录也能看到。
              </p>
            </div>
          </template>
          <p v-else class="py-10 text-center text-xs text-fg-dim">滚动到任意内容即可记笔记</p>
        </div>
      </div>
    </aside>
  </div>

  <!-- 移动端笔记抽屉 -->
  <div v-if="mobileNotesOpen && doc" class="fixed inset-0 z-50 flex xl:hidden">
    <div class="flex-1 bg-black/50" @click="mobileNotesOpen = false" />
    <div
      class="flex h-full w-[88vw] max-w-sm flex-col border-l border-line bg-app"
      style="padding-bottom: env(safe-area-inset-bottom)"
    >
      <div class="flex items-center gap-2 border-b border-line px-4 py-3">
        <h2 class="text-sm font-semibold text-fg">笔记</h2>
        <button class="btn btn-ghost ml-auto px-2 py-1 text-xs" @click="mobileNotesOpen = false">关闭</button>
      </div>
      <div class="min-h-0 flex-1 overflow-y-auto p-3">
        <template v-if="activeItem">
          <p class="mb-2 text-[11px] leading-snug text-fg-dim">
            正在看：<span class="text-fg-muted">{{ activeItem.title }}</span>
          </p>
          <NotePanel
            :key="`m-${doc.slug}-${activeItem.id}`"
            :doc-id="doc.slug"
            :item-id="activeItem.id"
            :notes="activeNotes"
            @add="(text) => addNote(activeItem!, text)"
            @edit="editNote"
            @remove="removeNote"
          />
        </template>
        <p v-else class="py-10 text-center text-xs text-fg-dim">还没有选中内容</p>
      </div>
    </div>
  </div>

  <!-- 图片放大 -->
  <div
    v-if="lightbox"
    class="fixed inset-0 z-[60] grid place-items-center bg-black/85 p-4 backdrop-blur"
    @click="lightbox = ''"
  >
    <img :src="lightbox" alt="" class="max-h-full max-w-full rounded-lg" />
    <button class="absolute right-5 top-5 text-2xl text-white/70 hover:text-white" aria-label="关闭">×</button>
  </div>

  <!-- 提示 -->
  <transition
    enter-active-class="transition duration-200"
    enter-from-class="opacity-0 translate-y-2"
    leave-active-class="transition duration-150"
    leave-to-class="opacity-0"
  >
    <div
      v-if="toast"
      class="fixed bottom-20 left-1/2 z-[70] -translate-x-1/2 rounded-full bg-fg px-4 py-1.5 text-xs font-medium text-app shadow-lg sm:bottom-6"
    >
      {{ toast }}
    </div>
  </transition>
</template>
