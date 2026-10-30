import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { Doc, DocMeta, Goal, Library, Manifest, Note, Progress, Snapshot, Status } from '@/types'

/** 内容库（manifest + 已加载文档缓存），只读。 */
export const useContentStore = defineStore('content', () => {
  const manifest = ref<Manifest | null>(null)
  const docs = ref<Record<string, Doc>>({})
  const loading = ref(false)
  const error = ref('')

  const docMetas = computed<DocMeta[]>(() =>
    [...(manifest.value?.docs ?? [])].sort((a, b) => a.order - b.order),
  )

  /** 按内容库筛选，默认八股题。 */
  const byLibrary = (library: Library) => docMetas.value.filter((d) => d.library === library)

  const interviewDocs = computed(() => byLibrary('interview'))
  const algoDocs = computed(() => byLibrary('algo'))

  const totalQuestions = computed(() =>
    docMetas.value.reduce((sum, d) => sum + d.questionCount, 0),
  )

  const totalChars = computed(() => docMetas.value.reduce((sum, d) => sum + d.charCount, 0))

  const groups = computed(() => groupBy(docMetas.value))
  const interviewGroups = computed(() => groupBy(interviewDocs.value))
  const algoGroups = computed(() => groupBy(algoDocs.value))

  function groupBy(list: DocMeta[]) {
    const map = new Map<string, DocMeta[]>()
    for (const doc of list) {
      const items = map.get(doc.group) ?? []
      items.push(doc)
      map.set(doc.group, items)
    }
    return [...map.entries()].map(([title, items]) => ({ title, items }))
  }

  async function loadManifest() {
    if (manifest.value) return manifest.value
    loading.value = true
    error.value = ''
    try {
      manifest.value = await api.getFresh<Manifest>('/api/content/manifest')
      return manifest.value
    } catch (e) {
      error.value = e instanceof Error ? e.message : '内容加载失败'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function loadDoc(slug: string): Promise<Doc> {
    const cached = docs.value[slug]
    if (cached) return cached
    const doc = await api.getFresh<Doc>(`/api/content/docs/${slug}`)
    docs.value[slug] = doc
    return doc
  }

  function metaOf(slug: string): DocMeta | undefined {
    return docMetas.value.find((d) => d.slug === slug)
  }

  return {
    manifest,
    docs,
    loading,
    error,
    docMetas,
    interviewDocs,
    algoDocs,
    groups,
    interviewGroups,
    algoGroups,
    totalQuestions,
    totalChars,
    loadManifest,
    loadDoc,
    metaOf,
  }
})

/** 学习状态：进度 + 笔记 + 学习任务 + 连续打卡。 */
export const useStudyStore = defineStore('study', () => {
  const progress = ref<Record<string, Progress>>({})
  const notes = ref<Note[]>([])
  const goal = ref<Goal | null>(null)
  const loaded = ref(false)
  const syncing = ref(false)
  const lastSyncAt = ref<string>('')
  const syncError = ref('')

  const key = (docId: string, itemId: string) => `${docId}::${itemId}`

  const statusOf = (docId: string, itemId: string): Status =>
    (progress.value[key(docId, itemId)]?.status ?? 0) as Status

  const starredOf = (docId: string, itemId: string): boolean =>
    progress.value[key(docId, itemId)]?.starred ?? false

  /** 某篇文档的统计。 */
  function docStats(docId: string, total: number) {
    let done = 0
    let learning = 0
    let starred = 0
    for (const p of Object.values(progress.value)) {
      if (p.docId !== docId) continue
      if (p.status === 2) done++
      else if (p.status === 1) learning++
      if (p.starred) starred++
    }
    return {
      done,
      learning,
      starred,
      total,
      percent: total > 0 ? Math.round((done / total) * 100) : 0,
    }
  }

  const allStats = computed(() => {
    let done = 0
    let learning = 0
    let starred = 0
    for (const p of Object.values(progress.value)) {
      if (p.status === 2) done++
      else if (p.status === 1) learning++
      if (p.starred) starred++
    }
    return { done, learning, starred }
  })

  const notesOf = (docId: string, itemId: string): Note[] =>
    notes.value.filter((n) => n.docId === docId && n.itemId === itemId)

  const noteCountByDoc = computed(() => {
    const map: Record<string, number> = {}
    for (const n of notes.value) map[n.docId] = (map[n.docId] ?? 0) + 1
    return map
  })

  /** 有笔记的条目，用于「我的笔记」列表。 */
  const noteGroups = computed(() => {
    const map = new Map<string, { docId: string; itemId: string; notes: Note[] }>()
    for (const n of notes.value) {
      const k = key(n.docId, n.itemId)
      const g = map.get(k) ?? { docId: n.docId, itemId: n.itemId, notes: [] }
      g.notes.push(n)
      map.set(k, g)
    }
    return [...map.values()]
  })

  /** 最近 26 周的学习热力图数据（按「已掌握」的发生日）。 */
  const heatmap = computed(() => {
    const counts = new Map<string, number>()
    for (const p of Object.values(progress.value)) {
      if (p.status !== 2 || !p.doneDay) continue
      counts.set(p.doneDay, (counts.get(p.doneDay) ?? 0) + 1)
    }
    for (const n of notes.value) {
      const d = n.createdDay
      if (!d) continue
      counts.set(d, (counts.get(d) ?? 0) + 1)
    }
    return counts
  })

  /** 连续学习天数（今天或昨天有动作都算连续）。 */
  const streak = computed(() => {
    const days = heatmap.value
    const fmt = (d: Date) => {
      const y = d.getFullYear()
      const m = `${d.getMonth() + 1}`.padStart(2, '0')
      const dd = `${d.getDate()}`.padStart(2, '0')
      return `${y}-${m}-${dd}`
    }
    const cursor = new Date()
    if (!days.has(fmt(cursor))) cursor.setDate(cursor.getDate() - 1)
    let n = 0
    while (days.has(fmt(cursor))) {
      n++
      cursor.setDate(cursor.getDate() - 1)
    }
    return n
  })

  const todayCount = computed(() => {
    const d = new Date()
    const t = `${d.getFullYear()}-${`${d.getMonth() + 1}`.padStart(2, '0')}-${`${d.getDate()}`.padStart(2, '0')}`
    return heatmap.value.get(t) ?? 0
  })

  async function load() {
    if (loaded.value) return
    syncing.value = true
    syncError.value = ''
    try {
      const snap = await api.get<Snapshot>('/api/snapshot')
      applySnapshot(snap)
      loaded.value = true
      lastSyncAt.value = new Date().toISOString()
    } catch (e) {
      syncError.value = e instanceof Error ? e.message : '同步失败'
      throw e
    } finally {
      syncing.value = false
    }
  }

  function applySnapshot(snap: Snapshot) {
    const map: Record<string, Progress> = {}
    for (const p of snap.progress) map[key(p.docId, p.itemId)] = p
    progress.value = map
    notes.value = snap.notes
    goal.value = snap.goal ?? null
  }

  async function refresh() {
    syncing.value = true
    syncError.value = ''
    try {
      const snap = await api.get<Snapshot>('/api/snapshot')
      applySnapshot(snap)
      lastSyncAt.value = new Date().toISOString()
    } catch (e) {
      syncError.value = e instanceof Error ? e.message : '同步失败'
    } finally {
      syncing.value = false
    }
  }

  /** 乐观更新 + 失败回滚。 */
  async function setStatus(docId: string, itemId: string, status: Status) {
    const k = key(docId, itemId)
    const prev = progress.value[k]
    const next: Progress = {
      docId,
      itemId,
      status,
      starred: prev?.starred ?? false,
      updatedAt: new Date().toISOString(),
      doneDay: status === 2 ? (prev?.doneDay ?? today()) : undefined,
    }
    progress.value = { ...progress.value, [k]: next }
    try {
      const saved = await api.put<Progress>('/api/progress', { docId, itemId, status })
      progress.value = { ...progress.value, [k]: saved }
    } catch (e) {
      if (prev) progress.value = { ...progress.value, [k]: prev }
      else {
        const copy = { ...progress.value }
        delete copy[k]
        progress.value = copy
      }
      throw e
    }
  }

  async function toggleStar(docId: string, itemId: string) {
    const k = key(docId, itemId)
    const prev = progress.value[k]
    const starred = !(prev?.starred ?? false)
    progress.value = {
      ...progress.value,
      [k]: {
        docId,
        itemId,
        status: prev?.status ?? 0,
        starred,
        updatedAt: new Date().toISOString(),
        doneDay: prev?.doneDay,
      },
    }
    try {
      const saved = await api.put<Progress>('/api/progress', { docId, itemId, starred })
      progress.value = { ...progress.value, [k]: saved }
    } catch (e) {
      if (prev) progress.value = { ...progress.value, [k]: prev }
      else {
        const copy = { ...progress.value }
        delete copy[k]
        progress.value = copy
      }
      throw e
    }
  }

  async function addNote(docId: string, itemId: string, content: string) {
    const note = await api.post<Note>('/api/notes', { docId, itemId, content })
    notes.value = [...notes.value, note]
    return note
  }

  async function editNote(id: number, content: string) {
    const note = await api.put<Note>(`/api/notes/${id}`, { content })
    notes.value = notes.value.map((n) => (n.id === id ? note : n))
    return note
  }

  async function removeNote(id: number) {
    await api.del(`/api/notes/${id}`)
    notes.value = notes.value.filter((n) => n.id !== id)
  }

  // ---------- 学习任务 ----------

  async function saveGoal(input: {
    docSlugs: string[]
    dueDay: string
    dailyTarget: number
  }) {
    const saved = await api.put<Goal>('/api/goal', input)
    goal.value = saved
    return saved
  }

  async function clearGoal() {
    await api.del('/api/goal')
    goal.value = null
  }

  return {
    progress,
    notes,
    goal,
    loaded,
    syncing,
    lastSyncAt,
    syncError,
    saveGoal,
    clearGoal,
    allStats,
    noteCountByDoc,
    noteGroups,
    heatmap,
    streak,
    todayCount,
    statusOf,
    starredOf,
    docStats,
    notesOf,
    load,
    refresh,
    setStatus,
    toggleStar,
    addNote,
    editNote,
    removeNote,
  }
})

export const useAuthStore = defineStore('auth', () => {
  const authed = ref(false)
  const checked = ref(false)
  const version = ref('')

  async function check() {
    try {
      const me = await api.get<{ authed: boolean; version: string }>('/api/me')
      authed.value = me.authed
      version.value = me.version
    } catch {
      authed.value = false
    } finally {
      checked.value = true
    }
    return authed.value
  }

  async function login(password: string) {
    await api.post<{ ok: boolean }>('/api/login', { password })
    authed.value = true
  }

  async function logout() {
    try {
      await api.post('/api/logout', {})
    } finally {
      authed.value = false
    }
  }

  return { authed, checked, version, check, login, logout }
})

function today(): string {
  const d = new Date()
  return `${d.getFullYear()}-${`${d.getMonth() + 1}`.padStart(2, '0')}-${`${d.getDate()}`.padStart(2, '0')}`
}
