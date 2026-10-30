import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  DEFAULT_MINUTES,
  LIBRARY_LABEL,
  WEEKDAYS,
  adviceText,
  dayLabel,
  dayLoads,
  daysBetweenText,
  overloadedDays,
  perLibPace,
  planSummary,
  restDayNote,
  reviewShareOf,
  suggestionText,
  verdictText,
} from './plan.ts'
import type { PlanAdvice, PlanDay, StudyPlan } from '../types.ts'

function day(over: Partial<PlanDay> = {}): PlanDay {
  return {
    day: '2026-03-01',
    rest: false,
    new: {},
    review: {},
    newMinutes: 0,
    reviewMinutes: 0,
    minutesUsed: 0,
    remaining: {},
    ...over,
  }
}

function plan(over: Partial<StudyPlan> = {}): StudyPlan {
  return {
    days: [],
    feasible: true,
    finishDay: '2026-03-20',
    totalNew: 100,
    learned: 100,
    reviewMinutesTotal: 0,
    peakMinutes: 0,
    overloadedDays: 0,
    dailyMinutes: 120,
    studyDays: 20,
    restCount: 0,
    ...over,
  }
}

function advice(over: Partial<PlanAdvice> = {}): PlanAdvice {
  return {
    feasible: true,
    requiredDailyMinutes: 120,
    earliestFinishDay: '2026-03-20',
    suggestedDailyMinutes: 120,
    suggestedDueDay: '2026-03-20',
    reviewShare: 0.2,
    verdict: 'ok',
    ...over,
  }
}

// ---------- 常量 ----------

test('WEEKDAYS 从周日开始，和 JS 的 getDay 对齐', () => {
  assert.equal(WEEKDAYS.length, 7)
  assert.equal(WEEKDAYS[0], '日')
  assert.equal(WEEKDAYS[6], '六')
  // 2026-03-01 是周日
  assert.equal(new Date(2026, 2, 1).getDay(), 0)
})

test('各库默认单价里算法明显高于八股', () => {
  assert.ok(DEFAULT_MINUTES.algo > DEFAULT_MINUTES.interview)
  assert.ok(DEFAULT_MINUTES.algo >= 20)
})

test('LIBRARY_LABEL 覆盖三个库', () => {
  for (const lib of ['interview', 'algo', 'ai']) {
    assert.ok(LIBRARY_LABEL[lib])
  }
})

// ---------- 日期 ----------

test('dayLabel 带星期', () => {
  assert.equal(dayLabel('2026-03-01'), '3/1 周日')
  assert.equal(dayLabel('2026-03-05'), '3/5 周四')
})

test('daysBetweenText 算天数差', () => {
  assert.equal(daysBetweenText('2026-03-01', '2026-03-11'), 10)
  assert.equal(daysBetweenText('2026-03-11', '2026-03-01'), -10)
  assert.equal(daysBetweenText('2026-03-01', '2026-03-01'), 0)
})

// ---------- 结论 ----------

test('可行且富余较多时说明能提前多少天', () => {
  const p = plan({
    days: [day({ day: '2026-03-01' }), day({ day: '2026-04-30' })],
    finishDay: '2026-04-10',
  })
  const s = verdictText(p, advice({ feasible: true }))
  assert.ok(s.includes('可行'))
  assert.ok(s.includes('2026-04-10'))
  assert.ok(s.includes('早'))
})

test('不可行时报出学掉了多少 / 总共多少', () => {
  const p = plan({
    days: [day({ day: '2026-03-01' })],
    feasible: false,
    learned: 300,
    totalNew: 900,
  })
  const s = verdictText(p, advice({ feasible: false }))
  assert.ok(s.includes('偏紧'))
  assert.ok(s.includes('300'))
  assert.ok(s.includes('900'))
})

test('没有任何天数时给出明确提示', () => {
  assert.ok(verdictText(plan({ days: [] }), advice()).includes('没有'))
})

// ---------- 替代方案 ----------

test('不可行时同时给出「加时间」和「延截止」两个方向', () => {
  const p = plan({ feasible: false, dailyMinutes: 90 })
  const s = adviceText(
    p,
    advice({
      feasible: false,
      requiredDailyMinutes: 200,
      earliestFinishDay: '2027-01-20',
    }),
  )
  assert.ok(s.includes('200'))
  assert.ok(s.includes('2027-01-20'))
  // 两个方向都要有
  assert.ok(s.includes('至少'))
  assert.ok(s.includes('最早'))
})

test('可行时不啰嗦替代方案', () => {
  assert.equal(adviceText(plan(), advice({ feasible: true, reviewShare: 0.2 })), '')
})

test('可行但复习占比过高时给出提醒', () => {
  const s = adviceText(plan(), advice({ feasible: true, reviewShare: 0.7 }))
  assert.ok(s.includes('70%'))
  assert.ok(s.includes('复习'))
})

test('折中建议格式清楚', () => {
  const s = suggestionText(
    advice({ feasible: false, suggestedDailyMinutes: 160, suggestedDueDay: '2027-01-05' }),
  )
  assert.ok(s.includes('160'))
  assert.ok(s.includes('2027-01-05'))
})

test('可行时不给折中建议', () => {
  assert.equal(suggestionText(advice({ feasible: true })), '')
})

// ---------- 时间分配 ----------

test('dayLoads 截取指定天数并保留休息标记', () => {
  const days = [
    day({ day: '2026-03-01', newMinutes: 60, reviewMinutes: 30, minutesUsed: 90 }),
    day({ day: '2026-03-02', rest: true }),
    day({ day: '2026-03-03', newMinutes: 80 }),
  ]
  const out = dayLoads(plan({ days }), 2)
  assert.equal(out.length, 2)
  assert.equal(out[0].newMinutes, 60)
  assert.equal(out[1].rest, true)
})

test('reviewShareOf 统计整段计划的复习占比', () => {
  const days = [
    day({ newMinutes: 50, reviewMinutes: 50, minutesUsed: 100 }),
    day({ newMinutes: 20, reviewMinutes: 80, minutesUsed: 100 }),
  ]
  assert.ok(Math.abs(reviewShareOf(plan({ days })) - 0.65) < 1e-9)
})

test('全为 0 时不除零', () => {
  assert.equal(reviewShareOf(plan({ days: [day()] })), 0)
})

test('overloadedDays 找出超预算的日子', () => {
  const days = [
    day({ day: '2026-03-01', minutesUsed: 100 }),
    day({ day: '2026-03-02', minutesUsed: 150 }),
    day({ day: '2026-03-03', minutesUsed: 200 }),
  ]
  const over = overloadedDays(plan({ days, dailyMinutes: 120 }))
  assert.equal(over.length, 2)
})

// ---------- 停滞日 ----------

test('没有停滞日时不显示提示', () => {
  assert.equal(restDayNote(0, []), '')
})

test('有停滞日时说明已扣除并重排', () => {
  const s = restDayNote(3, [])
  assert.ok(s.includes('3 个'))
  assert.ok(s.includes('重排'))
})

test('每周固定休息也写进说明', () => {
  const s = restDayNote(0, [0, 6])
  assert.ok(s.includes('日'))
  assert.ok(s.includes('六'))
})

// ---------- 各库速度 ----------

test('perLibPace 算出某库的日均新学条数', () => {
  const days = [
    day({ new: { algo: 2 }, newMinutes: 50 }),
    day({ new: { algo: 4 }, newMinutes: 100 }),
    day({ new: { algo: 0 }, newMinutes: 0 }), // 不算进学习日
    day({ rest: true }),
  ]
  // 2 个有产出的学习日，共 6 条 → 日均 3
  assert.ok(Math.abs(perLibPace(plan({ days }), 'algo') - 3) < 1e-9)
})

test('没有学习日时返回 0', () => {
  assert.equal(perLibPace(plan({ days: [day({ rest: true })] }), 'algo'), 0)
})

// ---------- 总览 ----------

test('planSummary 列出各库剩余与总工作量', () => {
  const p = plan({ days: [day(), day(), day()], totalNew: 900, dailyMinutes: 120 })
  const s = planSummary(p, [
    { library: 'interview', remaining: 700, minutesPerItem: 5 },
    { library: 'algo', remaining: 200, minutesPerItem: 25 },
  ])
  assert.ok(s.includes('700'))
  assert.ok(s.includes('200'))
  assert.ok(s.includes('900'))
  assert.ok(s.includes('八股'))
  assert.ok(s.includes('算法'))
})

test('没有排期时 summary 为空', () => {
  assert.equal(planSummary(plan({ days: [] }), []), '')
})
