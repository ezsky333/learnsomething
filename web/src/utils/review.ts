/**
 * 复习模块的展示逻辑。
 *
 * 调度算法全在服务端（internal/store/review.go），这里只负责把数字翻译成人话，
 * 以及把「今天该复习什么」渲染成可读的列表。
 */

import type { ReviewDayLoad, ReviewGrade, ReviewItem, ReviewPlan } from '@/types'

/** 四个评分按钮的元数据：顺序、文案、说明。 */
export interface GradeAction {
  grade: ReviewGrade
  label: string
  hint: string
  /** 键盘快捷键 */
  key: string
  tone: 'red' | 'amber' | 'mint' | 'ghost'
}

export const GRADE_ACTIONS: GradeAction[] = [
  { grade: 0, label: '忘了', hint: '退回第 1 阶段，明天重来', key: '1', tone: 'red' },
  { grade: 1, label: '模糊', hint: '原地踏步，间隔对折', key: '2', tone: 'amber' },
  { grade: 2, label: '记住了', hint: '进入下一阶段', key: '3', tone: 'mint' },
  { grade: 3, label: '今天没空', hint: '顺延到明天，不扣阶段', key: '4', tone: 'ghost' },
]

/** 阶段文案：第几轮 / 共几轮。 */
export function stageLabel(it: ReviewItem, stages: number): string {
  return `第 ${it.stage + 1} / ${stages} 轮`
}

/**
 * 这条内容现在的处境。
 *
 * 逾期最久的应当被明确指出 —— 否则用户看到一长串卡片，
 * 完全不知道自己已经拖了多久。
 */
export function dueLabel(it: ReviewItem): string {
  if (it.overdue > 0) return `逾期 ${it.overdue} 天`
  if (it.interval > 0) return `${it.interval} 天后到期`
  return '今天到期'
}

/** 下次复习在几天后。 */
export function nextLabel(it: ReviewItem): string {
  if (it.graduated) return '已完成全部复习'
  if (it.overdue > 0) return '待补'
  if (it.interval <= 0) return '今天'
  if (it.interval === 1) return '明天'
  return `${it.interval} 天后`
}

/**
 * 今日队列的情绪化提示。
 *
 * 复习模块最容易失败的地方是「一打开就看到 80 条」直接关掉，
 * 所以每种情况都要给一句「今天该做什么」而不是只报数字。
 *
 * 判断顺序很重要：先看队列里有没有活，再看今天有没有到期 ——
 * 提前复习时「今天没有到期的」和「现在有 20 条要做」同时成立，
 * 只报前者会让下面的卡片显得凭空冒出来。
 */
export function queueHeadline(plan: ReviewPlan): string {
  if (!plan.enabled) return '复习模块已关闭'
  if (plan.queue.length > 0) {
    if (plan.aheadTotal > 0) {
      return `现在可以复习 ${plan.queue.length} 条（其中 ${plan.aheadTotal} 条是提前做的）`
    }
    return `今天还有 ${plan.queue.length} 条要复习`
  }
  if (plan.doneToday >= plan.cap) return `今天的复习额度已用完（${plan.cap} 条），剩下的明天继续`
  if (plan.dueTotal === 0) {
    if (plan.graduated > 0 && plan.enrolled === 0) return '全部内容都已复习完毕，安心去学新的 🎉'
    if (plan.nextDueDay) return `今天没有要复习的，下次是 ${plan.nextDueDay}`
    return '今天没有要复习的'
  }
  return '今天的额度已用完'
}

/** 队列全部做完后的一句收尾话。 */
export function finishedHeadline(plan: ReviewPlan): string {
  if (plan.deferred > 0) {
    return `今天的 ${plan.queue.length} 条已经复习完，剩下 ${plan.deferred} 条已自动摊到后面几天，明天见 👋`
  }
  if (plan.backlog > 0) return '今天的复习做完了 👋'
  return '今天的复习做完了，明天继续 👋'
}

/**
 * 负载条的相对高度（0..1）。
 *
 * 按「未来峰值」归一化而不是按上限 —— 用上限做刻度时，
 * 在籍内容较少的日子柱子会矮成一条线，图看起来像坏了。
 * 上限改用一条参考线表达：超载的日子一眼就能看出是黄柱越线。
 */
export function loadHeights(forecast: ReviewDayLoad[]): number[] {
  const max = Math.max(1, ...forecast.map((f) => f.count))
  return forecast.map((f) => Math.min(1, f.count / max))
}

/**
 * 上限线在图表里的高度（0..1）；超过图表顶部时返回 >1，调用方据此决定是否画。
 */
export function capLineHeight(forecast: ReviewDayLoad[], cap: number): number {
  const max = Math.max(1, ...forecast.map((f) => f.count))
  return cap / max
}

/**
 * 负载预警：未来几天有没有堆积。
 *
 * 用户最需要的不是「今天多少条」，而是「我是不是在给自己挖坑」。
 */
export function overloadWarning(plan: ReviewPlan): string {
  if (!plan.enabled || plan.forecast.length === 0) return ''
  const future = plan.forecast.slice(1)
  const heavy = future.filter((f) => f.count > plan.cap)
  if (heavy.length === 0) return ''
  const worst = heavy.reduce((a, b) => (b.count > a.count ? b : a))
  return `未来 ${heavy.length} 天会超出每日上限，最多的一天（${worst.day}）有 ${worst.count} 条`
}

/**
 * 学习强度与复习容量的关系提示。
 *
 * 这是本模块最核心的一条反馈：每天新学 I 条，复习轮数 L 轮，
 * 稳态下每天必然产生 I×L 条复习需求。若超过每日上限 C，
 * 系统会自动压缩轮数（保证做得完），代价是复习深度变浅。
 * 用户需要知道这件事，否则会以为「计划怎么只有 3 轮」是 bug。
 */
export function capacityNote(plan: ReviewPlan): string {
  if (!plan.enabled) return ''
  const intake = plan.intakePerDay
  if (intake <= 0) return ''
  const need = Math.round(intake * plan.stages)
  if (need <= plan.cap) {
    return `按每天新学 ${intake} 条的节奏，稳态复习量约 ${need} 条/天，在上限 ${plan.cap} 条之内，可以走满 ${plan.stages} 轮。`
  }
  return `每天新学 ${intake} 条 × ${plan.stages} 轮 ≈ ${need} 条/天的复习需求，超过上限 ${plan.cap} 条。系统已把复习压缩到 ${plan.stages} 轮以保证每天都做得完；想复习得更深，就把每天新学的量降到 ${Math.max(1, Math.floor(plan.cap / plan.stages))} 条以内。`
}

/** 进度百分比（今天已完成 / 今日应做）。 */
export function todayPercent(plan: ReviewPlan): number {
  const target = plan.doneToday + plan.queue.length
  if (target <= 0) return plan.dueTotal === 0 ? 100 : 0
  return Math.round((plan.doneToday / target) * 100)
}

/**
 * 「今天没有要复习的」这句是否成立。
 *
 * 提前复习模式下队列可能非空，但严格说今天确实没有到期的 ——
 * 两件事要分开说，否则用户会以为系统搞错了。
 */
export function isNothingDueToday(plan: ReviewPlan): boolean {
  return plan.enabled && plan.dueTotal === 0
}
