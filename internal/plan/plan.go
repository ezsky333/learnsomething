// Package plan 依据学习目标推算逐日计划。
//
// 核心是一句算术：**学习和复习争抢同一天的同一份时间**。
// 刚学的内容复习负担很轻，学得越多，后续复习需求越大，
// 所以「每天固定学 N 条」这种排法注定会崩 —— 第 30 天还在学 N 条，
// 同时背着前 29 天的复习，一定做不完。
//
// 正确的排法是「前紧后松」：趁着复习负担还没堆起来先多学，
// 复习压力上来后自动减少新学，最后几天可能完全不学新内容，只做复习。
// 本包用逐日模拟得出这条曲线，并判断目标是否现实、该给什么替代建议。
package plan

import (
	"sort"
	"time"
)

const dayLayout = "2006-01-02"

// maxHorizon 是模拟的最大天数，防止参数异常时死循环。
const maxHorizon = 1500

// reviewFactor 是「复习一条的成本 / 首次学习一条的成本」，按阶段递减：
// 第一次回头看几乎要从头回忆，最费劲；越往后越省。
var reviewFactor = []float64{0.6, 0.5, 0.45, 0.4, 0.35, 0.3, 0.3}

// fallbackIntervals 是调用方没给间隔时用的艾宾浩斯阶梯。
var fallbackIntervals = []int{1, 2, 4, 7, 15, 30, 60}

func factorFor(stage int) float64 {
	if stage < 0 {
		return reviewFactor[0]
	}
	if stage >= len(reviewFactor) {
		return reviewFactor[len(reviewFactor)-1]
	}
	return reviewFactor[stage]
}

func addDays(day string, n int) string {
	t, err := time.Parse(dayLayout, day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, n).Format(dayLayout)
}

// weekDay 返回 0=周日 … 6=周六。
func weekDay(day string) int {
	t, err := time.Parse(dayLayout, day)
	if err != nil {
		return -1
	}
	return int(t.Weekday())
}

// LibInput 是一个内容库的学习参数。
//
// 算法和八股必须分开给参数：一条算法长文要自己推一遍、写一遍，
// 一条八股题对本就走「见过、想过」的人来说几分钟就过。
// 用同一个「每天 N 条」去要求两者，结果一定是八股做不完、算法学不动。
type LibInput struct {
	Library        string `json:"library"`
	Remaining      int    `json:"remaining"`
	MinutesPerItem int    `json:"minutesPerItem"`
	ReviewCap      int    `json:"reviewCap"`
}

// Pending 是一条已进入复习计划、尚未到期复习的内容。
type Pending struct {
	Library string `json:"library"`
	DueDay  string `json:"dueDay"`
	Stage   int    `json:"stage"`
}

// Input 是推算所需的一切。
type Input struct {
	StartDay     string   `json:"startDay"`
	DueDay       string   `json:"dueDay"`
	DailyMinutes int      `json:"dailyMinutes"`
	RestDays     []string `json:"restDays"`
	WeeklyRest   []int    `json:"weeklyRest"`
	Libs         []LibInput `json:"libs"`
	Pending      []Pending  `json:"-"`
	Intervals    []int      `json:"-"`
}

// DayPlan 是计划里的一天。
type DayPlan struct {
	Day     string         `json:"day"`
	Rest    bool           `json:"rest"`
	New     map[string]int `json:"new"`
	Review  map[string]int `json:"review"`
	// NewMinutes / ReviewMinutes 分开报，才能解释「今天为什么只能学这么多」。
	NewMinutes    int            `json:"newMinutes"`
	ReviewMinutes int            `json:"reviewMinutes"`
	MinutesUsed   int            `json:"minutesUsed"`
	Remaining     map[string]int `json:"remaining"`
}

// Result 是一次推算的结果。
type Result struct {
	Days []DayPlan `json:"days"`
	// Feasible 表示按当前设置能否在 dueDay 前学完。
	Feasible bool `json:"feasible"`
	// FinishDay 是实际学完的那天（可行时 <= dueDay）。
	FinishDay string `json:"finishDay"`
	// TotalNew 是总共要新学的条数，Learned 是到 dueDay 为止学掉的条数。
	TotalNew int `json:"totalNew"`
	Learned  int `json:"learned"`
	// ReviewMinutesTotal 是整段计划花在复习上的分钟数，
	// 它和学习时间此消彼长，是「为什么后期学不动了」的答案。
	ReviewMinutesTotal int `json:"reviewMinutesTotal"`
	// PeakMinutes 是单日最高用时；超过 DailyMinutes 说明复习已压不动。
	PeakMinutes int `json:"peakMinutes"`
	// OverloadedDays 是「光复习就超时」的天数，>0 表示计划结构性不可行。
	OverloadedDays int `json:"overloadedDays"`
	DailyMinutes   int `json:"dailyMinutes"`
	// StudyDays / RestDays 是实际用于分布的天数。
	StudyDays int `json:"studyDays"`
	RestCount int `json:"restCount"`
}

type pending struct {
	lib   string
	due   string
	stage int
}

// Simulate 按天推演整个计划。
//
// 每天的流程：先还复习债，剩下的时间才拿去学新内容。
// 所以「学得越多 → 后面的复习越重 → 能学的新内容越少」这个反馈会被自动算进去。
func Simulate(in Input) Result {
	res := Result{Days: []DayPlan{}, DailyMinutes: in.DailyMinutes}
	if in.DailyMinutes <= 0 {
		in.DailyMinutes = 120
		res.DailyMinutes = in.DailyMinutes
	}
	intervals := in.Intervals
	if len(intervals) == 0 {
		intervals = fallbackIntervals
	}

	libs := map[string]LibInput{}
	order := make([]string, 0, len(in.Libs))
	remaining := map[string]int{}
	for _, l := range in.Libs {
		if l.MinutesPerItem <= 0 {
			l.MinutesPerItem = 5
		}
		if l.ReviewCap < 0 {
			l.ReviewCap = 0
		}
		libs[l.Library] = l
		order = append(order, l.Library)
		remaining[l.Library] = l.Remaining
	}
	sort.Strings(order)

	restSet := map[string]bool{}
	for _, d := range in.RestDays {
		if d != "" {
			restSet[d] = true
		}
	}
	wkRest := map[int]bool{}
	for _, w := range in.WeeklyRest {
		if w >= 0 && w <= 6 {
			wkRest[w] = true
		}
	}

	pend := make([]pending, 0, len(in.Pending))
	for _, p := range in.Pending {
		if p.DueDay == "" {
			continue
		}
		pend = append(pend, pending{lib: p.Library, due: p.DueDay, stage: p.Stage})
	}

	for _, l := range in.Libs {
		if l.Remaining > 0 {
			res.TotalNew += l.Remaining
		}
	}

	guard := 0
	for day := in.StartDay; day <= in.DueDay && guard < maxHorizon; day = addDays(day, 1) {
		guard++
		dp := DayPlan{
			Day:       day,
			New:       map[string]int{},
			Review:    map[string]int{},
			Remaining: map[string]int{},
		}

		if restSet[day] || wkRest[weekDay(day)] {
			dp.Rest = true
			res.RestCount++
			for _, l := range order {
				dp.Remaining[l] = remaining[l]
			}
			res.Days = append(res.Days, dp)
			continue
		}
		res.StudyDays++

		// ---- 1. 先还复习债 ----
		dueIdx := make([]int, 0, len(pend))
		for i, p := range pend {
			if p.due <= day {
				dueIdx = append(dueIdx, i)
			}
		}
		sort.Slice(dueIdx, func(a, b int) bool {
			pa, pb := pend[dueIdx[a]], pend[dueIdx[b]]
			if pa.due != pb.due {
				return pa.due < pb.due
			}
			return pa.stage < pb.stage
		})

		perLib := map[string]int{}
		reviewMinutes := 0
		taken := make([]bool, len(pend))
		for _, i := range dueIdx {
			p := pend[i]
			cfg := libs[p.lib]
			if cfg.ReviewCap > 0 && perLib[p.lib] >= cfg.ReviewCap {
				continue // 今天该库的复习额度用完，留到明天
			}
			perLib[p.lib]++
			cost := int(float64(cfg.MinutesPerItem)*factorFor(p.stage) + 0.5)
			if cost < 1 {
				cost = 1
			}
			reviewMinutes += cost
			taken[i] = true
			dp.Review[p.lib]++
		}

		// ---- 2. 剩下的时间学新内容 ----
		newBudget := in.DailyMinutes - reviewMinutes
		if newBudget < 0 {
			newBudget = 0
			res.OverloadedDays++
		}
		alloc := allocate(newBudget, order, remaining, libs)
		newMinutes := 0
		for _, l := range order {
			n := alloc[l]
			if n <= 0 {
				continue
			}
			dp.New[l] = n
			newMinutes += n * libs[l].MinutesPerItem
			remaining[l] -= n
			res.Learned += n
			// 新学内容进入复习队列
			pend = append(pend, pending{lib: l, due: addDays(day, intervals[0]), stage: 0})
		}

		// ---- 3. 推进已复习内容的阶段 ----
		kept := make([]pending, 0, len(pend))
		for i, p := range pend {
			if i < len(taken) && taken[i] {
				stage := p.stage + 1
				if stage >= len(intervals) {
					continue // 毕业
				}
				p.stage = stage
				p.due = addDays(day, intervals[stage])
			}
			kept = append(kept, p)
		}
		pend = kept

		dp.NewMinutes = newMinutes
		dp.ReviewMinutes = reviewMinutes
		dp.MinutesUsed = newMinutes + reviewMinutes
		if dp.MinutesUsed > res.PeakMinutes {
			res.PeakMinutes = dp.MinutesUsed
		}
		res.ReviewMinutesTotal += reviewMinutes
		for _, l := range order {
			dp.Remaining[l] = remaining[l]
		}
		res.Days = append(res.Days, dp)

		// 全部学完就提前收工，不用把日历排满
		if allDone(remaining) && res.FinishDay == "" {
			res.FinishDay = day
		}
	}

	if res.FinishDay == "" && allDone(remaining) {
		res.FinishDay = in.DueDay
	}
	res.Feasible = allDone(remaining)
	return res
}

// allDone 判断是否已学完。
//
// 空的 remaining（没选任何模块）不算「学完」—— 没有目标就无所谓完成，
// 否则界面上会出现「0 条任务，计划可行」这种无意义结论。
func allDone(remaining map[string]int) bool {
	if len(remaining) == 0 {
		return false
	}
	for _, v := range remaining {
		if v > 0 {
			return false
		}
	}
	return true
}

// allocate 把新学预算按「剩余工作量」分给各库。
//
// 按剩余条数等比分配，再用轮转把余下的预算补满，这样：
//   - 各模块大致同步推进，不会出现某个模块先学完干等着；
//   - 预算碎片也能被利用上，不会因为向下取整浪费掉半条的时间。
func allocate(budget int, order []string, remaining map[string]int, libs map[string]LibInput) map[string]int {
	out := map[string]int{}
	if budget <= 0 {
		return out
	}
	totalRem := 0
	for _, l := range order {
		totalRem += remaining[l]
	}
	if totalRem <= 0 {
		return out
	}

	for _, l := range order {
		if remaining[l] <= 0 {
			continue
		}
		cost := libs[l].MinutesPerItem
		share := float64(budget) * float64(remaining[l]) / float64(totalRem)
		n := int(share) / cost
		if n > remaining[l] {
			n = remaining[l]
		}
		if n > 0 {
			out[l] = n
		}
	}

	used := func() int {
		s := 0
		for _, l := range order {
			s += out[l] * libs[l].MinutesPerItem
		}
		return s
	}
	for {
		progressed := false
		for _, l := range order {
			if remaining[l]-out[l] <= 0 {
				continue
			}
			if used()+libs[l].MinutesPerItem <= budget {
				out[l]++
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

// Recommendation 是对「这个目标现实吗」的回答。
type Recommendation struct {
	// Feasible 是把用户设置原样跑一遍是否可行。
	Feasible bool `json:"feasible"`
	// RequiredDailyMinutes 是保持截止日不变、最少每天要投入多少分钟（不可行时才有意义）。
	RequiredDailyMinutes int `json:"requiredDailyMinutes"`
	// EarliestFinishDay 是按当前每天时间、最早哪天能学完（超出截止日时有意义）。
	EarliestFinishDay string `json:"earliestFinishDay"`
	// SuggestedDailyMinutes / SuggestedDueDay 是给出的替代方案：
	// 略微放宽截止日 + 略微提高每天时间，取两边都不极端的组合。
	SuggestedDailyMinutes int    `json:"suggestedDailyMinutes"`
	SuggestedDueDay       string `json:"suggestedDueDay"`
	// ReviewShare 是复习占总时间的比例（0..1），越高说明计划越靠复习撑着。
	ReviewShare float64 `json:"reviewShare"`
	// Verdict 是机器可读的结论：ok / raise-minutes / extend-deadline / both / impossible
	Verdict string `json:"verdict"`
}

// minMinutes / maxMinutes 是「每天投入」的可搜索范围（分钟）。
const (
	minMinutes = 15
	maxMinutes = 480
)

// Recommend 在可行与不可行两侧都给出可执行的替代方案。
func Recommend(in Input) Recommendation {
	base := Simulate(in)
	rec := Recommendation{Feasible: base.Feasible}

	total := base.DailyMinutes*(base.StudyDays) + 1
	if total > 0 {
		rec.ReviewShare = float64(base.ReviewMinutesTotal) / float64(total)
	}
	if rec.ReviewShare > 1 {
		rec.ReviewShare = 1
	}

	if base.Feasible {
		rec.RequiredDailyMinutes = in.DailyMinutes
		rec.EarliestFinishDay = base.FinishDay
		rec.SuggestedDailyMinutes = in.DailyMinutes
		rec.SuggestedDueDay = in.DueDay
		rec.Verdict = "ok"
		return rec
	}

	// 1) 截止日不变，需要多少分钟？
	rec.RequiredDailyMinutes = requiredMinutes(in)

	// 2) 每天时间不变，最早哪天学完？
	wide := in
	wide.DueDay = addDays(in.StartDay, maxHorizon-1)
	wres := Simulate(wide)
	rec.EarliestFinishDay = wres.FinishDay

	// 3) 折中方案：把每天时间提到「原值 × 1.5」和「必需值」的较小者，
	//    仍不可行就继续延长截止日，直到可行 —— 两边都不走极端。
	target := rec.RequiredDailyMinutes
	suggest := in.DailyMinutes + (target-in.DailyMinutes)*2/3
	if suggest < in.DailyMinutes {
		suggest = in.DailyMinutes
	}
	if suggest > maxMinutes {
		suggest = maxMinutes
	}
	if suggest < minMinutes {
		suggest = minMinutes
	}
	rec.SuggestedDailyMinutes = roundStep(suggest)

	cand := in
	cand.DailyMinutes = rec.SuggestedDailyMinutes
	cand.DueDay = addDays(in.StartDay, maxHorizon-1)
	cres := Simulate(cand)
	rec.SuggestedDueDay = cres.FinishDay

	switch {
	case rec.RequiredDailyMinutes <= maxMinutes && rec.RequiredDailyMinutes <= in.DailyMinutes*3/2:
		rec.Verdict = "raise-minutes"
	case rec.RequiredDailyMinutes > maxMinutes:
		rec.Verdict = "both"
	default:
		rec.Verdict = "extend-deadline"
	}
	if rec.RequiredDailyMinutes <= in.DailyMinutes {
		rec.Verdict = "both" // 时间够却学不完，只能是复习压垮，必须延长
	}
	return rec
}

// requiredMinutes 二分搜索「截止日不变时最少每天要多少分钟」。
func requiredMinutes(in Input) int {
	lo, hi := minMinutes, maxMinutes
	if !feasibleAt(in, hi) {
		return maxMinutes
	}
	for lo < hi {
		mid := (lo + hi) / 2
		if feasibleAt(in, mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return roundStep(lo)
}

func feasibleAt(in Input, minutes int) bool {
	try := in
	try.DailyMinutes = minutes
	return Simulate(try).Feasible
}

// roundStep 把分钟数抹到 5 分钟的整数倍，避免出现「每天 137 分钟」这种怪数。
func roundStep(m int) int {
	if m < minMinutes {
		return minMinutes
	}
	return ((m + 2) / 5) * 5
}
