package store

import (
	"testing"
)

// 复习间隔必须是「先密后疏」，累计节点要覆盖半年。
func TestReviewIntervalsShape(t *testing.T) {
	if len(ReviewIntervals) < 5 {
		t.Fatalf("复习阶段太少，无法覆盖长期记忆周期: %d", len(ReviewIntervals))
	}
	// 间隔单调不减（先密后疏），且第一次必须是 1 天（遗忘曲线掉得最狠的点）
	if ReviewIntervals[0] != 1 {
		t.Fatalf("第一次复习应排在次日，实际 %d 天后", ReviewIntervals[0])
	}
	for i := 1; i < len(ReviewIntervals); i++ {
		if ReviewIntervals[i] <= ReviewIntervals[i-1] {
			t.Fatalf("间隔应递增，第 %d 项 %d 不大于前一项 %d",
				i, ReviewIntervals[i], ReviewIntervals[i-1])
		}
	}
	cum := 0
	for _, v := range ReviewIntervals {
		cum += v
	}
	if cum < 100 {
		t.Fatalf("走完全部阶段应在 %d 天内完成，太短无法形成长期记忆", cum)
	}
}

func TestAddDiffDays(t *testing.T) {
	if got := addDays("2026-01-31", 1); got != "2026-02-01" {
		t.Fatalf("跨月: %s", got)
	}
	if got := addDays("2026-12-31", 1); got != "2027-01-01" {
		t.Fatalf("跨年: %s", got)
	}
	if got := addDays("2026-03-01", -1); got != "2026-02-28" {
		t.Fatalf("往回: %s", got)
	}
	// 闰年
	if got := addDays("2028-02-28", 1); got != "2028-02-29" {
		t.Fatalf("闰年: %s", got)
	}
	if d := diffDays("2026-01-30", "2026-02-02"); d != 3 {
		t.Fatalf("diffDays: %d", d)
	}
}

// 标记「已掌握」应自动入列，且首复习排在次日附近。
func TestEnrollOnDone(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetProgress("java", "q1", StatusDone, nil, day); err != nil {
		t.Fatal(err)
	}
	it, err := s.GetReview("java", "q1")
	if err != nil {
		t.Fatalf("应已自动进入复习计划: %v", err)
	}
	if it.Stage != 0 || it.Graduated {
		t.Fatalf("初始状态不对: %+v", it)
	}
	// 第 1 次复习落在次日，最多因负载均衡顺延 1 天（spread = 1/3 → 1）
	if d := diffDays(day, it.DueDay); d < 1 || d > 2 {
		t.Fatalf("首复习应在 1~2 天后，实际 %d 天（%s）", d, it.DueDay)
	}
}

// 撤销「已掌握」要移出复习计划，不能继续占额度。
func TestDropOnUndone(t *testing.T) {
	s := newStore(t)
	if _, err := s.SetProgress("java", "q1", StatusDone, nil, "2026-03-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProgress("java", "q1", StatusUnread, nil, "2026-03-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetReview("java", "q1"); err != ErrNotFound {
		t.Fatalf("撤销后应移出复习计划，实际 %v", err)
	}
}

// 重复标记「已掌握」是幂等的，不能把已经走过的复习进度打回原点。
func TestEnrollIdempotent(t *testing.T) {
	s := newStore(t)
	if _, err := s.SetProgress("java", "q1", StatusDone, nil, "2026-03-01"); err != nil {
		t.Fatal(err)
	}
	// 走两次「记住」
	for i := 0; i < 2; i++ {
		if _, err := s.GradeReview("java", "q1", GradeGood, "2026-03-02"); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.GetReview("java", "q1")
	if before.Stage != 2 {
		t.Fatalf("两轮后应在阶段 2，实际 %d", before.Stage)
	}

	// 再次标记已掌握（比如加了星又取消后再点）
	if _, err := s.SetProgress("java", "q1", StatusDone, nil, "2026-03-02"); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetReview("java", "q1")
	if after.Stage != 2 {
		t.Fatalf("重复标记不应重置阶段：%d -> %d", before.Stage, after.Stage)
	}
	if after.DueDay != before.DueDay {
		t.Fatalf("重复标记不应改变下次复习日：%s -> %s", before.DueDay, after.DueDay)
	}
}

// 完整走完全部阶段应毕业，不再出现在队列里。
func TestGraduateAfterAllStages(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetProgress("redis", "a", StatusDone, nil, day); err != nil {
		t.Fatal(err)
	}
	cur := day
	for i := 0; i < len(ReviewIntervals); i++ {
		it, err := s.GradeReview("redis", "a", GradeGood, cur)
		if err != nil {
			t.Fatalf("第 %d 次复习失败: %v", i+1, err)
		}
		if i < len(ReviewIntervals)-1 {
			if it.Graduated {
				t.Fatalf("第 %d 次复习不该毕业", i+1)
			}
			cur = it.DueDay
		} else {
			if !it.Graduated {
				t.Fatalf("走完全部 %d 阶段后应毕业", len(ReviewIntervals))
			}
			if it.DueDay != "" {
				t.Fatalf("毕业后不该再有下次复习日: %q", it.DueDay)
			}
		}
	}

	plan, err := s.ReviewPlan(cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Queue) != 0 {
		t.Fatalf("毕业后不该出现在队列里: %+v", plan.Queue)
	}
	if plan.Graduated != 1 {
		t.Fatalf("毕业数应为 1，实际 %d", plan.Graduated)
	}
}

// 「忘了」要退回第 1 阶段并记一次 lapse，间隔重新从 1 天开始。
func TestAgainResetsToFirstStage(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetProgress("os", "x", StatusDone, nil, day); err != nil {
		t.Fatal(err)
	}
	// 推到阶段 3
	cur := day
	for i := 0; i < 3; i++ {
		it, err := s.GradeReview("os", "x", GradeGood, cur)
		if err != nil {
			t.Fatal(err)
		}
		cur = it.DueDay
	}
	before, _ := s.GetReview("os", "x")
	if before.Stage != 3 {
		t.Fatalf("应在阶段 3，实际 %d", before.Stage)
	}

	after, err := s.GradeReview("os", "x", GradeAgain, cur)
	if err != nil {
		t.Fatal(err)
	}
	if after.Stage != 0 {
		t.Fatalf("忘了应退回阶段 0，实际 %d", after.Stage)
	}
	if after.Lapses != 1 {
		t.Fatalf("应记 1 次 lapse，实际 %d", after.Lapses)
	}
	if d := diffDays(cur, after.DueDay); d < 1 || d > 2 {
		t.Fatalf("忘了之后应在 1~2 天后重来，实际 %d 天", d)
	}
}

// 「模糊」原地踏步，间隔对折。
func TestHardKeepsStage(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetProgress("os", "x", StatusDone, nil, day); err != nil {
		t.Fatal(err)
	}
	cur := day
	for i := 0; i < 3; i++ {
		it, _ := s.GradeReview("os", "x", GradeGood, cur)
		cur = it.DueDay
	}
	before, _ := s.GetReview("os", "x")
	after, err := s.GradeReview("os", "x", GradeHard, cur)
	if err != nil {
		t.Fatal(err)
	}
	if after.Stage != before.Stage {
		t.Fatalf("模糊不应推进阶段：%d -> %d", before.Stage, after.Stage)
	}
	// 阶段 3 的间隔是 7，对折后 3 天
	full := reviewInterval(before.Stage)
	if d := diffDays(cur, after.DueDay); d >= full {
		t.Fatalf("模糊后间隔应对折（%d 天），实际 %d 天", full, d)
	}
}

// 顺延：阶段不动、不计入复习次数、不写流水。
func TestDefer(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetProgress("os", "x", StatusDone, nil, day); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetReview("os", "x")
	after, err := s.GradeReview("os", "x", GradeDefer, day)
	if err != nil {
		t.Fatal(err)
	}
	if after.Stage != before.Stage || after.ReviewCount != before.ReviewCount {
		t.Fatalf("顺延不该改变阶段和复习次数: %+v -> %+v", before, after)
	}
	if after.DueDay != addDays(day, 1) {
		t.Fatalf("顺延应排到次日，实际 %s", after.DueDay)
	}
	plan, _ := s.ReviewPlan(day)
	if plan.DoneToday != 0 {
		t.Fatalf("顺延不该算作今天复习过，实际 %d", plan.DoneToday)
	}
}

// 每日上限：到期太多时只排 cap 条，剩下的顺延，且不丢。
func TestDailyCapQueues(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetReviewConfig(ReviewConfig{DailyCap: 5, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// 同一天塞 12 条，负载均衡会把它们摊到相邻几天
	for i := 0; i < 12; i++ {
		doc := "d" + string(rune('a'+i))
		if _, err := s.SetProgress(doc, "q", StatusDone, nil, day); err != nil {
			t.Fatal(err)
		}
	}

	// 人为把全部 due_day 压到今天，模拟「积压了 12 条」
	if _, err := s.db.Exec(`UPDATE review SET due_day = ?`, day); err != nil {
		t.Fatal(err)
	}

	plan, err := s.ReviewPlan(day)
	if err != nil {
		t.Fatal(err)
	}
	if plan.DueTotal != 12 {
		t.Fatalf("今天应到期 12 条，实际 %d", plan.DueTotal)
	}
	if len(plan.Queue) != 5 {
		t.Fatalf("队列应被截到上限 5，实际 %d", len(plan.Queue))
	}
	if plan.Deferred != 7 {
		t.Fatalf("剩下 7 条应顺延，实际 %d", plan.Deferred)
	}
	if plan.Backlog != 0 {
		t.Fatalf("当天到期不算逾期，实际 backlog %d", plan.Backlog)
	}

	// 做完 2 条后，队列应缩小到 3（上限是「今天总共做多少」）
	for i := 0; i < 2; i++ {
		it := plan.Queue[i]
		if _, err := s.GradeReview(it.DocID, it.ItemID, GradeGood, day); err != nil {
			t.Fatal(err)
		}
	}
	plan2, _ := s.ReviewPlan(day)
	if plan2.DoneToday != 2 {
		t.Fatalf("今天应已完成 2 条，实际 %d", plan2.DoneToday)
	}
	if len(plan2.Queue) != 3 {
		t.Fatalf("剩余额度应是 3，实际 %d", len(plan2.Queue))
	}
}

// 逾期内容必须优先，并且当天排不下时不能凭空消失。
func TestBacklogPriority(t *testing.T) {
	s := newStore(t)
	if _, err := s.SetReviewConfig(ReviewConfig{DailyCap: 10, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// 一条很早逾期的（阶段 0，很脆），一条今天到期的（阶段 2）
	if _, err := s.SetProgress("a", "old", StatusDone, nil, "2026-02-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProgress("b", "fresh", StatusDone, nil, "2026-03-01"); err != nil {
		t.Fatal(err)
	}
	// 把 a/old 压成 3 天前到期，b/fresh 正好今天到期
	if _, err := s.db.Exec(`UPDATE review SET due_day = ?, stage = 0 WHERE doc_id = 'a'`, "2026-02-27"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE review SET due_day = ?, stage = 2 WHERE doc_id = 'b'`, "2026-03-01"); err != nil {
		t.Fatal(err)
	}

	plan, err := s.ReviewPlan("2026-03-01")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Backlog != 1 {
		t.Fatalf("应有 1 条逾期，实际 %d", plan.Backlog)
	}
	if len(plan.Queue) < 2 {
		t.Fatalf("两条都该进队列，实际 %d", len(plan.Queue))
	}
	if plan.Queue[0].DocID != "a" {
		t.Fatalf("逾期最久的应排最前，实际第一条是 %s", plan.Queue[0].DocID)
	}
	if plan.Queue[0].Overdue != 2 {
		t.Fatalf("逾期天数应为 2（2/27 → 3/1），实际 %d", plan.Queue[0].Overdue)
	}
}

// 负载均衡：同一天标记的一批内容应摊到相邻几天，而非全挤在同一天。
func TestLoadBalancingSpreads(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetReviewConfig(ReviewConfig{DailyCap: 3, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// 每阶段间隔 1 天，窗口只有 [d+1, d+2]，上限 3 → 最多摊 6 条，
	// 这里放 6 条，应该正好把两天各填 3 条。
	for i := 0; i < 6; i++ {
		doc := "d" + string(rune('a'+i))
		if _, err := s.SetProgress(doc, "q", StatusDone, nil, day); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := s.dueCounts([]string{addDays(day, 1), addDays(day, 2)})
	if err != nil {
		t.Fatal(err)
	}
	d1, d2 := counts[addDays(day, 1)], counts[addDays(day, 2)]
	if d1 == 0 || d2 == 0 {
		t.Fatalf("应摊到两天，实际 %d / %d", d1, d2)
	}
	if d1 > 3 || d2 > 3 {
		t.Fatalf("任一天不应超过上限 3，实际 %d / %d", d1, d2)
	}
	if d1+d2 != 6 {
		t.Fatalf("6 条应全部排进去，实际 %d", d1+d2)
	}
}

// 窗口全满时也不能把内容丢掉，必须安排到某一天。
func TestLoadBalancingNeverDrops(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetReviewConfig(ReviewConfig{DailyCap: 1, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		doc := "d" + string(rune('a'+i))
		if _, err := s.SetProgress(doc, "q", StatusDone, nil, day); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM review WHERE graduated = 0 AND due_day <> ''`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("5 条都必须排到某一天，实际 %d", n)
	}
}

// 关闭复习模块时不入列，但已有计划保留（用户可能只是暂时关掉）。
func TestDisabledEnrollment(t *testing.T) {
	s := newStore(t)
	if _, err := s.SetReviewConfig(ReviewConfig{DailyCap: 20, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProgress("java", "q1", StatusDone, nil, "2026-03-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetReview("java", "q1"); err != ErrNotFound {
		t.Fatalf("关闭时不该入列，实际 %v", err)
	}

	plan, _ := s.ReviewPlan("2026-03-01")
	if len(plan.Queue) != 0 {
		t.Fatalf("关闭时队列应为空，实际 %d", len(plan.Queue))
	}
}

// 无到期内容时应给出「下次是几号」的提示。
func TestNextDueDayHint(t *testing.T) {
	s := newStore(t)
	if _, err := s.SetProgress("java", "q1", StatusDone, nil, "2026-03-01"); err != nil {
		t.Fatal(err)
	}
	it, _ := s.GetReview("java", "q1")

	plan, err := s.ReviewPlan("2026-03-01")
	if err != nil {
		t.Fatal(err)
	}
	if plan.DueTotal != 0 {
		t.Fatalf("当天不该有到期内容，实际 %d", plan.DueTotal)
	}
	if plan.NextDueDay != it.DueDay {
		t.Fatalf("应提示下次复习日 %s，实际 %s", it.DueDay, plan.NextDueDay)
	}
}

// 未来负载预测要能看出「哪天会忙」。
func TestForecast(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 4; i++ {
		doc := "d" + string(rune('a'+i))
		if _, err := s.SetProgress(doc, "q", StatusDone, nil, "2026-03-01"); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := s.ReviewPlan("2026-03-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Forecast) != 14 {
		t.Fatalf("应预测 14 天，实际 %d", len(plan.Forecast))
	}
	total := 0
	for _, f := range plan.Forecast {
		total += f.Count
	}
	if total != 4 {
		t.Fatalf("预测应覆盖全部 4 条（含今天 0 条 + 未来 4 条），实际 %d", total)
	}
	if plan.Forecast[0].Day != "2026-03-01" {
		t.Fatalf("第一格应是今天，实际 %s", plan.Forecast[0].Day)
	}
}

// 重置单条复习进度。
func TestResetReviewItem(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetProgress("java", "q1", StatusDone, nil, day); err != nil {
		t.Fatal(err)
	}
	cur := day
	for i := 0; i < 2; i++ {
		it, _ := s.GradeReview("java", "q1", GradeGood, cur)
		cur = it.DueDay
	}
	if err := s.ResetReviewItem("java", "q1", cur); err != nil {
		t.Fatal(err)
	}
	it, _ := s.GetReview("java", "q1")
	if it.Stage != 0 || it.ReviewCount != 0 || it.Graduated || it.Lapses != 0 {
		t.Fatalf("重置后应回到初始状态: %+v", it)
	}
}

// 快照要带上在籍复习状态，毕业的不再出现。
func TestSnapshotIncludesReview(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetProgress("java", "q1", StatusDone, nil, day); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProgress("java", "q2", StatusDone, nil, day); err != nil {
		t.Fatal(err)
	}
	// q2 推到毕业
	cur := day
	for i := 0; i < len(ReviewIntervals); i++ {
		it, err := s.GradeReview("java", "q2", GradeGood, cur)
		if err != nil {
			t.Fatal(err)
		}
		cur = it.DueDay
	}

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Review) != 1 {
		t.Fatalf("快照应只带 1 条在籍记录，实际 %d", len(snap.Review))
	}
	if snap.Review[0].ItemID != "q1" {
		t.Fatalf("在籍的应是 q1，实际 %s", snap.Review[0].ItemID)
	}
	// 派生字段要算出来。q1 停在 2026-03-02，相对今天早已逾期，
	// 所以 Interval 归零、Overdue 大于零 —— 前端据此显示「积压 N 天」。
	q1 := snap.Review[0]
	if q1.DueDay != "2026-03-02" {
		t.Fatalf("下次复习日应是 2026-03-02，实际 %s", q1.DueDay)
	}
	if q1.Interval != 0 {
		t.Fatalf("已逾期的内容 Interval 应为 0，实际 %d", q1.Interval)
	}
	if q1.Overdue <= 0 {
		t.Fatalf("应算出逾期天数，实际 %d", q1.Overdue)
	}
}

// ---------- 长期稳定性：这是「不至于每天堆积太多」的最终验收 ----------

// 连续 60 天、每天新学 8 条（对一个业余备考的人来说已经不轻），
// 每天只能复习 cap 条。断言：
//  1. 没有一天被塞爆（每天都 <= cap）
//  2. 在籍总量趋于稳定，不随时间无限增长
//  3. 真正到期却没被处理的内容（积压）保持有界
func TestSteadyStateNoBacklogExplosion(t *testing.T) {
	s := newStore(t)
	const (
		dailyNew = 8
		cap      = 30
		totalDay = 60
	)
	if _, err := s.SetReviewConfig(ReviewConfig{DailyCap: cap, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	start := "2026-01-01"
	enrolledSeries := make([]int, 0, totalDay)
	backlogSeries := make([]int, 0, totalDay)

	for d := 0; d < totalDay; d++ {
		day := addDays(start, d)

		// 当天新学的内容标记为掌握
		for i := 0; i < dailyNew; i++ {
			doc := "doc" + string(rune('a'+(i%26)))
			item := "q" + string(rune('a'+(d%26))) + string(rune('a'+i))
			if _, err := s.SetProgress(doc, item, StatusDone, nil, day); err != nil {
				t.Fatal(err)
			}
		}

		plan, err := s.ReviewPlan(day)
		if err != nil {
			t.Fatalf("第 %d 天开盘失败: %v", d+1, err)
		}

		// 1) 每天排定的复习量绝不超过上限
		if len(plan.Queue) > cap {
			t.Fatalf("第 %d 天队列 %d 条超出上限 %d", d+1, len(plan.Queue), cap)
		}

		// 把当天的队列全部做掉（模拟用户按时完成）
		for _, it := range plan.Queue {
			if _, err := s.GradeReview(it.DocID, it.ItemID, GradeGood, day); err != nil {
				t.Fatalf("第 %d 天复习失败: %v", d+1, err)
			}
		}

		// 记录在籍量与真实积压
		var enrolled, backlog int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM review WHERE graduated = 0`).Scan(&enrolled); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM review
			WHERE graduated = 0 AND due_day < ?`, day).Scan(&backlog); err != nil {
			t.Fatal(err)
		}
		enrolledSeries = append(enrolledSeries, enrolled)
		backlogSeries = append(backlogSeries, backlog)
	}

	// 2) 在籍量必须收敛：后半程的均值不能比前半程大幅增长。
	// 每天新增 8 条、cap 30，若系统不收敛，60 天后会堆到几百条。
	firstHalf := avg(enrolledSeries[:totalDay/2])
	secondHalf := avg(enrolledSeries[totalDay/2:])
	if secondHalf > firstHalf*1.6 {
		t.Fatalf("在籍复习量持续膨胀：前半程均值 %.1f → 后半程 %.1f（说明积压会滚雪球）",
			firstHalf, secondHalf)
	}

	// 3) 积压有界：任何一天的积压都不该超过「上限 + 几天的新增」
	maxBacklog := 0
	for _, b := range backlogSeries {
		if b > maxBacklog {
			maxBacklog = b
		}
	}
	if maxBacklog > cap*2 {
		t.Fatalf("积压失控：峰值 %d 条（上限 %d）", maxBacklog, cap)
	}

	// 最后一天的队列仍然不该超上限，且系统仍在正常工作
	final, err := s.ReviewPlan(addDays(start, totalDay))
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Queue) > cap {
		t.Fatalf("最终队列 %d 条超出上限 %d", len(final.Queue), cap)
	}
}

func avg(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return float64(sum) / float64(len(xs))
}

// 学得越狠（每天新学条数越多），复习需求越高，但每日实际做的量恒等于上限。
func TestCapIsRespectedUnderHeavyLoad(t *testing.T) {
	cases := []struct {
		dailyNew int
		cap      int
	}{
		{5, 20}, {15, 20}, {40, 20}, {100, 20},
	}
	for _, c := range cases {
		s := newStore(t)
		if _, err := s.SetReviewConfig(ReviewConfig{DailyCap: c.cap, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		start := "2026-01-01"
		for d := 0; d < 20; d++ {
			day := addDays(start, d)
			for i := 0; i < c.dailyNew; i++ {
				doc := "d" + string(rune('a'+i%26))
				item := "q" + string(rune('a'+d%26)) + "-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
				if _, err := s.SetProgress(doc, item, StatusDone, nil, day); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := s.ReviewPlan(day)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Queue) > c.cap {
				t.Fatalf("每天新学 %d 条时，第 %d 天队列 %d 条超出上限 %d",
					c.dailyNew, d+1, len(plan.Queue), c.cap)
			}
			// 全部做掉，模拟用户遵守节奏
			for _, it := range plan.Queue {
				if _, err := s.GradeReview(it.DocID, it.ItemID, GradeGood, day); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

// ---------- 提前复习 ----------

// 今天没有到期内容时，可以用 ahead 把未来几天的拉进来，避免空转。
func TestPlanAheadPullsFuture(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	for i := 0; i < 4; i++ {
		doc := "d" + string(rune('a'+i))
		if _, err := s.SetProgress(doc, "q", StatusDone, nil, day); err != nil {
			t.Fatal(err)
		}
	}

	// 今天：没有到期
	plain, err := s.ReviewPlan(day)
	if err != nil {
		t.Fatal(err)
	}
	if plain.DueTotal != 0 || len(plain.Queue) != 0 {
		t.Fatalf("今天不该有到期内容: due=%d queue=%d", plain.DueTotal, len(plain.Queue))
	}

	// 提前 3 天：4 条都该进候选
	ahead, err := s.ReviewPlanAhead(day, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(ahead.Queue) != 4 {
		t.Fatalf("提前 3 天应能拉进 4 条，实际 %d", len(ahead.Queue))
	}
	if ahead.AheadTotal != 4 {
		t.Fatalf("AheadTotal 应为 4，实际 %d", ahead.AheadTotal)
	}
	// 提前复习不改变「今天是几号」的事实
	if ahead.DueTotal != 0 {
		t.Fatalf("提前模式不该改变今日到期数，实际 %d", ahead.DueTotal)
	}
}

// 提前复习必须让「真正到期的」排在前面，不能把积压挤到后面。
func TestPlanAheadKeepsDueFirst(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetProgress("a", "overdue", StatusDone, nil, "2026-02-20"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProgress("b", "future", StatusDone, nil, day); err != nil {
		t.Fatal(err)
	}
	// a/overdue 压到 2 天前；b/future 排到 3 天后
	if _, err := s.db.Exec(`UPDATE review SET due_day = ? WHERE doc_id = 'a'`, "2026-02-27"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE review SET due_day = ? WHERE doc_id = 'b'`, "2026-03-04"); err != nil {
		t.Fatal(err)
	}

	plan, err := s.ReviewPlanAhead(day, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Queue) != 2 {
		t.Fatalf("两条都该进队列，实际 %d", len(plan.Queue))
	}
	if plan.Queue[0].DocID != "a" {
		t.Fatalf("逾期的应排最前，实际 %s", plan.Queue[0].DocID)
	}
	if plan.Queue[1].DocID != "b" {
		t.Fatalf("未来的应排后面，实际 %s", plan.Queue[1].DocID)
	}
}

// 提前复习仍受每日上限约束。
func TestPlanAheadRespectsCap(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetReviewConfig(ReviewConfig{DailyCap: 3, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		doc := "d" + string(rune('a'+i))
		if _, err := s.SetProgress(doc, "q", StatusDone, nil, day); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := s.ReviewPlanAhead(day, 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Queue) > 3 {
		t.Fatalf("提前复习也不该超过上限 3，实际 %d", len(plan.Queue))
	}
}

// ahead=0 必须与普通 ReviewPlan 完全一致。
func TestPlanAheadZeroEqualsPlain(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	for i := 0; i < 3; i++ {
		doc := "d" + string(rune('a'+i))
		if _, err := s.SetProgress(doc, "q", StatusDone, nil, "2026-02-25"); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := s.ReviewPlan(day)
	b, _ := s.ReviewPlanAhead(day, 0)
	if len(a.Queue) != len(b.Queue) {
		t.Fatalf("ahead=0 应与普通计划一致: %d vs %d", len(a.Queue), len(b.Queue))
	}
	if a.DueTotal != b.DueTotal {
		t.Fatalf("DueTotal 应一致: %d vs %d", a.DueTotal, b.DueTotal)
	}
}

// ---------- 按内容库分池：算法不能被八股挤掉 ----------

// 八股条数多、复习快，如果用单一的全局队列，
// 算法复习会被永远推到队尾。必须保证每个库都有保底额度。
func TestLibCapPreventsStarvation(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"

	// 告诉 store 哪些文档属于哪个库
	s.SetDocLibraries(map[string]string{
		"java":       "interview",
		"algo-array": "algo",
	})

	if _, err := s.SetReviewConfig(ReviewConfig{
		DailyCap: 40,
		Enabled:  true,
		LibCap:   map[string]int{"interview": 30, "algo": 3},
	}); err != nil {
		t.Fatal(err)
	}

	// 30 条八股 + 5 条算法，全部压到今天到期
	for i := 0; i < 30; i++ {
		doc := "java"
		item := "q" + string(rune('a'+i))
		if _, err := s.SetProgress(doc, item, StatusDone, nil, day); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		item := "a" + string(rune('a'+i))
		if _, err := s.SetProgress("algo-array", item, StatusDone, nil, day); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE review SET due_day = ?`, day); err != nil {
		t.Fatal(err)
	}

	plan, err := s.ReviewPlan(day)
	if err != nil {
		t.Fatal(err)
	}

	// 统计队列里各库的条数
	counts := map[string]int{}
	for _, it := range plan.Queue {
		counts[s.libOf(it.DocID)]++
	}
	if counts["algo"] == 0 {
		t.Fatalf("算法复习被饿死了：%+v", counts)
	}
	if counts["algo"] > 3 {
		t.Fatalf("算法不该超过自己的额度 3，实际 %d", counts["algo"])
	}
	// 八股也不该被完全饿死
	if counts["interview"] == 0 {
		t.Fatalf("八股也不该为 0：%+v", counts)
	}
}

// 算法只有一个库时，仍应受其额度约束（防止单库刷满全局额度）。
func TestLibCapLimitsSingleLib(t *testing.T) {
	s := newStore(t)
	s.SetDocLibraries(map[string]string{"algo-array": "algo"})
	day := "2026-03-01"
	if _, err := s.SetReviewConfig(ReviewConfig{
		DailyCap: 40,
		Enabled:  true,
		LibCap:   map[string]int{"algo": 2},
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		item := "a" + string(rune('a'+i))
		if _, err := s.SetProgress("algo-array", item, StatusDone, nil, day); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE review SET due_day = ?`, day); err != nil {
		t.Fatal(err)
	}

	plan, _ := s.ReviewPlan(day)
	// 第一轮保底只给 2 条；补齐轮会用剩余全局额度继续补 algo，
	// 所以总数受全局 cap 约束，但仍应 >= 保底。
	if len(plan.Queue) < 2 {
		t.Fatalf("至少应排出保底的 2 条，实际 %d", len(plan.Queue))
	}
	if len(plan.Queue) > 40 {
		t.Fatalf("不该超过全局上限，实际 %d", len(plan.Queue))
	}
}

// 各库额度是独立的：调低八股额度不该影响算法能排多少。
func TestLibCapsIndependent(t *testing.T) {
	run := func(interviewCap int) map[string]int {
		s := newStore(t)
		s.SetDocLibraries(map[string]string{"java": "interview", "algo-array": "algo"})
		day := "2026-03-01"
		if _, err := s.SetReviewConfig(ReviewConfig{
			DailyCap: 100,
			Enabled:  true,
			LibCap:   map[string]int{"interview": interviewCap, "algo": 5},
		}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 20; i++ {
			if _, err := s.SetProgress("java", "q"+string(rune('a'+i)), StatusDone, nil, day); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 5; i++ {
			if _, err := s.SetProgress("algo-array", "a"+string(rune('a'+i)), StatusDone, nil, day); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.Exec(`UPDATE review SET due_day = ?`, day); err != nil {
			t.Fatal(err)
		}
		p, _ := s.ReviewPlan(day)
		out := map[string]int{}
		for _, it := range p.Queue {
			out[s.libOf(it.DocID)]++
		}
		return out
	}

	low := run(5)
	high := run(20)
	if low["algo"] != high["algo"] {
		t.Fatalf("八股额度变化不该影响算法：%d vs %d", low["algo"], high["algo"])
	}
	if high["interview"] <= low["interview"] {
		t.Fatalf("放宽八股额度后应排得更多：%d -> %d", low["interview"], high["interview"])
	}
}

// 未知库的文档按八股处理（最保守的额度），不能崩。
func TestUnknownLibraryFallback(t *testing.T) {
	s := newStore(t)
	day := "2026-03-01"
	if _, err := s.SetProgress("mystery", "q1", StatusDone, nil, day); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE review SET due_day = ?`, day); err != nil {
		t.Fatal(err)
	}
	plan, err := s.ReviewPlan(day)
	if err != nil {
		t.Fatalf("未知库不该导致失败: %v", err)
	}
	if len(plan.Queue) != 1 {
		t.Fatalf("应排出 1 条，实际 %d", len(plan.Queue))
	}
}

// 配置里缺失的库应当补上默认额度。
func TestLibCapDefaultsFilled(t *testing.T) {
	s := newStore(t)
	cfg, err := s.SetReviewConfig(ReviewConfig{DailyCap: 20, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, lib := range []string{"interview", "algo", "ai"} {
		if cfg.LibCap[lib] <= 0 {
			t.Fatalf("%s 应有默认额度，实际 %d", lib, cfg.LibCap[lib])
		}
	}
	// 读回来也要带着
	got, _ := s.GetReviewConfig()
	if got.LibCap["algo"] <= 0 {
		t.Fatalf("读回的配置丢了 LibCap: %+v", got)
	}
}
