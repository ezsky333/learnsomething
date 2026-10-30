import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  GRADE_ACTIONS,
  capacityNote,
  capLineHeight,
  dueLabel,
  finishedHeadline,
  isNothingDueToday,
  loadHeights,
  nextLabel,
  overloadWarning,
  queueHeadline,
  stageLabel,
  todayPercent,
} from './review.ts'
import type { ReviewDayLoad, ReviewItem, ReviewPlan } from '../types.ts'

function item(over: Partial<ReviewItem> = {}): ReviewItem {
  return {
    docId: 'java',
    itemId: 'q1',
    stage: 0,
    lapses: 0,
    reviewCount: 0,
    lastDay: '2026-03-01',
    dueDay: '2026-03-02',
    lastGrade: -1,
    graduated: false,
    overdue: 0,
    interval: 0,
    ...over,
  }
}

function plan(over: Partial<ReviewPlan> = {}): ReviewPlan {
  return {
    day: '2026-03-01',
    cap: 40,
    enabled: true,
    queue: [],
    dueTotal: 0,
    deferred: 0,
    backlog: 0,
    doneToday: 0,
    enrolled: 0,
    graduated: 0,
    forecast: [],
    nextDueDay: '',
    stages: 7,
    intakePerDay: 0,
    ...over,
  }
}

function forecast(counts: number[]): ReviewDayLoad[] {
  const out: ReviewDayLoad[] = []
  for (let i = 0; i < counts.length; i++) {
    const d = new Date(2026, 2, 1 + i)
    out.push({
      day: `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`,
      count: counts[i],
      overdue: 0,
    })
  }
  return out
}

// ---------- 评分按钮 ----------

test('四个评分按钮齐全且互不重复', () => {
  assert.equal(GRADE_ACTIONS.length, 4)
  const grades = GRADE_ACTIONS.map((a) => a.grade)
  assert.deepEqual(grades, [0, 1, 2, 3])

  // 快捷键不能冲突，否则按一个键触发两个动作
  const keys = GRADE_ACTIONS.map((a) => a.key)
  assert.equal(new Set(keys).size, keys.length)
})

test('评分按钮顺序是「忘了 → 模糊 → 记住」的由差到好', () => {
  // 渐进的顺序符合直觉，误点的概率最低
  assert.equal(GRADE_ACTIONS[0].grade, 0)
  assert.equal(GRADE_ACTIONS[GRADE_ACTIONS.length - 2].grade, 2)
})

// ---------- 阶段与到期文案 ----------

test('stageLabel 显示第几轮共几轮', () => {
  assert.equal(stageLabel(item({ stage: 0 }), 7), '第 1 / 7 轮')
  assert.equal(stageLabel(item({ stage: 3 }), 5), '第 4 / 5 轮')
})

test('dueLabel 区分「逾期」「今天到期」和「还没到期」', () => {
  assert.equal(dueLabel(item({ overdue: 0, interval: 0 })), '今天到期')
  assert.equal(dueLabel(item({ overdue: 3 })), '逾期 3 天')
  assert.equal(dueLabel(item({ overdue: 0, interval: 2 })), '2 天后到期')
})

test('nextLabel 把天数翻成人话', () => {
  assert.equal(nextLabel(item({ graduated: true })), '已完成全部复习')
  assert.equal(nextLabel(item({ overdue: 2 })), '待补')
  assert.equal(nextLabel(item({ interval: 0 })), '今天')
  assert.equal(nextLabel(item({ interval: 1 })), '明天')
  assert.equal(nextLabel(item({ interval: 6 })), '6 天后')
})

// ---------- 今日队列提示 ----------

test('关闭时明确说明已关闭', () => {
  assert.equal(queueHeadline(plan({ enabled: false })), '复习模块已关闭')
})

test('没有到期内容时给出下次日期，而不是留白', () => {
  const s = queueHeadline(plan({ nextDueDay: '2026-03-05' }))
  assert.ok(s.includes('2026-03-05'))
})

test('全部毕业时给一句收尾话', () => {
  const s = queueHeadline(plan({ graduated: 12, enrolled: 0 }))
  assert.ok(s.includes('🎉'))
})

test('额度用完后不再催', () => {
  const s = queueHeadline(plan({ cap: 20, doneToday: 20, dueTotal: 30 }))
  assert.ok(s.includes('额度已用完'))
})

test('正常情况报出剩余条数', () => {
  const s = queueHeadline(plan({ queue: [item(), item()], dueTotal: 5, cap: 40 }))
  assert.ok(s.includes('2 条'))
})

test('做完后如果顺延了内容，要告知已摊到后面而不是让人以为还有一堆', () => {
  const s = finishedHeadline(plan({ queue: [item()], deferred: 15 }))
  assert.ok(s.includes('15'))
  assert.ok(s.includes('摊到'))
})

test('没顺延时只报平安', () => {
  const s = finishedHeadline(plan({ queue: [item()] }))
  assert.ok(s.includes('明天继续'))
})

// ---------- 负载可视化 ----------

test('loadHeights 按未来峰值归一化，峰值是 1', () => {
  const h = loadHeights(forecast([0, 5, 10, 0]))
  assert.equal(h.length, 4)
  assert.equal(h[0], 0)
  assert.equal(h[2], 1)
  assert.ok(h.every((v) => v >= 0 && v <= 1))
})

test('loadHeights 在全部为空时不会除零', () => {
  const h = loadHeights(forecast([0, 0, 0]))
  assert.ok(h.every((v) => v === 0))
})

test('capLineHeight 在上限高于峰值时大于 1（不画线）', () => {
  // 峰值 10，上限 40 → 线在 4 倍高度处，图表里不该出现
  const line = capLineHeight(forecast([10, 5]), 40)
  assert.ok(line > 1)
})

test('capLineHeight 在超载时小于 1（要画线）', () => {
  // 峰值 80，上限 40 → 线在 0.5 处
  const line = capLineHeight(forecast([80, 20]), 40)
  assert.ok(Math.abs(line - 0.5) < 1e-9)
})

// ---------- 过载预警 ----------

test('未来没有超额时不报警', () => {
  assert.equal(overloadWarning(plan({ cap: 40, forecast: forecast([10, 20, 5]) })), '')
})

test('超额时报出天数与最忙的一天', () => {
  const s = overloadWarning(plan({ cap: 20, forecast: forecast([5, 30, 50, 10]) }))
  assert.ok(s.includes('2 天'))
  assert.ok(s.includes('50'))
})

test('关闭时永远不报警', () => {
  assert.equal(overloadWarning(plan({ enabled: false, cap: 5, forecast: forecast([99]) })), '')
})

// ---------- 容量说明：本模块最核心的一条反馈 ----------

test('负荷在容量内时说明可以走满轮数', () => {
  const s = capacityNote(plan({ cap: 40, stages: 7, intakePerDay: 5 }))
  // 5 × 7 = 35 <= 40
  assert.ok(s.includes('35'))
  assert.ok(s.includes('7 轮'))
})

test('负荷超容量时说明会被压缩，并给出建议的学习量', () => {
  const s = capacityNote(plan({ cap: 40, stages: 3, intakePerDay: 30 }))
  // 30 × 3 = 90 > 40；建议 40/3 = 13 条
  assert.ok(s.includes('90'))
  assert.ok(s.includes('超过上限'))
  assert.ok(s.includes('13'))
})

test('还没开始学时不显示容量说明', () => {
  assert.equal(capacityNote(plan({ intakePerDay: 0 })), '')
})

test('关闭时不显示容量说明', () => {
  assert.equal(capacityNote(plan({ enabled: false, intakePerDay: 5 })), '')
})

// ---------- 今日完成度 ----------

test('todayPercent 按「已做 + 待做」算', () => {
  const p = plan({ doneToday: 5, queue: [item(), item(), item(), item(), item()], dueTotal: 10 })
  assert.equal(todayPercent(p), 50)
})

test('没有任务时视为 100%（不做真空进度条）', () => {
  assert.equal(todayPercent(plan({ dueTotal: 0 })), 100)
})

test('全部做完时是 100%', () => {
  assert.equal(todayPercent(plan({ doneToday: 8, queue: [], dueTotal: 8 })), 100)
})

// ---------- 「今天没事」与「提前复习」的区分 ----------

test('isNothingDueToday 只看真正的到期数，不受提前复习影响', () => {
  // 提前复习把队列填满了，但今天确实没有到期的
  const p = plan({ dueTotal: 0, queue: [item(), item()], ahead: 3, aheadTotal: 2 })
  assert.equal(isNothingDueToday(p), true)
})

test('有到期内容时 isNothingDueToday 为 false', () => {
  assert.equal(isNothingDueToday(plan({ dueTotal: 5 })), false)
})

test('关闭时不算「今天没事」', () => {
  assert.equal(isNothingDueToday(plan({ enabled: false, dueTotal: 0 })), false)
})
