<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { renderMarkdown } from '@/utils/markdown'
import type { Note } from '@/types'

const props = defineProps<{
  docId: string
  itemId: string
  notes: Note[]
  /** 紧凑模式：右侧常驻栏里用，编辑框更矮。 */
  compact?: boolean
}>()

const emit = defineEmits<{
  add: [content: string]
  edit: [id: number, content: string]
  remove: [id: number]
}>()

const draft = ref('')
const editingId = ref<number | null>(null)
const editingText = ref('')
const preview = ref(false)
const draftEl = ref<HTMLTextAreaElement | null>(null)
const editEl = ref<HTMLTextAreaElement | HTMLTextAreaElement[] | null>(null)

function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const now = new Date()
  const hh = `${d.getHours()}`.padStart(2, '0')
  const mm = `${d.getMinutes()}`.padStart(2, '0')
  if (d.toDateString() === now.toDateString()) return `今天 ${hh}:${mm}`
  if (d.getFullYear() === now.getFullYear()) return `${d.getMonth() + 1}/${d.getDate()} ${hh}:${mm}`
  return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()}`
}

function submit() {
  const content = draft.value.trim()
  if (!content) return
  emit('add', content)
  draft.value = ''
  preview.value = false
}

async function startEdit(note: Note) {
  editingId.value = note.id
  editingText.value = note.content
  await nextTick()
  const el = editEl.value
  const target = Array.isArray(el) ? el[0] : el
  target?.focus()
  target?.setSelectionRange(target.value.length, target.value.length)
}

function saveEdit() {
  if (editingId.value == null) return
  const content = editingText.value.trim()
  if (!content) return
  emit('edit', editingId.value, content)
  editingId.value = null
  editingText.value = ''
}

function cancelEdit() {
  editingId.value = null
  editingText.value = ''
}

function onDraftKeydown(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
    e.preventDefault()
    submit()
  }
}

watch(
  () => props.itemId,
  () => {
    draft.value = ''
    cancelEdit()
    preview.value = false
  },
)

defineExpose({ focus: () => draftEl.value?.focus() })
</script>

<template>
  <div :class="compact ? '' : 'rounded-xl border border-line bg-panel-soft/50 p-3.5'">
    <div v-if="!compact" class="mb-2 flex items-center gap-2">
      <h4 class="text-xs font-semibold uppercase tracking-wider text-fg-dim">我的笔记</h4>
      <span v-if="notes.length" class="chip bg-panel-soft text-fg-muted">{{ notes.length }}</span>
      <button
        v-if="draft.trim()"
        class="btn btn-ghost ml-auto px-2 py-0.5 text-[11px]"
        @click="preview = !preview"
      >
        {{ preview ? '编辑' : '预览' }}
      </button>
    </div>

    <ul v-if="notes.length" class="space-y-2" :class="compact ? '' : 'mb-3'">
      <li
        v-for="note in notes"
        :key="note.id"
        class="group rounded-lg bg-panel px-2.5 py-2 ring-1 ring-line"
      >
        <template v-if="editingId === note.id">
          <textarea
            ref="editEl"
            v-model="editingText"
            rows="4"
            class="field resize-y py-2 text-[13px] leading-relaxed"
          />
          <div class="mt-2 flex gap-2">
            <button class="btn btn-primary px-2.5 py-1 text-[11px]" @click="saveEdit">保存</button>
            <button class="btn btn-ghost px-2.5 py-1 text-[11px]" @click="cancelEdit">取消</button>
          </div>
        </template>
        <template v-else>
          <div class="note-md text-[13px] leading-relaxed text-fg-muted" v-html="renderMarkdown(note.content)" />
          <div class="mt-1.5 flex items-center gap-2 text-[10px] text-fg-dim">
            <span>{{ formatTime(note.createdAt) }}</span>
            <button
              class="ml-auto opacity-0 transition-opacity hover:text-accent group-hover:opacity-100 focus:opacity-100"
              @click="startEdit(note)"
            >
              编辑
            </button>
            <button
              class="opacity-0 transition-opacity hover:text-red-500 group-hover:opacity-100 focus:opacity-100"
              @click="emit('remove', note.id)"
            >
              删除
            </button>
          </div>
        </template>
      </li>
    </ul>

    <div v-if="preview && draft.trim()">
      <div class="note-md min-h-16 rounded-md bg-panel p-2.5 text-[13px] ring-1 ring-line" v-html="renderMarkdown(draft)" />
      <div class="mt-2 flex gap-2">
        <button class="btn btn-primary px-2.5 py-1 text-[11px]" @click="submit">保存</button>
        <button class="btn btn-ghost px-2.5 py-1 text-[11px]" @click="preview = false">继续写</button>
      </div>
    </div>
    <div v-else>
      <textarea
        ref="draftEl"
        v-model="draft"
        :rows="compact ? 3 : 2"
        placeholder="记点什么…（**粗体**、`代码`、- 列表，Ctrl+Enter 保存）"
        class="field resize-y py-2 text-[13px] leading-relaxed"
        @keydown="onDraftKeydown"
      />
      <div class="mt-1.5 flex items-center gap-2">
        <button class="btn btn-primary px-2.5 py-1 text-[11px]" :disabled="!draft.trim()" @click="submit">
          保存笔记
        </button>
        <button
          v-if="draft.trim()"
          class="btn btn-ghost px-2 py-1 text-[11px]"
          @click="preview = true"
        >
          预览
        </button>
        <span class="ml-auto text-[10px] text-fg-dim">Ctrl/⌘ + Enter</span>
      </div>
    </div>
  </div>
</template>
