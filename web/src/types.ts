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
  /** interview = 小林coding 八股题；algo = 代码随想录算法教程；ai = 小林面试笔记大模型题库
   */
  library: Library
  /** 只是一份指向站外文章的目录，不计入学习任务的理论进度 */
  readingList?: boolean
}

export type Library = 'interview' | 'algo' | 'ai'

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

/** 一个每日时间窗（上午只学算法 / 下午复习+八股）。 */
export interface PlanWindow {
  name: string
  /** 该窗可用分钟数 */
  minutes: number
  /** 该窗允许处理的库；空数组表示不限 */
  libs: string[]
  /** 只做复习，不学新内容 */
  reviewOnly: boolean
}

/** 学习任务：选中的模块 + 截止日期 + 每天的可用时间。 */
export interface Goal {
  docSlugs: string[]
  startDay: string
  dueDay: string
  /** 0 表示按剩余天数自动均摊（旧字段，新逻辑以 dailyMinutes 为准） */
  dailyTarget: number
  /** 每天可投入的分钟数 —— 计划推算的核心输入 */
  dailyMinutes: number
  /** 一次性停滞日 YYYY-MM-DD */
  restDays: string[]
  /** 每周固定休息：0=周日 … 6=周六 */
  weeklyRest: number[]
  /** 各内容库「学一条」的分钟数，算法与八股差异很大 */
  minutesPerItem: Record<string, number>
  /** 每日时间窗；为空时按「一天一个总时间」算 */
  windows?: PlanWindow[]
  createdAt: string
  updatedAt: string
}

/** 把历史「已掌握」纳入复习的结果。 */
export interface BackfillResult {
  added: number
  skipped: number
  total: number
  overdue: number
}

// ---------- 学习计划推算 ----------

export interface PlanLibInput {
  library: string
  remaining: number
  minutesPerItem: number
}

export interface PlanDay {
  day: string
  rest: boolean
  /** 各库当天新学条数 */
  new: Record<string, number>
  /** 各库当天复习条数 */
  review: Record<string, number>
  newMinutes: number
  reviewMinutes: number
  minutesUsed: number
  remaining: Record<string, number>
}

export interface StudyPlan {
  days: PlanDay[]
  feasible: boolean
  finishDay: string
  totalNew: number
  learned: number
  reviewMinutesTotal: number
  peakMinutes: number
  overloadedDays: number
  dailyMinutes: number
  studyDays: number
  restCount: number
}

export interface PlanAdvice {
  feasible: boolean
  requiredDailyMinutes: number
  earliestFinishDay: string
  suggestedDailyMinutes: number
  suggestedDueDay: string
  reviewShare: number
  verdict: 'ok' | 'raise-minutes' | 'extend-deadline' | 'both' | 'impossible'
}

export interface StudyPlanResponse {
  hasGoal: boolean
  goal?: Goal
  plan?: StudyPlan
  advice?: PlanAdvice
  libs?: Record<string, number>
  /** 各内容库已完成的条数（不是剩余） */
  libDone?: Record<string, number>
  /** 今日进度：复习做了多少、到期多少、上限多少 */
  today?: {
    reviewDone: number
    reviewDue: number
    reviewCap: number
  }
}

export interface Snapshot {
  progress: Progress[]
  notes: Note[]
  goal: Goal | null
  review: ReviewItem[]
  revision: string
}

// ---------- 复习计划 ----------

/** 复习评分。 */
export type ReviewGrade =
  | 0 // 忘了：退回第 1 阶段，明天重来
  | 1 // 模糊：原地踏步，间隔对折
  | 2 // 记住了：进入下一阶段
  | 3 // 今天没时间：顺延到明天

/** 一条内容的复习状态。 */
export interface ReviewItem {
  docId: string
  itemId: string
  /** 当前阶段，0 起步；等于 stages 时毕业 */
  stage: number
  /** 「忘了」的累计次数 */
  lapses: number
  /** 实际复习过几轮 */
  reviewCount: number
  lastDay: string
  /** 下次复习日 YYYY-MM-DD */
  dueDay: string
  lastGrade: number
  graduated: boolean
  /** 相对今天逾期几天（0 = 今天到期） */
  overdue: number
  /** 距下次复习还有几天（已逾期为 0） */
  interval: number
}

export interface ReviewDayLoad {
  day: string
  count: number
  overdue: number
}

/** 今天该复习什么。 */
export interface ReviewPlan {
  day: string
  /** 每日上限 */
  cap: number
  enabled: boolean
  queue: ReviewItem[]
  /** 今天到期（含逾期）总条数 */
  dueTotal: number
  /** 今天排不下、已摊到未来的条数 */
  deferred: number
  /** 逾期条数 */
  backlog: number
  doneToday: number
  enrolled: number
  graduated: number
  forecast: ReviewDayLoad[]
  nextDueDay: string
  /** 提前复习的天数窗口（0 = 只看今天到期的） */
  ahead: number
  /** 提前模式下拉进来的「还没到期」的条数 */
  aheadTotal: number
  /** 当前生效的复习轮数（学得猛时会压缩） */
  stages: number
  /** 近两周实测每天新学条数 */
  intakePerDay: number
  /** 各内容库的每日复习额度 */
  libCap?: Record<string, number>
}

export interface ReviewConfig {
  dailyCap: number
  enabled: boolean
  /** 各内容库的每日复习条数（算法一条要十几分钟，额度必须单独给） */
  libCap: Record<string, number>
}
