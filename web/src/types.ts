/** 后端返回的数据结构。 */

export interface DocMeta {
  slug: string
  title: string
  group: string
  order: number
  sourceUrl: string
  sourceUpdated: string
  sectionCount: number
  questionCount: number
  charCount: number
  /** interview = 小林coding 八股题；algo = 代码随想录算法教程 */
  library: Library
}

export type Library = 'interview' | 'algo'

export interface Manifest {
  generatedAt: string
  docs: DocMeta[]
}

export interface Item {
  id: string
  title: string
  html: string
  text: string
  charCount: number
}

export interface Section {
  id: string
  title: string
  intro: string
  items: Item[]
}

export interface Doc extends DocMeta {
  intro: string
  sections: Section[]
}

/** 0 未开始 / 1 学习中 / 2 已掌握 */
export type Status = 0 | 1 | 2

export interface Progress {
  docId: string
  itemId: string
  status: Status
  starred: boolean
  updatedAt: string
  doneDay?: string
}

export interface Note {
  id: number
  docId: string
  itemId: string
  content: string
  createdAt: string
  updatedAt: string
  createdDay: string
}

export interface Snapshot {
  progress: Progress[]
  notes: Note[]
  revision: string
}
