package plan

import (
	"testing"
)

func baseInput() Input {
	return Input{
		StartDay:     "2026-03-01",
		DueDay:       "2026-04-30",
		DailyMinutes: 120,
		Libs: []LibInput{
			{Library: "interview", Remaining: 400, MinutesPerItem: 5, ReviewCap: 40},
			{Library: "algo", Remaining: 200, MinutesPerItem: 25, ReviewCap: 20},
		},
	}
}

// 算法和八股的单价必须分开算：同样 120 分钟，
// 八股能过 24 条，算法只能啃 4 条。混在一起用同一个速度是错的。
func TestLibsHaveSeparatePace(t *testing.T) {
	in := baseInput()
	in.Libs = []LibInput{
		{Library: "interview", Remaining: 1000, MinutesPerItem: 5, ReviewCap: 40},
		{Library: "algo", Remaining: 1000, MinutesPerItem: 25, ReviewCap: 40},
	}
	res := Simulate(in)
	if len(res.Days) == 0 {
		t.Fatal("应排出天数")
	}
	first := res.Days[0]
	if first.Rest {
		t.Fatal("第一天不该是休息日")
	}
	if first.New["interview"] <= first.New["algo"] {
		t.Fatalf("同样时间下八股应学得比算法多：interview=%d algo=%d",
			first.New["interview"], first.New["algo"])
	}
	// 比例应接近 5:1（单价 5 分钟 vs 25 分钟）
	ratio := float64(first.New["interview"]) / float64(first.New["algo"])
	if ratio < 3 || ratio > 8 {
		t.Fatalf("速度比应接近 5:1，实际 %.1f", ratio)
	}
}

// 关键性质：先紧后松。后期复习压力上来，新学量必须自动下降。
func TestFrontLoadedThenTapering(t *testing.T) {
	in := baseInput()
	res := Simulate(in)

	if len(res.Days) < 20 {
		t.Fatalf("应有足够长的计划，实际 %d 天", len(res.Days))
	}
	// 取前 5 个学习日与最后 5 个学习日的新学量对比
	var studyDays []DayPlan
	for _, d := range res.Days {
		if !d.Rest && d.MinutesUsed > 0 {
			studyDays = append(studyDays, d)
		}
	}
	if len(studyDays) < 12 {
		t.Fatalf("学习日太少，无法判断曲线: %d", len(studyDays))
	}
	sumNew := func(ds []DayPlan) int {
		s := 0
		for _, d := range ds {
			for _, v := range d.New {
				s += v
			}
		}
		return s
	}
	head := sumNew(studyDays[:5])
	tail := sumNew(studyDays[len(studyDays)-5:])
	if tail >= head {
		t.Fatalf("后期新学量应少于前期（复习挤压）：前 5 天 %d 条 -> 后 5 天 %d 条", head, tail)
	}
	// 后期复习占比应显著高于前期
	reviewShare := func(ds []DayPlan) float64 {
		r, t := 0, 0
		for _, d := range ds {
			r += d.ReviewMinutes
			t += d.MinutesUsed
		}
		if t == 0 {
			return 0
		}
		return float64(r) / float64(t)
	}
	headShare := reviewShare(studyDays[:5])
	tailShare := reviewShare(studyDays[len(studyDays)-5:])
	if tailShare <= headShare {
		t.Fatalf("后期复习占比应更高：前 %.2f -> 后 %.2f", headShare, tailShare)
	}
}

// 每天用时不得超过设定上限（复习债也得在上限内还）。
func TestDailyMinutesRespected(t *testing.T) {
	in := baseInput()
	in.DailyMinutes = 90
	res := Simulate(in)
	for _, d := range res.Days {
		if d.MinutesUsed > in.DailyMinutes {
			t.Fatalf("%s 用了 %d 分钟，超过上限 %d", d.Day, d.MinutesUsed, in.DailyMinutes)
		}
	}
}

// 时间越多，学得越快、完成得越早。
func TestMoreMinutesFinishesEarlier(t *testing.T) {
	slow := baseInput()
	slow.DailyMinutes = 60
	fast := baseInput()
	fast.DailyMinutes = 180

	s := Simulate(slow)
	f := Simulate(fast)
	if !s.Feasible || !f.Feasible {
		t.Skip("参数下不可行，跳过对比")
	}
	if !(f.FinishDay < s.FinishDay) {
		t.Fatalf("每天 180 分钟应比 60 分钟更早完成：%s vs %s", f.FinishDay, s.FinishDay)
	}
}

// 停滞日必须真的被跳过，且当天不学不复习。
func TestRestDaysAreSkipped(t *testing.T) {
	in := baseInput()
	in.RestDays = []string{"2026-03-03", "2026-03-04"}
	res := Simulate(in)

	found := 0
	for _, d := range res.Days {
		if d.Day == "2026-03-03" || d.Day == "2026-03-04" {
			found++
			if !d.Rest {
				t.Fatalf("%s 应标记为休息日", d.Day)
			}
			if d.NewMinutes != 0 || d.ReviewMinutes != 0 {
				t.Fatalf("%s 休息日不该有任务: new=%d review=%d", d.Day, d.NewMinutes, d.ReviewMinutes)
			}
		}
	}
	if found != 2 {
		t.Fatalf("应找到 2 个休息日，实际 %d", found)
	}
}

// 每周固定休息日也要生效（比如周日不学）。
func TestWeeklyRestDay(t *testing.T) {
	in := baseInput()
	in.WeeklyRest = []int{0} // 周日
	res := Simulate(in)
	for _, d := range res.Days {
		if weekDay(d.Day) == 0 && !d.Rest {
			t.Fatalf("%s 是周日，应休息", d.Day)
		}
	}
	if res.RestCount == 0 {
		t.Fatal("应有休息日")
	}
}

// 加了停滞日，完成时间必然不早于原计划 —— 因为少了好几天可用时间。
func TestRestDaysPushFinishLater(t *testing.T) {
	plain := baseInput()
	plain.DailyMinutes = 90
	p := Simulate(plain)

	withRest := baseInput()
	withRest.DailyMinutes = 90
	withRest.RestDays = []string{"2026-03-05", "2026-03-06", "2026-03-07", "2026-03-08"}
	r := Simulate(withRest)

	if !p.Feasible || !r.Feasible {
		t.Skip("不可行，跳过")
	}
	if r.FinishDay < p.FinishDay {
		t.Fatalf("加了停滞日反而更早完成不合理：%s -> %s", p.FinishDay, r.FinishDay)
	}
}

// 目标过激时必须判为不可行，并给出「需要多少分钟」。
func TestInfeasibleGoalDetected(t *testing.T) {
	in := baseInput()
	in.DueDay = "2026-03-10" // 40 天要啃 600 条，明显不可能
	in.DailyMinutes = 60

	res := Simulate(in)
	if res.Feasible {
		t.Fatalf("这个目标应判为不可行，实际学掉了 %d/%d", res.Learned, res.TotalNew)
	}

	rec := Recommend(in)
	if rec.Feasible {
		t.Fatal("Recommend 也应判为不可行")
	}
	if rec.RequiredDailyMinutes <= in.DailyMinutes {
		t.Fatalf("应算出更高的每日投入，实际 %d", rec.RequiredDailyMinutes)
	}
	if rec.RequiredDailyMinutes > maxMinutes {
		t.Fatalf("所需分钟数不该超过搜索上限，实际 %d", rec.RequiredDailyMinutes)
	}
	if rec.SuggestedDailyMinutes <= 0 {
		t.Fatal("应给出建议的每日分钟数")
	}
	if rec.SuggestedDueDay == "" {
		t.Fatal("应给出建议的截止日")
	}
}

// 宽松目标应判为可行，且建议值就是用户自己的设置。
func TestFeasibleGoalKeepsSettings(t *testing.T) {
	in := baseInput()
	in.DueDay = "2026-09-30"
	in.DailyMinutes = 120

	rec := Recommend(in)
	if !rec.Feasible {
		t.Fatal("宽松目标应可行")
	}
	if rec.Verdict != "ok" {
		t.Fatalf("结论应为 ok，实际 %s", rec.Verdict)
	}
	if rec.SuggestedDailyMinutes != 120 {
		t.Fatalf("可行时不该改每日时间，实际 %d", rec.SuggestedDailyMinutes)
	}
	if rec.SuggestedDueDay != in.DueDay {
		t.Fatalf("可行时不该改截止日，实际 %s", rec.SuggestedDueDay)
	}
}

// 建议方案本身必须可行 —— 否则建议毫无意义。
func TestSuggestionIsItselfFeasible(t *testing.T) {
	in := baseInput()
	in.DueDay = "2026-03-20"
	in.DailyMinutes = 45

	rec := Recommend(in)
	try := in
	try.DailyMinutes = rec.SuggestedDailyMinutes
	try.DueDay = rec.SuggestedDueDay
	if !Simulate(try).Feasible {
		t.Fatalf("建议方案自己不可行：每天 %d 分钟、截止 %s",
			rec.SuggestedDailyMinutes, rec.SuggestedDueDay)
	}
}

// 需要的时间越多，算出的「最少每日分钟」应单调不减。
func TestRequiredMinutesMonotonic(t *testing.T) {
	loose := baseInput()
	loose.DueDay = "2026-06-30"
	tight := baseInput()
	tight.DueDay = "2026-04-15"
	tightest := baseInput()
	tightest.DueDay = "2026-03-25"

	a := requiredMinutes(loose)
	b := requiredMinutes(tight)
	c := requiredMinutes(tightest)
	if !(a <= b && b <= c) {
		t.Fatalf("截止日越紧所需时间应不减少：%d / %d / %d", a, b, c)
	}
}

// 已有的复习债必须算进计划，否则会严重低估后期负担。
func TestPendingReviewLoadConsidered(t *testing.T) {
	plain := baseInput()
	plain.DueDay = "2026-05-30"
	plain.DailyMinutes = 120
	p := Simulate(plain)

	withDebt := plain
	for i := 0; i < 300; i++ {
		withDebt.Pending = append(withDebt.Pending, Pending{
			Library: "interview",
			DueDay:  addDays(plain.StartDay, i%20),
			Stage:   i % 3,
		})
	}
	d := Simulate(withDebt)

	if d.ReviewMinutesTotal <= p.ReviewMinutesTotal {
		t.Fatalf("有复习债时应产生更多复习时间：%d vs %d",
			d.ReviewMinutesTotal, p.ReviewMinutesTotal)
	}
	if d.Learned >= p.Learned {
		t.Fatalf("复习债会挤占新学时间，学得应更少：%d vs %d", d.Learned, p.Learned)
	}
}

// 全部学完后剩余天数不该继续产生新任务。
func TestStopsWhenDone(t *testing.T) {
	in := Input{
		StartDay:     "2026-03-01",
		DueDay:       "2026-06-30",
		DailyMinutes: 200,
		Libs:         []LibInput{{Library: "interview", Remaining: 10, MinutesPerItem: 5}},
	}
	res := Simulate(in)
	if !res.Feasible {
		t.Fatal("10 条应可行")
	}
	if res.FinishDay == "" {
		t.Fatal("应记录完成日")
	}
	// 完成日之后仍有天数（复习债要还），但不该再有新学
	for _, d := range res.Days {
		if d.Day > res.FinishDay {
			for lib, n := range d.New {
				if n > 0 {
					t.Fatalf("%s 已完成，不该再学新内容（%s %d 条）", res.FinishDay, lib, n)
				}
			}
		}
	}
}

// 每天预算小于单条成本时不该死循环，也不该硬塞。
func TestTinyBudgetNoHang(t *testing.T) {
	in := Input{
		StartDay:     "2026-03-01",
		DueDay:       "2026-03-20",
		DailyMinutes: 10,
		Libs:         []LibInput{{Library: "algo", Remaining: 50, MinutesPerItem: 25}},
	}
	res := Simulate(in)
	if res.Feasible {
		t.Fatal("每天 10 分钟学不了 25 分钟一条的算法，应不可行")
	}
	for _, d := range res.Days {
		if d.MinutesUsed > 10 {
			t.Fatalf("%s 超预算: %d", d.Day, d.MinutesUsed)
		}
	}
}

// 分配结果不能超过各库的剩余量。
func TestAllocateRespectsRemaining(t *testing.T) {
	libs := map[string]LibInput{
		"a": {Library: "a", MinutesPerItem: 5, Remaining: 3},
		"b": {Library: "b", MinutesPerItem: 5, Remaining: 100},
	}
	remaining := map[string]int{"a": 3, "b": 100}
	out := allocate(600, []string{"a", "b"}, remaining, libs)
	if out["a"] > 3 {
		t.Fatalf("不该超过剩余量: a=%d", out["a"])
	}
}

// 空输入不应 panic。
func TestEmptyInput(t *testing.T) {
	res := Simulate(Input{})
	if res.Feasible {
		t.Fatal("没有任务时不该报可行")
	}
	if res.TotalNew != 0 {
		t.Fatalf("总条数应为 0，实际 %d", res.TotalNew)
	}
}
