<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import AppHeader from '@/components/AppHeader.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { useContentStore, useStudyStore } from '@/stores'
import { renderMarkdown } from '@/utils/markdown'

const content = useContentStore()
const study = useStudyStore()
const keyword = ref('')
const tab = ref<'notes' | 'starred'>('notes')

onMounted(async () => {
  await content.loadManifest()
  if (!study.loaded) await study.load()
  await loadReferencedDocs()
})

/** 笔记 / 加星条目所属的文档需要加载一次，才能显示题目标题。 */
async function loadReferencedDocs() {
  const slugs = new Set<string>()
  for (const g of study.noteGroups) slugs.add(g.docId)
  for (const p of Object.values(study.progress)) {
    if (p.starred) slugs.add(p.docId)
  }
  await Promise.allSettled([...slugs].map((s) => content.loadDoc(s)))
}

interface Row {
  docId: string
  docTitle: string
  itemId: string
  itemTitle: string
  status: number
  starred: boolean
  body: string
  updatedAt: string
  count: number
}

function titleOf(docId: string, itemId: string): string {
  const cached = content.docs[docId]
  if (cached) {
    for (const section of cached.sections) {
      const found = section.items.find((it) => it.id === itemId)
      if (found) return found.title
    }
  }
  return itemId
}

const rows = computed<Row[]>(() => {
  const out: Row[] = []
  for (const group of study.noteGroups) {
    const meta = content.metaOf(group.docId)
    out.push({
      docId: group.docId,
      docTitle: meta?.title ?? group.docId,
      itemId: group.itemId,
      itemTitle: titleOf(group.docId, group.itemId),
      status: study.statusOf(group.docId, group.itemId),
      starred: study.starredOf(group.docId, group.itemId),
      body: group.notes.map((n) => n.content).join('\n\n---\n\n'),
      updatedAt: group.notes[group.notes.length - 1]?.updatedAt ?? '',
      count: group.notes.length,
    })
  }
  const kw = keyword.value.trim().toLowerCase()
  const filtered = kw
    ? out.filter(
        (r) =>
          r.body.toLowerCase().includes(kw) ||
          r.itemTitle.toLowerCase().includes(kw) ||
          r.docTitle.toLowerCase().includes(kw),
      )
    : out
  return filtered.sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
})

const starredRows = computed(() => {
  const out: { docId: string; docTitle: string; itemId: string; itemTitle: string }[] = []
  for (const p of Object.values(study.progress)) {
    if (!p.starred) continue
    out.push({
      docId: p.docId,
      docTitle: content.metaOf(p.docId)?.title ?? p.docId,
      itemId: p.itemId,
      itemTitle: titleOf(p.docId, p.itemId),
    })
  }
  return out
})
</script>

<template>
  <AppHeader />

  <main class="mx-auto max-w-4xl px-4 pb-24 pt-6 sm:px-6">
    <div class="mb-5 flex flex-wrap items-center gap-3">
      <h1 class="text-xl font-semibold text-fg">我的笔记</h1>
      <div class="flex items-center gap-0.5 rounded-lg bg-panel-soft p-0.5">
        <button
          v-for="t in [
            { k: 'notes', label: `笔记 ${study.noteGroups.length}` },
            { k: 'starred', label: `加星 ${starredRows.length}` },
          ]"
          :key="t.k"
          class="rounded-md px-2.5 py-1 text-[11px] transition-colors"
          :class="tab === t.k ? 'bg-accent font-medium text-accent-fg' : 'text-fg-dim hover:text-fg'"
          @click="tab = t.k as 'notes' | 'starred'"
        >
          {{ t.label }}
        </button>
      </div>
      <input
        v-if="tab === 'notes'"
        v-model="keyword"
        type="search"
        placeholder="搜索笔记…"
        class="field ml-auto w-full py-1.5 text-xs sm:w-56"
      />
    </div>

    <template v-if="tab === 'notes'">
      <div v-if="rows.length" class="space-y-3">
        <article v-for="row in rows" :key="`${row.docId}-${row.itemId}`" class="card group p-4">
          <div class="mb-2 flex flex-wrap items-center gap-2">
            <span class="chip bg-panel-soft text-fg-dim">{{ row.docTitle }}</span>
            <StatusBadge :status="row.status" compact />
            <span v-if="row.starred" class="text-xs text-amber-glow">★</span>
            <span class="ml-auto text-[10px] text-fg-dim">{{ row.count }} 条</span>
          </div>
          <RouterLink
            :to="{ name: 'reader', params: { slug: row.docId }, hash: `#item-${row.itemId}` }"
            class="text-[15px] font-medium text-fg hover:text-accent"
          >
            {{ row.itemTitle }}
          </RouterLink>
          <div class="note-md mt-2 text-[13px] leading-relaxed text-fg-muted" v-html="renderMarkdown(row.body)" />
        </article>
      </div>
      <div v-else class="card p-10 text-center">
        <p class="text-sm text-fg-dim">
          {{ keyword ? '没找到匹配的笔记' : '还没有笔记，去阅读页右侧的笔记栏开始记录吧' }}
        </p>
        <RouterLink to="/" class="btn btn-outline mt-3 text-xs">去看板</RouterLink>
      </div>
    </template>

    <template v-else>
      <div v-if="starredRows.length" class="grid gap-2 sm:grid-cols-2">
        <RouterLink
          v-for="row in starredRows"
          :key="`${row.docId}-${row.itemId}`"
          :to="{ name: 'reader', params: { slug: row.docId }, hash: `#item-${row.itemId}` }"
          class="card flex items-center gap-2 p-3 hover:border-line-strong"
        >
          <span class="text-amber-glow">★</span>
          <div class="min-w-0">
            <p class="truncate text-sm text-fg">{{ row.itemTitle }}</p>
            <p class="truncate text-[11px] text-fg-dim">{{ row.docTitle }}</p>
          </div>
        </RouterLink>
      </div>
      <div v-else class="card p-10 text-center text-sm text-fg-dim">
        还没有加星的题目，阅读时点 ☆ 收藏重点。
      </div>
    </template>
  </main>
</template>
