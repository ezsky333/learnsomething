/**
 * 学习任务进度推导的单元测试。
 * 运行：pnpm test（或 node --test）
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  computeGoalProgress,
  computeModuleProgress,
  daysBetween,
  formatDay,
  paceOf,
  parseDay,
  today,
} from './goal.ts'
import type { DocMeta, Goal } from '../types.ts'

function doc(slug: string, questionCount: number, readingList = false): DocMeta {
  return {
    slug,
    title: slug,
    group: 'test',
    order: 1,
    sourceUrl: '',
    sourceUpdated: '',
    sectionCount: 1,
    questionCount,
    charCount: questionCount * 100,
    library: 'interview',
    readingList,
  } as DocMeta
}

function goal(docSlugs: string[], startDay: string, dueDay: string, dailyTarget = 0): Goal {
  return {
    docSlugs,
    startDay,
    dueDay,
    dailyTarget,
    dailyMinutes: 120,
    restDays: [],
    weeklyRest: [],
    minutesPerItem: {},
    createdAt: '',
    updatedAt: '',
  }
}

/** 生成相对今天偏移 n 天的日期串 */
function dayOffset(n: number): string {
  const d = new Date()
  d.setDate(d.getDate() + n)
  return formatDay(d)
}

test('daysBetween 按本地日期计算', () => {
  assert.equal(daysBetween('2026-01-01', '2026-01-01'), 0)
  assert.equal(daysBetween('2026-01-01', '2026-01-02'), 1)
  assert.equal(daysBetween('2026-01-01', '2026-01-31'), 30)
  // 跨月、跨年
  assert.equal(daysBetween('2026-01-31', '2026-02-01'), 1)
  assert.equal(daysBetween('2025-12-31', '2026-01-01'), 1)
})

test('parseDay / formatDay 往返一致', () => {
  assert.equal(formatDay(parseDay('2026-03-05')), '2026-03-05')
})

test('没有任务时返回空进度', () => {
  const p = computeGoalProgress(null, [doc('a', 10)], () => 0)
  assert.equal(p.hasGoal, false)
  assert.equal(p.totalItems, 0)
  assert.equal(paceOf(p), 'none')
})

test('总量 = 选中模块条数之和，未选中的不计入', () => {
  const docs = [doc('a', 10), doc('b', 20), doc('c', 99)]
  const g = goal(['a', 'b'], dayOffset(0), dayOffset(9))
  const p = computeGoalProgress(g, docs, () => 0)
  assert.equal(p.totalItems, 30)
  assert.equal(p.totalDays, 10) // 含首尾
})

test('阅读清单类文档不计入进度（避免低估工作量）', () => {
  const docs = [doc('a', 10), doc('list', 500, true)]
  const g = goal(['a', 'list'], dayOffset(0), dayOffset(9))
  const p = computeGoalProgress(g, docs, () => 0)
  assert.equal(p.totalItems, 10, '阅读清单应被排除')
})

test('第一天：今日应有 = 每天应完成量', () => {
  const docs = [doc('a', 100)]
  const g = goal(['a'], dayOffset(0), dayOffset(9)) // 10 天 100 条
  const p = computeGoalProgress(g, docs, () => 0)
  assert.equal(p.totalDays, 10)
  assert.equal(p.daysElapsed, 1)
  assert.equal(p.dailyTarget, 10)
  assert.equal(p.expectedDone, 10, '第 1 天应有 10 条')
  assert.equal(p.aheadBy, -10, '一条没做，落后 10 条')
  assert.equal(p.todayRemaining, 10)
})

test('进度超前 / 落后 / 正好', () => {
  const docs = [doc('a', 100)]
  const g = goal(['a'], dayOffset(-4), dayOffset(5)) // 已过 5 天，共 10 天
  assert.equal(computeGoalProgress(g, docs, () => 50).aheadBy, 0, '50 条正好')
  assert.equal(computeGoalProgress(g, docs, () => 70).aheadBy, 20, '超前 20')
  assert.equal(computeGoalProgress(g, docs, () => 30).aheadBy, -20, '落后 20')
})

test('paceOf 的分档', () => {
  const docs = [doc('a', 100)]
  const g = goal(['a'], dayOffset(-4), dayOffset(5)) // 每天 10
  const mk = (done: number) => paceOf(computeGoalProgress(g, docs, () => done))
  assert.equal(mk(50), 'onTrack') // 差 0
  assert.equal(mk(60), 'ahead') // 超 10 = 1 天量
  assert.equal(mk(45), 'onTrack') // 差 5 = 半天量
  assert.equal(mk(35), 'slightlyBehind') // 差 15
  assert.equal(mk(0), 'behind') // 差 50
})

test('全部完成时状态为 done', () => {
  const docs = [doc('a', 10)]
  const g = goal(['a'], dayOffset(-1), dayOffset(5))
  const p = computeGoalProgress(g, docs, () => 10)
  assert.equal(p.percent, 100)
  assert.equal(paceOf(p), 'done')
})

test('过期任务标记为 expired', () => {
  const docs = [doc('a', 10)]
  const g = goal(['a'], dayOffset(-10), dayOffset(-1))
  const p = computeGoalProgress(g, docs, () => 3)
  assert.equal(p.expired, true)
  assert.equal(paceOf(p), 'expired')
  assert.equal(p.daysLeft, 0)
})

test('用户自定每日目标优先于自动均摊', () => {
  const docs = [doc('a', 100)]
  const g = goal(['a'], dayOffset(0), dayOffset(9), 25) // 自定每天 25
  const p = computeGoalProgress(g, docs, () => 0)
  assert.equal(p.dailyTarget, 25)
  assert.equal(p.expectedDone, 25, '第 1 天按自定目标算')
})

test('完成条数不会被模块总量截断之外地超算', () => {
  const docs = [doc('a', 10)]
  const g = goal(['a'], dayOffset(-5), dayOffset(5))
  // 即使传入 999（脏数据），每个模块最多算满额
  const p = computeGoalProgress(g, docs, () => 999)
  assert.equal(p.doneItems, 10)
  assert.equal(p.percent, 100)
})

test('预期完成日按当前节奏推算', () => {
  const docs = [doc('a', 100)]
  // 10 天 100 条 → 每天 10 条；已完成 60，剩 40 → 还需 4 天
  const g = goal(['a'], dayOffset(-5), dayOffset(4))
  const p = computeGoalProgress(g, docs, () => 60)
  const expected = new Date()
  expected.setDate(expected.getDate() + 3) // 4 天行程的第 4 天 = 今天+3
  assert.equal(p.projectedEndDay, formatDay(expected))
})

test('多模块分别读取各自完成数', () => {
  const docs = [doc('a', 10), doc('b', 10)]
  const g = goal(['a', 'b'], dayOffset(0), dayOffset(9))
  const done: Record<string, number> = { a: 5, b: 3 }
  const p = computeGoalProgress(g, docs, (s) => done[s] ?? 0)
  assert.equal(p.doneItems, 8)
  assert.equal(p.totalItems, 20)
})

test('任务里含已删除的模块不会崩，直接跳过', () => {
  const docs = [doc('a', 10)]
  const g = goal(['a', 'ghost'], dayOffset(0), dayOffset(9))
  const p = computeGoalProgress(g, docs, () => 0)
  assert.equal(p.totalItems, 10)
})

test('today() 与 dayOffset(0) 一致', () => {
  assert.equal(today(), dayOffset(0))
})

// ---------- 分模块进度 ----------

test('分模块：各模块独立计算应有进度', () => {
  // 用户勾选：java(100条) + 链表(50条)，10 天
  const docs = [doc('java', 100), doc('list', 50)]
  const g = goal(['java', 'list'], dayOffset(-4), dayOffset(5)) // 第 5 天
  const done: Record<string, number> = { java: 50, list: 0 }
  const p = computeGoalProgress(g, docs, (s) => done[s] ?? 0)

  assert.equal(p.modules.length, 2)

  const java = p.modules.find((m) => m.slug === 'java')!
  const list = p.modules.find((m) => m.slug === 'list')!

  // java: 每天 10 条，第 5 天应有 50，实际 50 → 正好
  assert.equal(java.totalItems, 100)
  assert.equal(java.dailyTarget, 10)
  assert.equal(java.expectedDone, 50)
  assert.equal(java.aheadBy, 0)
  assert.equal(java.pace, 'onTrack')

  // 链表: 每天 5 条，第 5 天应有 25，实际 0 → 落后 25
  assert.equal(list.totalItems, 50)
  assert.equal(list.dailyTarget, 5)
  assert.equal(list.expectedDone, 25)
  assert.equal(list.aheadBy, -25)
  assert.equal(list.pace, 'behind')
  assert.equal(list.todayRemaining, 25)
})

test('分模块：一个已完成的模块显示 done', () => {
  const docs = [doc('a', 10), doc('b', 20)]
  const g = goal(['a', 'b'], dayOffset(-2), dayOffset(7))
  const done: Record<string, number> = { a: 10, b: 0 }
  const p = computeGoalProgress(g, docs, (s) => done[s] ?? 0)
  const a = p.modules.find((m) => m.slug === 'a')!
  assert.equal(a.pace, 'done')
  assert.equal(a.percent, 100)
})

test('分模块：自定每日目标按条数占比分摊', () => {
  // a 有 30 条、b 有 10 条，总共 40；自定每天 20 条
  // 按占比：a 应分到 20*(30/40)=15，b 分到 20*(10/40)=5
  const docs = [doc('a', 30), doc('b', 10)]
  const g = goal(['a', 'b'], dayOffset(0), dayOffset(3), 20)
  const p = computeGoalProgress(g, docs, () => 0)
  const a = p.modules.find((m) => m.slug === 'a')!
  const b = p.modules.find((m) => m.slug === 'b')!
  assert.equal(Number(a.dailyTarget.toFixed(2)), 15)
  assert.equal(Number(b.dailyTarget.toFixed(2)), 5)
  // 各模块每日额度之和 = 总日目标
  assert.equal(Number((a.dailyTarget + b.dailyTarget).toFixed(2)), 20)
})

test('分模块：阅读清单模块不出现在列表里', () => {
  const docs = [doc('a', 10), doc('reading', 99, true)]
  const g = goal(['a', 'reading'], dayOffset(0), dayOffset(9))
  const p = computeGoalProgress(g, docs, () => 0)
  assert.equal(p.modules.length, 1)
  assert.equal(p.modules[0].slug, 'a')
})

test('分模块：模块进度之和等于总体进度', () => {
  const docs = [doc('a', 30), doc('b', 50), doc('c', 20)]
  const g = goal(['a', 'b', 'c'], dayOffset(-3), dayOffset(6))
  const done: Record<string, number> = { a: 12, b: 5, c: 20 }
  const p = computeGoalProgress(g, docs, (s) => done[s] ?? 0)
  const sumDone = p.modules.reduce((s, m) => s + m.doneItems, 0)
  const sumTotal = p.modules.reduce((s, m) => s + m.totalItems, 0)
  assert.equal(sumDone, p.doneItems)
  assert.equal(sumTotal, p.totalItems)
})

test('分模块：过期任务各模块都是 expired', () => {
  const docs = [doc('a', 10), doc('b', 10)]
  const g = goal(['a', 'b'], dayOffset(-9), dayOffset(-2))
  const p = computeGoalProgress(g, docs, () => 1)
  for (const m of p.modules) {
    assert.equal(m.pace, 'expired')
  }
})
// ---------- 分库速度：算法和八股不能用同一个速度 ----------

function docLib(slug: string, questionCount: number, library: string): DocMeta {
  return {
    slug,
    title: slug,
    group: 'test',
    order: 1,
    sourceUrl: '',
    sourceUpdated: '',
    sectionCount: 1,
    questionCount,
    charCount: questionCount * 100,
    library: library as DocMeta['library'],
  } as DocMeta
}

function goalMin(
  docSlugs: string[],
  startDay: string,
  dueDay: string,
  dailyMinutes: number,
): Goal {  return {
    docSlugs,
    startDay,
    dueDay,
    dailyTarget: 0,
    dailyMinutes,
    restDays: [],
    weeklyRest: [],
    minutesPerItem: {},
    createdAt: '',
    updatedAt: '',
  }
}

test('按时间预算分库时，八股每日条数应远高于算法', () => {
  const docs = [docLib('java', 79, 'interview'), docLib('algo-array', 9, 'algo')]
  const g = goalMin(['java', 'algo-array'], dayOffset(-1), dayOffset(30), 120)
  const mods = computeModuleProgress(g, docs, () => 0, { interview: 5, algo: 25 })

  const java = mods.find((m) => m.slug === 'java')!
  const algo = mods.find((m) => m.slug === 'algo-array')!

  // 79*5=395 分钟  vs  9*25=225 分钟 → 八股占 64% 的时间
  // 八股每天 ≈ 120*0.637/5 ≈ 15.3 条；算法 ≈ 120*0.363/25 ≈ 1.7 条
  if (!(java.dailyTarget > algo.dailyTarget * 4)) {
    throw new Error(`八股日均应远高于算法：${java.dailyTarget.toFixed(1)} vs ${algo.dailyTarget.toFixed(1)}`)
  }
  // 算法至少得有 1 条以上，不能被抹成 0
  if (algo.dailyTarget < 1) {
    throw new Error(`算法日均不该小于 1 条，实际 ${algo.dailyTarget}`)
  }
})

test('没有分钟数配置时退回按条数均摊', () => {
  const docs = [docLib('java', 30, 'interview')]
  const g = goalMin(['java'], dayOffset(-1), dayOffset(29), 120)
  const mods = computeModuleProgress(g, docs, () => 0, undefined)
  // 30 天，30 条 → 每天 1 条
  assert.ok(Math.abs(mods[0].dailyTarget - 1) < 0.2)
})

test('每日分钟数越多，两个库的每日条数都随之上升', () => {
  const docs = [docLib('java', 200, 'interview'), docLib('algo-array', 100, 'algo')]
  const low = computeModuleProgress(
    goalMin(['java', 'algo-array'], dayOffset(-1), dayOffset(60), 60),
    docs,
    () => 0,
    { interview: 5, algo: 25 },
  )
  const high = computeModuleProgress(
    goalMin(['java', 'algo-array'], dayOffset(-1), dayOffset(60), 180),
    docs,
    () => 0,
    { interview: 5, algo: 25 },
  )
  const jl = low.find((m) => m.slug === 'java')!.dailyTarget
  const jh = high.find((m) => m.slug === 'java')!.dailyTarget
  const al = low.find((m) => m.slug === 'algo-array')!.dailyTarget
  const ah = high.find((m) => m.slug === 'algo-array')!.dailyTarget
  if (!(jh > jl && ah > al)) {
    throw new Error(`时间翻倍应让两库都加速：java ${jl.toFixed(1)}→${jh.toFixed(1)}, algo ${al.toFixed(1)}→${ah.toFixed(1)}`)
  }
})

// ---------- 时间窗下的分模块额度 ----------

test('有时间窗时，各库额度按自己那段时间算，而不是全天平摊', () => {
  const docs = [docLib('java', 79, 'interview'), docLib('algo-array', 9, 'algo')]
  const g = goalMin(['java', 'algo-array'], dayOffset(-1), dayOffset(30), 190)
  // 上午 100 分钟只给算法，下午 90 分钟给八股
  g.windows = [
    { name: 'morning', minutes: 100, libs: ['algo'], reviewOnly: false },
    { name: 'evening', minutes: 90, libs: ['interview'], reviewOnly: false },
  ]
  const mods = computeModuleProgress(g, docs, () => 0, { interview: 5, algo: 25 })

  const java = mods.find((m) => m.slug === 'java')!
  const algo = mods.find((m) => m.slug === 'algo-array')!

  // 算法：100 / 25 = 4 条/天
  if (Math.abs(algo.dailyTarget - 4) > 0.01) {
    throw new Error(`算法应每天 4 条，实际 ${algo.dailyTarget}`)
  }
  // 八股：90 / 5 = 18 条/天
  if (Math.abs(java.dailyTarget - 18) > 0.01) {
    throw new Error(`八股应每天 18 条，实际 ${java.dailyTarget}`)
  }
})

test('没有时间窗时退回按工作量分摊（行为不变）', () => {
  const docs = [docLib('java', 79, 'interview'), docLib('algo-array', 9, 'algo')]
  const g = goalMin(['java', 'algo-array'], dayOffset(-1), dayOffset(30), 120)
  g.windows = []
  const mods = computeModuleProgress(g, docs, () => 0, { interview: 5, algo: 25 })
  const java = mods.find((m) => m.slug === 'java')!
  const algo = mods.find((m) => m.slug === 'algo-array')!
  // 八股权重 395、算法 225，总 620 → 八股 76.5 分钟/天 → 15.3 条
  if (!(java.dailyTarget > algo.dailyTarget)) {
    throw new Error('八股仍应快于算法')
  }
})

test('时间窗里某库没被任何窗包含时，额度为 0（不该瞎分）', () => {
  const docs = [docLib('java', 79, 'interview'), docLib('algo-array', 9, 'algo')]
  const g = goalMin(['java', 'algo-array'], dayOffset(-1), dayOffset(30), 90)
  // 只有八股有窗，算法没分配时间
  g.windows = [
    { name: 'evening', minutes: 90, libs: ['interview'], reviewOnly: false },
  ]
  const mods = computeModuleProgress(g, docs, () => 0, { interview: 5, algo: 25 })
  const algo = mods.find((m) => m.slug === 'algo-array')!
  if (algo.dailyTarget !== 0) {
    throw new Error(`算法没有时间窗，额度应为 0，实际 ${algo.dailyTarget}`)
  }
})
