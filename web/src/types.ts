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
  /** 只是一份指向站外文章的目录，不计入学习任务的理论进度 */
  readingList?: boolean
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

/** 学习任务：选中的模块 + 截止日期。 */
export interface Goal {
  docSlugs: string[]
  startDay: string
  dueDay: string
  /** 0 表示按剩余天数自动均摊 */
  dailyTarget: number
  createdAt: string
  updatedAt: string
}

export interface Snapshot {
  progress: Progress[]
  notes: Note[]
  goal: Goal | null
  revision: string
}
