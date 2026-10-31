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

// reviewFactor 是「复习一条的成本 / 首次学习一条的成本」，按阶段递减。
//
// 取值依据：第一次回头看时忘得最多，但仍是**回忆与核对**，不是重新学一遍，
// 所以约 1/3；越往后越是维护性巩固，降到 1/10 左右。
// 整条阶梯累计约 1.27 倍 —— 即「学一条内容，一生要再花 1.27 倍的时间复习」。
//
// 这个系数直接决定计划可行性，不能拍脑袋：
// 若取成 2.9（把每次复习都当重学），任何真实目标都会被判成不可行，
// 那这个功能就没用了。也不能取成 0.5，那会把复习负担算没，
// 结果就是「计划看着很美，做到一半发现来不及」。
var reviewFactor = []float64{0.35, 0.25, 0.20, 0.15, 0.12, 0.10, 0.10}

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
	// OnlyWindow 非空时，这个库的新内容只能排进指定时间窗。
	//
	// 真实场景里的约束就是这么来的：算法必须脑子清醒时啃（上午），
	// 八股和复习可以放到疲惫时段（下午）。不建模这个约束，
	// 算出来的计划会把算法摊到全天，而用户根本做不到。
	OnlyWindow string `json:"onlyWindow"`
}

// Pending 是一条已进入复习计划、尚未到期复习的内容。
type Pending struct {
	Library string `json:"library"`
	DueDay  string `json:"dueDay"`
	Stage   int    `json:"stage"`
}

// Window 是一个时间窗（比如「上午」）。
//
// 复习也受时间窗约束：算法的复习和学新算法一样费脑子，不能排到下午。
type Window struct {
	// Name 是窗口标识，和 LibInput.OnlyWindow 对应。
	Name string `json:"name"`
	// Minutes 是这个窗口的可用人分钟数。
	Minutes int `json:"minutes"`
	// ReviewOnly 为 true 时该窗口只做复习，不学新内容。
	ReviewOnly bool `json:"reviewOnly"`
	// Libs 非空时，该窗口只处理这些库的复习。
	Libs []string `json:"libs"`
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
	// Windows 非空时按时间窗排期（上午/下午各自独立预算）；
	// 为空则退化成「一天一个总预算」的简单模式。
	Windows []Window `json:"-"`
	// Spent 是首日（今天）已经用掉的时间与已还的复习，用于把今天算准。
	//
	// 不给的话，中午打开网页时排期会认为今天一整天都还空着，
	// 给出「今天还要学 22 条」这种明显做不到的数字。
	Spent *SpentToday `json:"-"`
}

// SpentToday 记录今天已经发生的投入。
type SpentToday struct {
	// ReviewByLib 各库今天已复习的条数。
	ReviewByLib map[string]int
	// MinutesByWindow 各时间窗今天已用掉的分钟数（key 为窗口名）。
	MinutesByWindow map[string]int
	// TotalMinutes 未分窗模式下今天已用掉的分钟数。
	TotalMinutes int
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

		// 首日要把「今天已经用掉的时间」扣掉，否则排期会把今天当成完整的一天。
		//
		// 这是最容易出错的地方：用户在下午打开页面，早上已经复习掉了 40 条，
		// 排期还按整天预算去排，就会说「今天还要学 22 条」——
		// 而实际上剩下那点时间连复习债都还不完。
		dayIn := in
		if day == in.StartDay && in.Spent != nil {
			dayIn = applySpent(in)
		}

		taken := make([]bool, len(pend))
		newByLib := map[string]int{}
		reviewByLib := map[string]int{}

		var reviewMinutes int
		if len(in.Windows) == 0 {
			// 简单模式：一天一个总预算，先还复习债再学新内容。
			reviewMinutes = simPlain(day, dayIn, libs, order, remaining, pend, taken, intervals,
				newByLib, reviewByLib)
		} else {
			// 时间窗模式：每个窗独立列预算，复习和学新都在窗内结算。
			reviewMinutes = simWindows(day, dayIn, libs, order, remaining, pend, taken, intervals,
				newByLib, reviewByLib)
		}

		// 首日已还的复习要如实记进当天排期，否则界面显示「今天复习 0 条」，
		// 用户明明做了 40 条却看不到任何痕迹。
		if day == in.StartDay && in.Spent != nil {
			for lib, n := range in.Spent.ReviewByLib {
				if n > 0 {
					reviewByLib[lib] += n
				}
			}
			reviewMinutes += spentReviewMinutes(in.Spent, libs)
		}

		// ---- 推进已复习内容的阶段 ----
		kept := make([]pending, 0, len(pend))
		for i, p := range pend {
			if taken[i] {
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

		// ---- 把本日新学内容登记进复习队列 ----
		newMinutes := 0
		for _, l := range order {
			n := newByLib[l]
			if n <= 0 {
				continue
			}
			dp.New[l] = n
			remaining[l] -= n
			res.Learned += n
			newMinutes += n * libs[l].MinutesPerItem
			for i := 0; i < n; i++ {
				pend = append(pend, pending{lib: l, due: addDays(day, intervals[0]), stage: 0})
			}
		}
		for lib, n := range reviewByLib {
			if n > 0 {
				dp.Review[lib] = n
			}
		}

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

// applySpent 返回「今天已经用掉一部分时间」之后的输入。
//
// 只影响首日：把各时间窗的可用分钟数减去今天已经花掉的量，
// 剩下的才是还能安排新内容的时间。
func applySpent(in Input) Input {
	out := in
	if len(in.Windows) == 0 {
		left := in.DailyMinutes - in.Spent.TotalMinutes
		if left < 0 {
			left = 0
		}
		out.DailyMinutes = left
		return out
	}
	ws := make([]Window, len(in.Windows))
	copy(ws, in.Windows)
	for i := range ws {
		used := in.Spent.MinutesByWindow[ws[i].Name]
		left := ws[i].Minutes - used
		if left < 0 {
			left = 0
		}
		ws[i].Minutes = left
	}
	out.Windows = ws
	// 同步总预算，供 Recommend 使用
	total := 0
	for _, w := range ws {
		total += w.Minutes
	}
	out.DailyMinutes = total
	return out
}

// spentReviewMinutes 估算今天已复习的内容花掉了多少分钟。
func spentReviewMinutes(sp *SpentToday, libs map[string]LibInput) int {
	total := 0
	for lib, n := range sp.ReviewByLib {
		if n <= 0 {
			continue
		}
		cfg := libs[lib]
		total += n * reviewCost(cfg, 0)
	}
	return total
}

// reviewCost 返回复习一条内容要多少分钟。
func reviewCost(lib LibInput, stage int) int {
	c := int(float64(lib.MinutesPerItem)*factorFor(stage) + 0.5)
	if c < 1 {
		c = 1
	}
	return c
}

// dueIdxOf 返回今天该复习的下标，按「逾期最久 → 阶段最低」排序。
func dueIdxOf(pend []pending, day string) []int {
	idx := make([]int, 0, len(pend))
	for i, p := range pend {
		if p.due <= day {
			idx = append(idx, i)
		}
	}
	sort.Slice(idx, func(a, b int) bool {
		pa, pb := pend[idx[a]], pend[idx[b]]
		if pa.due != pb.due {
			return pa.due < pb.due
		}
		return pa.stage < pb.stage
	})
	return idx
}

// simPlain 是「一天一个总预算」的简单模式。返回复习花掉的分钟数。
//
// 复习优先于学新：到期没复习的内容会遗忘，而少学一条只是进度慢一点。
// 所以这里**不**用预算去截断复习量 —— 复习量超过当天预算时宁可让新学归零，
// 也不能把复习往后推（推一天就要多复习一堆，积压会滚雪球）。
// 超支会记进 OverloadedDays，由 Recommend 给出「加时间或延期」的建议。
func simPlain(day string, in Input, libs map[string]LibInput, order []string,
	remaining map[string]int, pend []pending, taken []bool, intervals []int,
	newByLib, reviewByLib map[string]int) int {

	perLib := map[string]int{}
	reviewMinutes := 0
	for _, i := range dueIdxOf(pend, day) {
		p := pend[i]
		cfg := libs[p.lib]
		if cfg.ReviewCap > 0 && perLib[p.lib] >= cfg.ReviewCap {
			continue
		}
		perLib[p.lib]++
		reviewMinutes += reviewCost(cfg, p.stage)
		taken[i] = true
		reviewByLib[p.lib]++
	}

	budget := in.DailyMinutes - reviewMinutes
	if budget < 0 {
		budget = 0
	}
	for lib, n := range allocate(budget, order, remaining, libs) {
		newByLib[lib] = n
	}
	return reviewMinutes
}

// simWindows 是「上午学算法 / 下午复习+八股」这类分时间窗模式。
//
// 每个窗有独立的分钟预算，而且复习也受窗约束：算法的复习和学新算法一样费脑子，
// 必须排在上午；八股的复习和八股的新内容都在下午。
// 这一点不建模就会算出做不到的计划 —— 用户下午根本啃不动算法。
func simWindows(day string, in Input, libs map[string]LibInput, order []string,
	remaining map[string]int, pend []pending, taken []bool, intervals []int,
	newByLib, reviewByLib map[string]int) int {

	due := dueIdxOf(pend, day)
	totalReview := 0

	for _, w := range in.Windows {
		if w.Minutes <= 0 {
			continue
		}
		allowed := w.allowedSet()
		libFits := func(lib string) bool {
			return allowed == nil || allowed[lib]
		}

		// ---- 1. 本窗先还复习债 ----
		// 同 simPlain：复习不受预算截断，宁可当天不学新内容。
		used := 0
		perLib := map[string]int{}
		for _, i := range due {
			if taken[i] {
				continue
			}
			p := pend[i]
			if !libFits(p.lib) {
				continue
			}
			cfg := libs[p.lib]
			if cfg.ReviewCap > 0 && perLib[p.lib] >= cfg.ReviewCap {
				continue
			}
			used += reviewCost(cfg, p.stage)
			perLib[p.lib]++
			taken[i] = true
			reviewByLib[p.lib]++
		}
		totalReview += used

		// ---- 2. 本窗再学新内容 ----
		if w.ReviewOnly {
			continue
		}
		budget := w.Minutes - used
		if budget <= 0 {
			continue
		}
		// 只给「允许在这个窗学新内容」的库分配额度。
		//
		// eligible 必须扣掉「今天已经在别的窗分配过的量」，否则同一库会被
		// 多个窗重复分配，最后写回去的总数超过剩余量。
		eligible := map[string]int{}
		subOrder := make([]string, 0, len(order))
		for _, l := range order {
			left := remaining[l] - newByLib[l]
			if left <= 0 {
				continue
			}
			if !windowAllowsNew(libs[l], w) {
				continue
			}
			eligible[l] = left
			subOrder = append(subOrder, l)
		}
		for lib, n := range allocate(budget, subOrder, eligible, libs) {
			if n <= 0 {
				continue
			}
			// 累加而不是覆盖：算法可以上午学，八股下午学，
			// 两个窗的产物必须相加。直接赋值会让后一个窗把前一个窗抹掉 ——
			// 现象是「上午的算法永远学不到」，非常隐蔽。
			newByLib[lib] += n
		}
	}
	return totalReview
}

// allowedSet 返回该窗允许的库集合；nil 表示不限。
func (w Window) allowedSet() map[string]bool {
	if len(w.Libs) == 0 {
		return nil
	}
	m := make(map[string]bool, len(w.Libs))
	for _, l := range w.Libs {
		m[l] = true
	}
	return m
}

// windowAllowsNew 判断某库能否在这个窗里学新内容。
//
// 两道闸门都要过：
//  1. 库自己声明了专属窗口（OnlyWindow）时，只能在该窗学 —— 算法必须上午啃；
//  2. 窗显式列了 Libs 时，只能学列出的库 —— 上午窗就只留给了算法，
//     不能再塞进八股，否则上午的额度会被八股悄悄吃掉。
func windowAllowsNew(lib LibInput, w Window) bool {
	if lib.OnlyWindow != "" && lib.OnlyWindow != w.Name {
		return false
	}
	if len(w.Libs) > 0 {
		for _, l := range w.Libs {
			if l == lib.Library {
				return true
			}
		}
		// 窗限定了库列表，但没包含这个库 → 不允许学
		return false
	}
	return !w.ReviewOnly
}

// allocate 把新学预算按「剩余工作量」分给各库。

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
