/**
 * 学习计划推算的展示逻辑。
 *
 * 服务端给出逐日排期（internal/plan），这里负责把它翻译成：
 *   - 一句「这个目标现不现实」的结论
 *   - 一张能看懂的压力曲线（新学 vs 复习）
 *   - 停滞日的处理提示
 */

import type { PlanAdvice, PlanDay, PlanLibInput, StudyPlan } from '@/types'

/** 星期几的中文名，0=周日。 */
export const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六'] as const

/** 各内容库「学一条」的默认分钟数，和服务端默认值保持一致。 */
export const DEFAULT_MINUTES: Record<string, number> = {
  interview: 5,
  algo: 25,
  ai: 8,
}

export const LIBRARY_LABEL: Record<string, string> = {
  interview: '八股',
  algo: '算法',
  ai: '大模型',
}

/** 把 YYYY-MM-DD 转成「3/5 周四」这种短标签。 */
export function dayLabel(day: string): string {
  const [y, m, d] = day.split('-').map(Number)
  if (!y || !m || !d) return day
  const wd = new Date(y, m - 1, d).getDay()
  return `${m}/${d} 周${WEEKDAYS[wd]}`
}

/**
 * 「这个目标现实吗」的结论。
 *
 * 用户最需要知道的是「能不能在截止日前学完」以及「如果不行，差在哪」，
 * 所以可行与不可行各自给一句可执行的话，而不是只报数字。
 */
export function verdictText(plan: StudyPlan, advice: PlanAdvice): string {
  if (!plan.days.length) return '还没有可以排期的内容'
  if (advice.feasible) {
    const finish = plan.finishDay
    const slack = daysBetweenText(finish, plan.days[plan.days.length - 1].day)
    if (slack > 7) {
      return `目标可行：按每天 ${plan.dailyMinutes} 分钟，预计 ${finish} 就能全部学完，比截止日早 ${slack} 天。`
    }
    return `目标可行：按每天 ${plan.dailyMinutes} 分钟，预计 ${finish} 学完，和截止日基本吻合。`
  }
  return `目标偏紧：按每天 ${plan.dailyMinutes} 分钟，到截止日只能学完 ${plan.learned} / ${plan.totalNew} 条。`
}

/**
 * 不可行时给出的替代方案。
 *
 * 两个方向都要给：加班（提高每天时间）和延期（放宽截止日），
 * 并明确推荐一个折中值 —— 只给「不可能」会让人放弃，只给一个方向又太死板。
 */
export function adviceText(plan: StudyPlan, advice: PlanAdvice): string {
  if (advice.feasible) {
    if (advice.reviewShare > 0.5) {
      return `注意：这段时间有 ${Math.round(advice.reviewShare * 100)}% 的时间花在复习上。想学得更快就把每天的时间再提高些。`
    }
    return ''
  }

  const parts: string[] = []
  if (advice.requiredDailyMinutes > plan.dailyMinutes) {
    parts.push(`保持截止日不变，每天至少要 ${advice.requiredDailyMinutes} 分钟`)
  }
  if (advice.earliestFinishDay) {
    parts.push(`保持每天 ${plan.dailyMinutes} 分钟，最早 ${advice.earliestFinishDay} 学完`)
  }
  return parts.join('；') + '。'
}

/** 折中建议的一句人话。 */
export function suggestionText(advice: PlanAdvice): string {
  if (advice.feasible) return ''
  if (!advice.suggestedDailyMinutes) return ''
  return `推荐方案：每天 ${advice.suggestedDailyMinutes} 分钟，截止日改到 ${advice.suggestedDueDay}。`
}

/** 两个 YYYY-MM-DD 相差的天数（b - a）。 */
export function daysBetweenText(a: string, b: string): number {
  const pa = a.split('-').map(Number)
  const pb = b.split('-').map(Number)
  if (pa.length !== 3 || pb.length !== 3) return 0
  const da = new Date(pa[0], pa[1] - 1, pa[2]).getTime()
  const db = new Date(pb[0], pb[1] - 1, pb[2]).getTime()
  return Math.round((db - da) / 86400000)
}

/**
 * 每日时间在「新学」和「复习」之间的分配。
 *
 * 这张图是本模块的核心：它直观显示「学到后期，时间几乎全被复习吃掉」，
 * 从而解释为什么不能「每天固定学 N 条」。
 */
export interface DayLoad {
  day: string
  newMinutes: number
  reviewMinutes: number
  rest: boolean
  total: number
}

export function dayLoads(plan: StudyPlan, limit = 30): DayLoad[] {
  return plan.days.slice(0, limit).map((d) => ({
    day: d.day,
    newMinutes: d.newMinutes,
    reviewMinutes: d.reviewMinutes,
    rest: d.rest,
    total: d.minutesUsed,
  }))
}

/** 复习占比（0..1），用于判断计划是否已被复习压垮。 */
export function reviewShareOf(plan: StudyPlan): number {
  let review = 0
  let total = 0
  for (const d of plan.days) {
    review += d.reviewMinutes
    total += d.minutesUsed
  }
  if (total <= 0) return 0
  return review / total
}

/** 需要提醒的日期：某天用时明显超出每日预算的。 */
export function overloadedDays(plan: StudyPlan): PlanDay[] {
  const limit = plan.dailyMinutes
  if (limit <= 0) return []
  return plan.days.filter((d) => d.minutesUsed > limit)
}

/**
 * 停滞日的一句话解释。
 *
 * 加了停滞日会让计划整体后移，用户需要知道「代价」，否则会以为设置没生效。
 */
export function restDayNote(restCount: number, weeklyRest: number[]): string {
  if (restCount === 0 && weeklyRest.length === 0) return ''
  const parts: string[] = []
  if (restCount > 0) parts.push(`${restCount} 个停滞日`)
  if (weeklyRest.length > 0) {
    parts.push(`每周休 ${weeklyRest.map((w) => WEEKDAYS[w]).join('、')}`)
  }
  return `已扣除 ${parts.join(' + ')}，计划已按剩余的有效天数重排。`
}

/** 某个库在整段计划里的每日新学量（用于说明「算法就是慢」）。 */
export function perLibPace(plan: StudyPlan, library: string): number {
  const studyDays = plan.days.filter((d) => !d.rest && d.newMinutes > 0).length
  if (studyDays <= 0) return 0
  let n = 0
  for (const d of plan.days) n += d.new[library] ?? 0
  return n / studyDays
}

/** 计划总览的一句话。 */
export function planSummary(plan: StudyPlan, libs: PlanLibInput[]): string {
  if (!plan.days.length) return ''
  const studyDays = plan.days.filter((d) => !d.rest).length
  const perLib = libs
    .map((l) => `${LIBRARY_LABEL[l.library] ?? l.library} ${l.remaining} 条`)
    .join(' + ')
  return `${perLib}，共 ${plan.totalNew} 条；可用 ${studyDays} 天，每天 ${plan.dailyMinutes} 分钟。`
}
