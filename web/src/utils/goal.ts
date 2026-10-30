/**
 * 学习任务的进度推导。
 *
 * 全部在前端算，不落库 —— 因为「已读多少条」是随进度实时变化的，
 * 存下来只会和数据不同步。
 *
 * 核心思路：
 *   1. 任务里选中的模块 + 每个模块的总条数 = 任务总工作量
 *   2. 时间轴从 startDay 到 dueDay（含两端），每天应完成 = 总量 / 总天数
 *   3. 今天应有的累计进度 = 每天应完成 × 已过天数
 *   4. 实际完成 − 应有进度 = 超前/落后多少
 */

import type { DocMeta, Goal } from '@/types'

const DAY_MS = 24 * 60 * 60 * 1000

/** 把 YYYY-MM-DD 解析成本地零点的 Date。 */
export function parseDay(day: string): Date {
  const [y, m, d] = day.split('-').map(Number)
  return new Date(y, (m ?? 1) - 1, d ?? 1)
}

/** 本地日期格式化为 YYYY-MM-DD。 */
export function formatDay(date: Date): string {
  const y = date.getFullYear()
  const m = `${date.getMonth() + 1}`.padStart(2, '0')
  const d = `${date.getDate()}`.padStart(2, '0')
  return `${y}-${m}-${d}`
}

export function today(): string {
  return formatDay(new Date())
}

/** 两个日期之间相差的天数（b - a），按本地零点算。 */
export function daysBetween(a: string, b: string): number {
  const da = parseDay(a).getTime()
  const db = parseDay(b).getTime()
  return Math.round((db - da) / DAY_MS)
}

export interface GoalProgress {
  /** 任务是否已设置 */
  hasGoal: boolean
  /** 任务是否已经过期（截止日在今天之前） */
  expired: boolean
  /** 选中模块的总条数 */
  totalItems: number
  /** 选中模块里已完成的条数 */
  doneItems: number
  /** 整体完成百分比 */
  percent: number
  /** 任务总天数（含首尾） */
  totalDays: number
  /** 已经过去的天数（第几天，1-based） */
  daysElapsed: number
  /** 剩余天数（含今天） */
  daysLeft: number
  /** 今天结束时理论上应完成多少条（累计） */
  expectedDone: number
  /** 实际 − 应有：正数超前，负数落后 */
  aheadBy: number
  /** 每天应完成多少条 */
  dailyTarget: number
  /** 今天还差多少条才追上（0 表示已追上或超前） */
  todayRemaining: number
  /** 今天实际完成了几条 */
  doneToday: number
  /** 按当前节奏，预计哪天能全部完成（当超前较多时可能早于截止日） */
  projectedEndDay: string
  /** 进度条上「今天应有」的位置百分比 */
  expectedPercent: number
  /** 按模块拆分后的分别进度 */
  modules: ModuleProgress[]
}

/** 单个模块（一篇文档 = 一个模块）的进度。 */
export interface ModuleProgress {
  slug: string
  title: string
  /** 所属分组，如「01 Java 面试篇」「算法教程 · 代码随想录」 */
  group: string
  library: string
  totalItems: number
  doneItems: number
  percent: number
  /** 今天结束时理论上应完成到第几条（累计） */
  expectedDone: number
  /** 实际 − 应有 */
  aheadBy: number
  /** 今天还差多少条 */
  todayRemaining: number
  /** 该模块每天应完成多少条（按天数均摊，或从总日目标按占比分摊） */
  dailyTarget: number
  pace: PaceLevel
}

/**
 * 按模块拆分进度。
 *
 * 用户勾选的模块之间进度可能差很远（Java 基础快学完了、算法链表还没开始），
 * 只给一个总数会掩盖这种差异，所以每个模块都要能单独回答
 * 「今天我这一块应该学到哪儿、差多少」。
 *
 * 关键：**不同内容库的速度天然不同**。算法一篇长文 25 分钟，八股一道题 5 分钟，
 * 用「总条数 / 总天数」平摊会让两边都失真 —— 八股显得学不完，算法显得随便。
 * 所以这里按「每条的分钟数」把总量折算成加权工作量，再按权重分摊每日额度。
 *
 * @param minutesPerItem 各库学一条的分钟数；缺省时各库等权。
 * @param splitDaily 为 true 时，把「每日总目标」按工作量占比分摊到各模块；
 *                   否则每个模块各自用「自身工作量 / 总天数」算。
 */
export function computeModuleProgress(
  goal: Goal | null,
  docs: DocMeta[],
  doneOf: (docSlug: string) => number,
  minutesPerItem?: Record<string, number>,
): ModuleProgress[] {
  if (!goal) return []

  const bySlug = new Map(docs.map((d) => [d.slug, d]))
  const todayStr = today()
  const totalDays = Math.max(1, daysBetween(goal.startDay, goal.dueDay) + 1)
  const rawElapsed = daysBetween(goal.startDay, todayStr) + 1
  const daysElapsed = Math.min(Math.max(rawElapsed, 0), totalDays)
  const expired = daysBetween(todayStr, goal.dueDay) < 0

  const minutesOf = (library: string) => {
    const m = minutesPerItem?.[library]
    return m && m > 0 ? m : 0
  }

  // 先算出参与统计的模块，以及总工作量（用于按占比分摊自定目标）
  const usable = goal.docSlugs
    .map((s) => bySlug.get(s))
    .filter((d): d is DocMeta => !!d && !d.readingList)
  const grandTotal = usable.reduce((sum, d) => {
    const w = minutesOf(d.library)
    return sum + (w > 0 ? d.questionCount * w : d.questionCount)
  }, 0)

  /**
   * 某个库每天可用的分钟数。
   *
   * 配置了时间窗时不能用「全天总时间」再去按权重分摊 —— 算法只能用上午那段，
   * 八股只能用下午那段。否则会算出「算法每天 0.2 条」这种明显不对的数字
   * （和计划推算面板的「每天 2.0 条」自相矛盾）。
   */
  const windows = goal.windows ?? []
  const minutesForLib = (library: string): number => {
    if (windows.length === 0) return 0 // 没有时间窗时走「按工作量分摊」的老路
    let sum = 0
    let matched = false
    for (const w of windows) {
      // 窗限定了库列表时，只有列出的库能用这个窗
      if (w.libs.length > 0 && !w.libs.includes(library)) continue
      matched = true
      sum += w.minutes
    }
    return matched ? sum : 0
  }

  const out: ModuleProgress[] = []
  for (const doc of usable) {
    const total = doc.questionCount
    const done = Math.min(doneOf(doc.slug), total)
    const per = minutesOf(doc.library)
    const weight = per > 0 ? total * per : total

    // 每天的额度：有自定总目标时按工作量占比分摊，否则按这个库自己的时间预算
    let daily: number
    if (goal.dailyTarget > 0 && grandTotal > 0) {
      daily = goal.dailyTarget * (weight / grandTotal)
    } else if (per > 0 && windows.length > 0) {
      // 时间窗模式：直接用该库在窗里能拿到的分钟数 ÷ 单条成本
      const libMinutes = minutesForLib(doc.library)
      daily = libMinutes > 0 ? libMinutes / per : 0
    } else if (per > 0 && goal.dailyMinutes > 0) {
      // 按时间预算反推：每天的时间先按工作量分给这个模块，再除以单条成本
      const shareMinutes = goal.dailyMinutes * (weight / (grandTotal || 1))
      daily = shareMinutes / per
    } else {
      daily = total / totalDays
    }

    const expected = Math.min(total, daily * daysElapsed)
    const ahead = done - expected
    const remain = Math.max(0, Math.ceil(expected - done))
    const percent = total > 0 ? Math.min(100, (done / total) * 100) : 0

    let pace: PaceLevel
    if (total > 0 && done >= total) pace = 'done'
    else if (expired) pace = 'expired'
    else {
      const unit = daily || 1
      if (ahead >= unit) pace = 'ahead'
      else if (ahead >= -0.5 * unit) pace = 'onTrack'
      else if (ahead >= -2 * unit) pace = 'slightlyBehind'
      else pace = 'behind'
    }

    out.push({
      slug: doc.slug,
      title: doc.title,
      group: doc.group,
      library: doc.library,
      totalItems: total,
      doneItems: done,
      percent,
      expectedDone: expected,
      aheadBy: ahead,
      todayRemaining: remain,
      dailyTarget: daily,
      pace,
    })
  }
  return out
}

/** 节奏评价：给用户一句人话。 */
export type PaceLevel = 'none' | 'ahead' | 'onTrack' | 'slightlyBehind' | 'behind' | 'done' | 'expired'

export function paceOf(g: GoalProgress): PaceLevel {
  if (!g.hasGoal) return 'none'
  if (g.totalItems > 0 && g.doneItems >= g.totalItems) return 'done'
  if (g.expired) return 'expired'
  // 落后幅度按「每天应完成量」衡量，避免总量差异导致误判
  const daily = g.dailyTarget || 1
  const diff = g.aheadBy
  if (diff >= daily) return 'ahead'
  if (diff >= -0.5 * daily) return 'onTrack'
  if (diff >= -2 * daily) return 'slightlyBehind'
  return 'behind'
}

/**
 * 计算任务进度。
 *
 * @param goal      当前任务
 * @param docs      内容库全量元信息（用来查每个模块的条数）
 * @param doneOf    给定 docSlug 返回该模块已完成条数
 * @param doneTodayOf 给定 docSlug 返回该模块今天完成的条数（可选）
 * @param minutesPerItem 各库学一条的分钟数，用于按真实成本分摊（可选）
 */
export function computeGoalProgress(
  goal: Goal | null,
  docs: DocMeta[],
  doneOf: (docSlug: string) => number,
  doneTodayOf?: (docSlug: string) => number,
  minutesPerItem?: Record<string, number>,
): GoalProgress {
  const empty: GoalProgress = {    hasGoal: false,
    expired: false,
    totalItems: 0,
    doneItems: 0,
    percent: 0,
    totalDays: 0,
    daysElapsed: 0,
    daysLeft: 0,
    expectedDone: 0,
    aheadBy: 0,
    dailyTarget: 0,
    todayRemaining: 0,
    doneToday: 0,
    projectedEndDay: '',
    expectedPercent: 0,
    modules: [],
  }
  if (!goal) return empty

  const bySlug = new Map(docs.map((d) => [d.slug, d]))
  let totalItems = 0
  let doneItems = 0
  let doneToday = 0
  for (const slug of goal.docSlugs) {
    const doc = bySlug.get(slug)
    if (!doc) continue
    // 阅读清单类文档（只有外链目录）不计入理论进度，否则会严重低估每天的工作量
    if (doc.readingList) continue
    totalItems += doc.questionCount
    doneItems += Math.min(doneOf(slug), doc.questionCount)
    if (doneTodayOf) doneToday += doneTodayOf(slug)
  }

  const todayStr = today()
  // 任务总天数含首尾两天，所以 +1
  const totalDays = Math.max(1, daysBetween(goal.startDay, goal.dueDay) + 1)
  const expired = daysBetween(todayStr, goal.dueDay) < 0

  // 已过天数：今天算第几天（1-based）。夹在 [0, totalDays]：
  // 任务还没开始是 0，已过期则封顶到 totalDays。
  const rawElapsed = daysBetween(goal.startDay, todayStr) + 1
  const daysElapsed = Math.min(Math.max(rawElapsed, 0), totalDays)

  // 剩余天数（含今天）。过期后必须为 0，不能再减出正数。
  const daysLeft = expired ? 0 : Math.max(1, totalDays - daysElapsed + 1)

  // 每天应完成的条数。
  //
  // 混库时这个数字本身意义有限（算法一条 25 分钟、八股 5 分钟），
  // 它只用于「实际 vs 应有」的对比，所以按条数均摊即可 ——
  // 真正指导做题的分库速度在 modules 里，由各模块自己的时间成本算出。
  const auto = totalItems / totalDays
  const dailyTarget = goal.dailyTarget > 0 ? goal.dailyTarget : auto

  // 今天结束时应有 = 每天应完成 × 已过天数（不超过总量）
  const expectedDone = Math.min(totalItems, dailyTarget * daysElapsed)
  const aheadBy = doneItems - expectedDone
  const todayRemaining = Math.max(0, Math.ceil(expectedDone - doneItems))
  const percent = totalItems > 0 ? Math.min(100, (doneItems / totalItems) * 100) : 0
  const expectedPercent = totalItems > 0 ? Math.min(100, (expectedDone / totalItems) * 100) : 0

  // 按当前总节奏推算完成日：剩余条数 / 每天应完成量
  let projectedEndDay = goal.dueDay
  const remain = totalItems - doneItems
  if (remain > 0 && dailyTarget > 0) {
    const needDays = Math.ceil(remain / dailyTarget)
    const d = new Date()
    d.setDate(d.getDate() + needDays - 1)
    projectedEndDay = formatDay(d)
  }

  return {
    hasGoal: true,
    expired,
    totalItems,
    doneItems,
    percent,
    totalDays,
    daysElapsed,
    daysLeft,
    expectedDone,
    aheadBy,
    dailyTarget,
    todayRemaining,
    doneToday,
    projectedEndDay,
    expectedPercent,
    modules: computeModuleProgress(goal, docs, doneOf, minutesPerItem),
  }
}
