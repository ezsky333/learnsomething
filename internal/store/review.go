package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// ---------- 复习调度：艾宾浩斯间隔 + 每日负载均衡 ----------

// ReviewIntervals 是各阶段的复习间隔（天）。
//
// 遗忘曲线在学完后第 1 天掉得最狠，之后越来越平缓，所以间隔必须「先密后疏」。
// 下面这串是 Ebbinghaus 经典节点的**间隔差**，累计起来正好是
// 1、3、7、14、29、59、119 天：
//
//	阶段 0（刚标记掌握）→ 1 天后第 1 次
//	阶段 1 → +2 天
//	阶段 2 → +4 天
//	阶段 3 → +7 天
//	阶段 4 → +15 天
//	阶段 5 → +30 天
//	阶段 6 → +60 天
//	阶段 7 = 毕业
//
// 7 次复习跨过半年，一条内容就算进入长期记忆，不再打扰。
var ReviewIntervals = []int{1, 2, 4, 7, 15, 30, 60}

// 复习评分。
const (
	GradeAgain = 0 // 忘了：记忆断了，退回第 1 阶段，明天重来
	GradeHard  = 1 // 模糊：只记住一半，原地踏步，间隔对折巩固
	GradeGood  = 2 // 记住了：按计划进入下一阶段
	GradeDefer = 3 // 今天没时间：顺延到明天，阶段不变
)

// ReviewItem 是一条内容的复习状态。
type ReviewItem struct {
	DocID       string `json:"docId"`
	ItemID      string `json:"itemId"`
	Stage       int    `json:"stage"`
	Lapses      int    `json:"lapses"`
	ReviewCount int    `json:"reviewCount"`
	LastDay     string `json:"lastDay"`
	DueDay      string `json:"dueDay"`
	LastGrade   int    `json:"lastGrade"`
	Graduated   bool   `json:"graduated"`

	// 下面两个字段读出来时现算，不落库。
	Overdue  int `json:"overdue"`  // 相对今天逾期几天（0 = 刚好今天到期）
	Interval int `json:"interval"` // 距下次复习还有几天（已逾期则为 0）
}

// ReviewDayLoad 是某一天排定的复习量。
type ReviewDayLoad struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
	// Overdue 只有「今天」这一格会大于 0：表示其中有多少是逾期积压。
	Overdue int `json:"overdue"`
}

// ReviewPlan 是复习模块一次请求要的全部数据。
type ReviewPlan struct {
	Day     string      `json:"day"`
	Cap     int         `json:"cap"`
	Enabled bool        `json:"enabled"`
	Queue   []ReviewItem `json:"queue"`

	DueTotal  int `json:"dueTotal"`  // 今天到期（含逾期）总条数
	Deferred  int `json:"deferred"`  // 今天排不下、已摊到未来的条数
	Backlog   int `json:"backlog"`   // 逾期条数
	DoneToday int `json:"doneToday"` // 今天已复习条数
	Enrolled  int `json:"enrolled"`  // 在籍（未毕业）条数
	Graduated int `json:"graduated"` // 已毕业条数

	Forecast   []ReviewDayLoad `json:"forecast"`
	NextDueDay string          `json:"nextDueDay"` // 最近一次未来的复习日（今天无事可做时提示用）

	// Ahead 是「提前复习」的天数窗口（0 = 只看今天到期的）。
	Ahead int `json:"ahead"`
	// AheadTotal 是在 Ahead 模式下额外拉进来的「还没到期」的条数。
	AheadTotal int `json:"aheadTotal"`

	// Stages 是当前生效的复习轮数。学得猛时会压缩（见 stageGaps），
	// 界面用它解释「为什么计划只有 3 轮」。
	Stages int `json:"stages"`
	// IntakePerDay 是近两周实测的每天新学条数，四舍五入到 0.1。
	IntakePerDay float64 `json:"intakePerDay"`
	// LibCap 是各内容库的每日额度，界面用它解释「算法为什么一天只排几条」。
	LibCap map[string]int `json:"libCap"`
}

// ReviewConfig 是复习模块的设置。
type ReviewConfig struct {
	// DailyCap 是每天最多安排多少条复习（全局总量）。
	//
	// 这是整个模块的调节旋钮：新学内容越多，稳态复习需求越高
	// （约为「每天新学条数 × 复习轮数」），超过上限的部分会被系统自动
	// 摊到未来几天、并把较牢固的内容往后拉，所以上限设多少，每天就真的只做多少。
	DailyCap int  `json:"dailyCap"`
	Enabled  bool `json:"enabled"`
	// LibCap 是各内容库的单独额度。
	//
	// 必须分开的理由很实在：算法一条复习要十几分钟，八股只要几分钟。
	// 共用一个全局上限时，算法会**永远排不进去** —— 八股条数多、复习快，
	// 总在额度用完前占满队列，算法的复习就被无限期挤后，等于没复习。
	LibCap map[string]int `json:"libCap"`
}

const (
	reviewConfigKey = "review_config"
	defaultDailyCap = 40
	dayLayout       = "2006-01-02"
)

// DefaultLibCap 是各库的默认每日复习条数。
//
// 按「一条要多久」反推：40 条八股约 40 分钟，6 条算法约 45 分钟，
// 两边投入的时间相当，才谈得上公平。
func DefaultLibCap() map[string]int {
	return map[string]int{
		"interview": 30,
		"algo":      6,
		"ai":        15,
	}
}

// DefaultReviewConfig 返回默认设置。
func DefaultReviewConfig() ReviewConfig {
	return ReviewConfig{DailyCap: defaultDailyCap, Enabled: true, LibCap: DefaultLibCap()}
}

// ---------- 日期工具 ----------

// addDays 在日期上加 n 天。用 UTC 解析，纯日历运算，不受时区和夏令时影响。
func addDays(day string, n int) string {
	t, err := time.Parse(dayLayout, day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, n).Format(dayLayout)
}

// diffDays 返回 to - from 的天数。
func diffDays(from, to string) int {
	a, err1 := time.Parse(dayLayout, from)
	b, err2 := time.Parse(dayLayout, to)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(b.Sub(a) / (24 * time.Hour))
}

// reviewInterval 返回理论阶梯中某阶段的间隔天数。
func reviewInterval(stage int) int {
	if stage < 0 {
		return ReviewIntervals[0]
	}
	if stage >= len(ReviewIntervals) {
		return 0 // 已毕业
	}
	return ReviewIntervals[stage]
}

// gapOf 取第 i 轮的间隔，越界时回退到相邻项。
func gapOf(gaps []int, i int) int {
	if len(gaps) == 0 {
		return ReviewIntervals[0]
	}
	if i < 0 {
		return gaps[0]
	}
	if i >= len(gaps) {
		return gaps[len(gaps)-1]
	}
	return gaps[i]
}

// recentIntake 估算「每天新学多少条」：近 14 天标记掌握的条数 / 14。
//
// 数据源是 progress.done_day 而不是 review.created_at —— 前者是用户
// 真正「学完并标记掌握」的日期，和 day 参数同一套日历；后者是数据库写入
// 的真实时钟，补录历史进度时会完全对不上。
//
// 用实测值而不是让用户填，因为它会随备考节奏自然变化，
// 而且用户往往低估自己一天能点掉多少题。
func (s *Store) recentIntake(day string) float64 {
	since := addDays(day, -13)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM progress
		WHERE status = ? AND done_day >= ? AND done_day <= ?`,
		StatusDone, since, day).Scan(&n); err != nil {
		return 0
	}
	if n == 0 {
		return 0
	}
	return float64(n) / 14.0
}

// stageGaps 返回「当前该走几轮、每轮间隔多少天」。
//
// 绕不开的算术：每条内容一生要复习 L 次，每天新学 I 条，那么
// **每天必然产生 I×L 条复习需求**（与间隔长短无关，间隔只决定它们排在什么时候）。
// 若 I×L 超过每日预算 C，需求每天溢出，逾期会无限累积 ——
// 光靠「把内容往后推」压不住，因为推只能改变时间，改变不了总次数。
//
// 所以过载时必须压缩轮数 L：L = C/I。宁可把 7 轮压成 3 轮、
// 每条都扎扎实实复习到，也不要排一个半年长的完美计划然后整体放弃。
//
// 压缩后不能沿用长阶梯的节点。若从 1、3、7、14、29、59、119 里等距抽 3 个点，
// 会得到「1 天 → 13 天 → 105 天」：只复习过两次就隔 105 天，
// 几乎必然遗忘，等于白排。正确做法是**等比扩张**（相邻间隔乘同一个系数），
// 让越少的复习次数对应越陡的间隔曲线、越短的总跨度：
//
//	L=3 → 1、3、9 天（累计 1、4、13）
//	L=4 → 1、3、9、27 天（累计 1、4、13、40）
//	L=5 → 1、3、9、27、81 天（累计 1、4、13、40、121）
//
// 这既是「少而陡」的科学退化方式，也让在籍池很快收敛（跨度短 → 池子小）。
// 负载轻时（I ≤ C/7）仍走完整的艾宾浩斯阶梯。
func (s *Store) stageGaps(day string, cfg ReviewConfig) []int {
	full := len(ReviewIntervals)
	intake := s.recentIntake(day)
	if intake <= 0 {
		return ReviewIntervals
	}

	l := int(float64(cfg.DailyCap) / intake)
	if l >= full {
		return ReviewIntervals
	}
	if l < 3 {
		// 再少就不叫间隔重复了。此时该做的是少学一点，界面会提示。
		l = 3
	}

	const (
		expandRatio = 3
		maxGap      = 120
	)
	gaps := make([]int, 0, l)
	g := 1
	for i := 0; i < l; i++ {
		if i > 0 {
			g *= expandRatio
			if g > maxGap {
				g = maxGap
			}
		}
		gaps = append(gaps, g)
	}
	return gaps
}

// ---------- 表结构与读写 ----------

const reviewCols = `doc_id, item_id, stage, lapses, review_count, last_day, due_day, last_grade, graduated`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanReview(sc rowScanner) (ReviewItem, error) {
	var it ReviewItem
	var graduated int
	err := sc.Scan(&it.DocID, &it.ItemID, &it.Stage, &it.Lapses, &it.ReviewCount,
		&it.LastDay, &it.DueDay, &it.LastGrade, &graduated)
	if err != nil {
		return it, err
	}
	it.Graduated = graduated == 1
	return it, nil
}

// finalize 补上现算的派生字段。
//
// Interval 由 due_day 反推，而不是查阶段表 —— 因为阶段间隔会随负载压缩，
// 只有「实际排在哪天」才是用户真正会体验到的东西。
func (it *ReviewItem) finalize(today string) {
	it.Interval = reviewInterval(it.Stage)
	it.Overdue = 0
	if it.Graduated || it.DueDay == "" || today == "" {
		return
	}
	if d := diffDays(it.DueDay, today); d > 0 {
		it.Overdue = d
		it.Interval = 0
	} else {
		it.Interval = diffDays(today, it.DueDay)
	}
}

// GetReviewConfig 读取复习设置。
func (s *Store) GetReviewConfig() (ReviewConfig, error) {
	raw, err := s.Meta(reviewConfigKey)
	if err != nil {
		return DefaultReviewConfig(), err
	}
	if raw == "" {
		return DefaultReviewConfig(), nil
	}
	cfg := DefaultReviewConfig()
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		// 数据损坏时回到默认值，不让整个快照接口失败。
		return DefaultReviewConfig(), nil
	}
	if cfg.DailyCap <= 0 {
		cfg.DailyCap = defaultDailyCap
	}
	// LibCap 缺失时补上默认值；已存在的键保留用户设置。
	merged := DefaultLibCap()
	for k, v := range cfg.LibCap {
		if v > 0 {
			merged[k] = v
		}
	}
	cfg.LibCap = merged
	return cfg, nil
}

// SetReviewConfig 保存复习设置。
func (s *Store) SetReviewConfig(cfg ReviewConfig) (ReviewConfig, error) {
	if cfg.DailyCap <= 0 {
		cfg.DailyCap = defaultDailyCap
	}
	if cfg.DailyCap > 500 {
		cfg.DailyCap = 500
	}
	if cfg.LibCap == nil {
		cfg.LibCap = DefaultLibCap()
	}
	for k, v := range cfg.LibCap {
		if v < 1 {
			cfg.LibCap[k] = 1
		}
		if cfg.LibCap[k] > 500 {
			cfg.LibCap[k] = 500
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	if err := s.SetMeta(reviewConfigKey, string(raw)); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// libCapOf 返回某库的每日复习额度；没配置时用默认值。
func libCapOf(cfg ReviewConfig, library string) int {
	if v, ok := cfg.LibCap[library]; ok && v > 0 {
		return v
	}
	if v, ok := DefaultLibCap()[library]; ok {
		return v
	}
	return cfg.DailyCap
}

// libCapsOf 返回补齐默认值后的各库额度。
func libCapsOf(cfg ReviewConfig) map[string]int {
	out := DefaultLibCap()
	for k, v := range cfg.LibCap {
		if v > 0 {
			out[k] = v
		}
	}
	return out
}

// SetDocLibraries 注入 docID → 内容库 的映射。
//
// 复习额度必须按库分配（算法一条要十几分钟，八股只要几分钟），
// 但 store 本身不读 manifest，所以由 server 在启动时把映射交进来。
func (s *Store) SetDocLibraries(m map[string]string) {
	s.libMu.Lock()
	defer s.libMu.Unlock()
	s.docLib = m
}

// libOf 返回某文档所属的库，未知时按八股处理（最保守的额度）。
func (s *Store) libOf(docID string) string {
	s.libMu.RLock()
	defer s.libMu.RUnlock()
	if v, ok := s.docLib[docID]; ok && v != "" {
		return v
	}
	return "interview"
}

// dueCounts 统计指定几天的排定复习量，用于负载均衡。
func (s *Store) dueCounts(days []string) (map[string]int, error) {
	out := make(map[string]int, len(days))
	if len(days) == 0 {
		return out, nil
	}
	q := `SELECT due_day, COUNT(*) FROM review WHERE graduated = 0 AND due_day IN (?`
	args := []any{days[0]}
	for _, d := range days[1:] {
		q += `,?`
		args = append(args, d)
	}
	q += `) GROUP BY due_day`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var day string
		var n int
		if err := rows.Scan(&day, &n); err != nil {
			return out, err
		}
		out[day] = n
	}
	return out, rows.Err()
}

// pickDueDay 挑一个下次复习的日子。
//
// 这是「不至于每天堆积太多」的第一层：不做 `今天 + 间隔` 这种硬性推算，
// 而是在间隔附近的几天窗口里，挑**最早那个还没排满的日子**。
// 于是同一天标记掌握的一批内容会被自然摊到相邻几天，而不会全部挤在同一天。
//
// 窗口宽度约为间隔的 1/3（最少 1 天，最多 7 天），既保持了间隔的科学性
// （第 1 次复习仍然落在 1~2 天内，不会拖成 5 天后），又留出了腾挪空间。
func (s *Store) pickDueDay(base string, gap, cap int) (string, error) {
	if gap < 1 {
		gap = 1
	}
	spread := gap / 3
	if spread < 1 {
		spread = 1
	}
	if spread > 7 {
		spread = 7
	}

	cands := make([]string, 0, spread+1)
	for i := 0; i <= spread; i++ {
		cands = append(cands, addDays(base, gap+i))
	}

	loads, err := s.dueCounts(cands)
	if err != nil {
		return cands[0], err
	}

	best, bestLoad := cands[0], loads[cands[0]]
	for _, c := range cands {
		// 第一个还有空位的日子就是它，保持间隔尽量贴近理论值
		if loads[c] < cap {
			return c, nil
		}
		if loads[c] < bestLoad {
			best, bestLoad = c, loads[c]
		}
	}
	// 整段窗口都排满了，退而求其次挑最空的一天，绝不让内容无处可去
	return best, nil
}

// findSlot 从 base+gap 开始往后几周里找一个还没排满的日子。
//
// 由近到远分三档试探：先按原间隔找（最贴合科学节奏），
// 再放到两倍间隔（放宽两周），最后放到四倍（放宽一个月）。
// ok=false 表示连最宽的一档都没空位，此时只能压缩间隔本身。
func (s *Store) findSlot(base string, gap, cap int) (string, bool, error) {
	windows := []struct {
		mult   int
		spread int
	}{
		{1, 7},  // 原间隔附近一周
		{2, 14}, // 两倍间隔附近两周
		{4, 30}, // 四倍间隔附近一个月
	}
	for _, w := range windows {
		g := gap * w.mult
		if g < 1 {
			g = 1
		}
		cands := make([]string, 0, w.spread+1)
		for i := 0; i <= w.spread; i++ {
			cands = append(cands, addDays(base, g+i))
		}
		loads, err := s.dueCounts(cands)
		if err != nil {
			return "", false, err
		}
		for _, c := range cands {
			if loads[c] < cap {
				return c, true, nil
			}
		}
	}
	return "", false, nil
}

// todayBudget 返回「今天还能复习多少条」。
//
// 每日上限约束的是「今天总共做多少」，所以要扣掉已经做完的；
// 否则做到上限后，队列又会冒出新的，永远做不完。
func (s *Store) todayBudget(day string, cfg ReviewConfig) (int, error) {
	var doneToday int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM review_log WHERE day = ?`, day).
		Scan(&doneToday); err != nil {
		return 0, err
	}
	budget := cfg.DailyCap - doneToday
	if budget < 0 {
		budget = 0
	}
	return budget, nil
}

// pickByLibCap 从候选里挑出今天的队列，兼顾「各库额度」与「全局额度」。
//
// 为什么要分两轮而不是简单取前 N 条：八股条数多、复习快，如果只按全局额度
// 从队首取，它会一路占满队列，算法复习被无限期挤后 —— 用户看到的现象是
// 「算法从来没被安排复习」，而算法恰恰是最需要重复的。
//
// 所以：
//  1. 保底轮 —— 每个库先按「自身额度占总额度的比例」分到一份名额，
//     保证小额度但重要的库（算法）有位置；
//  2. 补齐轮 —— 还有全局余额就按原顺序继续补，但仍不突破各库自己的上限。
//
// 两轮都保持「逾期最久优先」的相对顺序，不会打乱该先补哪些。
func (s *Store) pickByLibCap(cands []ReviewItem, budget int, cfg ReviewConfig) []ReviewItem {
	if budget <= 0 || len(cands) == 0 {
		return []ReviewItem{}
	}

	// 统计候选里出现了哪些库、各有多少条
	demand := map[string]int{}
	for _, it := range cands {
		demand[s.libOf(it.DocID)]++
	}
	totalCap := 0
	caps := map[string]int{}
	for lib := range demand {
		caps[lib] = libCapOf(cfg, lib)
		totalCap += caps[lib]
	}
	if totalCap <= 0 {
		totalCap = 1
	}

	out := make([]ReviewItem, 0, budget)
	taken := make([]bool, len(cands))
	perLib := map[string]int{}

	// 1. 保底轮：按额度占比分配名额（向上取整，让小额度的库也能分到）
	quota := map[string]int{}
	for lib, c := range caps {
		q := (budget*c + totalCap - 1) / totalCap
		if q > c {
			q = c
		}
		if q > demand[lib] {
			q = demand[lib]
		}
		quota[lib] = q
	}
	for i, it := range cands {
		if len(out) >= budget {
			break
		}
		lib := s.libOf(it.DocID)
		if perLib[lib] >= quota[lib] {
			continue
		}
		perLib[lib]++
		taken[i] = true
		out = append(out, it)
	}

	// 2. 补齐轮：用剩下的全局余额按原顺序补，但各库不得突破自己的上限
	for i, it := range cands {
		if len(out) >= budget {
			break
		}
		if taken[i] {
			continue
		}
		lib := s.libOf(it.DocID)
		if perLib[lib] >= caps[lib] {
			continue
		}
		perLib[lib]++
		out = append(out, it)
	}

	// 恢复「逾期最久优先、阶段低优先」的原始顺序
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].DueDay != out[b].DueDay {
			return out[a].DueDay < out[b].DueDay
		}
		return out[a].Stage < out[b].Stage
	})
	return out
}

// dueItems 取今天到期（含逾期）的内容，按「该先复习谁」排好序：
// 逾期最久的优先；同一天到期的，阶段越低越优先（记忆越脆，漏一次基本白学）。
func (s *Store) dueItems(day string) ([]ReviewItem, error) {
	return s.dueItemsUntil(day, day)
}

// dueItemsUntil 取 [.., until] 到期（含逾期）的内容，今天到期的排前面。
//
// 提前复习用它：今天到期的永远优先，之后才是未来几天到期的，
// 这样「提前看」不会把真正该补的积压挤到后面。
func (s *Store) dueItemsUntil(day, until string) ([]ReviewItem, error) {
	rows, err := s.db.Query(`SELECT `+reviewCols+` FROM review
		WHERE graduated = 0 AND due_day <= ?
		ORDER BY due_day ASC, stage ASC, lapses DESC, doc_id, item_id`, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewItem{}
	for rows.Next() {
		it, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		it.finalize(day)
		out = append(out, it)
	}
	return out, rows.Err()
}

// rebalance 把今天排不下的内容摊到未来几天。
//
// 这是「不至于每天堆积太多」的第二层：逾期内容如果一直留在原地，
// 第二天就和新逾期一起排队，队列只增不减。所以每次开盘时，
// 超出当日额度的部分会被按自身间隔推到未来有空位的日子。
//
// 只动 due_day，不改阶段 —— 相当于「这一轮先欠着，但排队位置往后挪」。
// 返回 (被挪用条数, 其中被强制拉长间隔的条数)。
func (s *Store) rebalance(day string, cfg ReviewConfig) (int, int, error) {
	if !cfg.Enabled {
		return 0, 0, nil
	}
	budget, err := s.todayBudget(day, cfg)
	if err != nil {
		return 0, 0, err
	}

	due, err := s.dueItems(day)
	if err != nil {
		return 0, 0, err
	}
	if len(due) <= budget {
		return 0, 0, nil
	}

	moved := 0
	stretched := 0
	now := time.Now().UTC().Format(time.RFC3339)
	gaps := s.stageGaps(day, cfg)
	for _, it := range due[budget:] {
		gap := gapOf(gaps, it.Stage)
		if gap < 1 {
			gap = 1
		}
		nd, ok, err := s.findSlot(day, gap, cfg.DailyCap)
		if err != nil {
			return moved, stretched, err
		}
		if !ok {
			// 近处全都排满了。继续往近处塞只会把后面几天也灌满，
			// 所以改成把这条记忆往后拉：阶段越高说明越牢固，拉长的代价越小。
			stretched++
			mult := 2 + it.Stage
			nd = addDays(day, gap*mult)
			if nd <= day {
				nd = addDays(day, 7*mult)
			}
		}
		if _, err := s.db.Exec(`UPDATE review SET due_day = ?, updated_at = ?
			WHERE doc_id = ? AND item_id = ?`,
			nd, now, it.DocID, it.ItemID); err != nil {
			return moved, stretched, err
		}
		moved++
	}
	return moved, stretched, nil
}

// EnrollReview 把一条刚标记掌握的内容纳入复习计划。
//
// 重复纳入（比如先加星又取消）不会重置已经走过的复习进度。
func (s *Store) EnrollReview(docID, itemID, day string) error {
	if day == "" {
		day = time.Now().Format(dayLayout)
	}
	var exists int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM review WHERE doc_id = ? AND item_id = ?`,
		docID, itemID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	cfg, err := s.GetReviewConfig()
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}

	gaps := s.stageGaps(day, cfg)
	due, err := s.pickDueDay(day, gapOf(gaps, 0), cfg.DailyCap)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`INSERT INTO review
		(doc_id, item_id, stage, lapses, review_count, last_day, due_day, last_grade, graduated, created_at, updated_at)
		VALUES(?, ?, 0, 0, 0, ?, ?, -1, 0, ?, ?)
		ON CONFLICT(doc_id, item_id) DO NOTHING`,
		docID, itemID, day, due, now, now)
	return err
}

// DropReview 把一条内容移出复习计划（取消「已掌握」时）。
func (s *Store) DropReview(docID, itemID string) error {
	_, err := s.db.Exec(`DELETE FROM review WHERE doc_id = ? AND item_id = ?`, docID, itemID)
	return err
}

// GetReview 读取一条复习状态。
func (s *Store) GetReview(docID, itemID string) (ReviewItem, error) {
	it, err := scanReview(s.db.QueryRow(`SELECT `+reviewCols+` FROM review
		WHERE doc_id = ? AND item_id = ?`, docID, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

// ActiveReviews 返回全部在籍（未毕业）的复习状态。
func (s *Store) ActiveReviews() ([]ReviewItem, error) {
	rows, err := s.db.Query(`SELECT ` + reviewCols + ` FROM review WHERE graduated = 0
		ORDER BY due_day, doc_id, item_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewItem{}
	for rows.Next() {
		it, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GradeReview 对一条内容评分并推进复习计划。
func (s *Store) GradeReview(docID, itemID string, grade int, day string) (ReviewItem, error) {
	if day == "" {
		day = time.Now().Format(dayLayout)
	}
	it, err := s.GetReview(docID, itemID)
	if err != nil {
		return it, err
	}
	if it.Graduated {
		return it, ErrNotFound
	}

	cfg, err := s.GetReviewConfig()
	if err != nil {
		return it, err
	}

	stage := it.Stage
	lapses := it.Lapses
	reviewCount := it.ReviewCount
	due := it.DueDay
	graduated := 0

	// 轮数与各轮间隔随负载浮动：学得猛时阶梯自动压缩，
	// 保证「每天新学 I 条 × 轮数 L ≤ 每天预算 C」这个吞吐不等式成立。
	gaps := s.stageGaps(day, cfg)
	stages := len(gaps)

	switch grade {
	case GradeGood:
		stage = it.Stage + 1
		reviewCount++
		if stage >= stages {
			// 走完当前阶梯，进入长期记忆，不再出现
			graduated = 1
			due = ""
		} else if due, err = s.pickDueDay(day, gapOf(gaps, stage), cfg.DailyCap); err != nil {
			return it, err
		}
	case GradeHard:
		// 有印象但不牢，原地踏步，间隔对折巩固一次
		reviewCount++
		gap := gapOf(gaps, it.Stage) / 2
		if due, err = s.pickDueDay(day, gap, cfg.DailyCap); err != nil {
			return it, err
		}
	case GradeAgain:
		// 忘了，记忆链断了：重回第 1 阶段，明天重来
		stage = 0
		lapses++
		reviewCount++
		if due, err = s.pickDueDay(day, gapOf(gaps, 0), cfg.DailyCap); err != nil {
			return it, err
		}
	case GradeDefer:
		// 今天没时间，顺延到明天，阶段不动、不计入复习次数
		due = addDays(day, 1)
	default:
		return it, errors.New("评分不合法")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE review SET stage = ?, lapses = ?, review_count = ?,
		last_day = ?, due_day = ?, last_grade = ?, graduated = ?, updated_at = ?
		WHERE doc_id = ? AND item_id = ?`,
		stage, lapses, reviewCount, day, due, grade, graduated, now, docID, itemID); err != nil {
		return it, err
	}

	// 顺延不算复习过，不进日志，否则「今天复习了多少条」会被虚高。
	if grade != GradeDefer {
		if _, err := s.db.Exec(`INSERT INTO review_log
			(doc_id, item_id, grade, day, stage_before, stage_after, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?)`,
			docID, itemID, grade, day, it.Stage, stage, now); err != nil {
			return it, err
		}
	}

	it.Stage = stage
	it.Lapses = lapses
	it.ReviewCount = reviewCount
	it.LastDay = day
	it.DueDay = due
	it.LastGrade = grade
	it.Graduated = graduated == 1
	it.finalize(day)
	return it, nil
}

// ReviewPlan 生成「今天该复习什么」。
//
// 会做一次「开盘重新排期」：今天到期量超过每日上限时，超出部分被摊到未来
// 有空位的日子（越牢固的内容推得越远），保证队列长度有界、积压不滚雪球。
func (s *Store) ReviewPlan(day string) (ReviewPlan, error) {
	return s.ReviewPlanAhead(day, 0)
}

// ReviewPlanAhead 同 ReviewPlan，但可以把未来 ahead 天内到期的内容提前拉进来。
//
// 用途：今天恰好没有到期内容时（学完第一天必然如此），
// 让用户能主动提前复习明天的，既不至于空转，也能把高峰提前削平。
// 提前复习不改变阶段推进逻辑，只是把 due_day 更晚的条目也放进队列。
func (s *Store) ReviewPlanAhead(day string, ahead int) (ReviewPlan, error) {
	if day == "" {
		day = time.Now().Format(dayLayout)
	}
	if ahead < 0 {
		ahead = 0
	}
	if ahead > 30 {
		ahead = 30
	}
	cfg, err := s.GetReviewConfig()
	if err != nil {
		return ReviewPlan{}, err
	}

	gaps := s.stageGaps(day, cfg)
	plan := ReviewPlan{
		Day:          day,
		Cap:          cfg.DailyCap,
		Enabled:      cfg.Enabled,
		Queue:        []ReviewItem{},
		Forecast:     []ReviewDayLoad{},
		Stages:       len(gaps),
		IntakePerDay: float64(int(s.recentIntake(day)*10+0.5)) / 10,
		Ahead:        ahead,
		LibCap:       libCapsOf(cfg),
	}

	// 今天已经复习的条数 —— 每日上限约束的是「今天总共做多少」，
	// 所以必须先扣掉已完成的额度，否则做到上限后队列又会冒出新的。
	budget, err := s.todayBudget(day, cfg)
	if err != nil {
		return plan, err
	}
	if done := cfg.DailyCap - budget; done > 0 {
		plan.DoneToday = done
	}

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM review WHERE graduated = 1`).
		Scan(&plan.Graduated); err != nil {
		return plan, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM review WHERE graduated = 0`).
		Scan(&plan.Enrolled); err != nil {
		return plan, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM review WHERE graduated = 0 AND due_day < ?`, day).
		Scan(&plan.Backlog); err != nil {
		return plan, err
	}

	// 先记录「真实的今日需求」，再做摊派 —— 否则用户看不到自己其实欠了多少。
	due, err := s.dueItems(day)
	if err != nil {
		return plan, err
	}
	plan.DueTotal = len(due)

	// 超额的摊到未来。越牢固的内容间隔越长，推得越远，代价最小。
	if cfg.Enabled && len(due) > budget {
		moved, _, err := s.rebalance(day, cfg)
		if err != nil {
			return plan, err
		}
		plan.Deferred = moved
	}

	if cfg.Enabled {
		pick := due
		if ahead > 0 {
			// 提前模式：把未来 ahead 天内到期的也一并纳入候选
			window, err := s.dueItemsUntil(day, addDays(day, ahead))
			if err != nil {
				return plan, err
			}
			pick = window
		}

		// 按库分额度，并保证每个库都能分到自己那一份。
		//
		// 只按全局上限取前 N 条是错的：八股条数多、复习快，会一路占满队列，
		// 算法的复习被无限期挤后 —— 用户看到的是「算法从来没被安排复习」。
		// 所以这里先各库保底取自己额度内的，再按剩余全局额度补。
		pick = s.pickByLibCap(pick, budget, cfg)

		plan.Queue = pick
		// AheadTotal 只统计「真正进到队列里、且今天还没到期」的条数，
		// 否则界面上会出现「队列 20 条但提前复习 25 条」这种对不上的数字。
		for _, it := range pick {
			if it.DueDay > day {
				plan.AheadTotal++
			}
		}
	}

	// 未来 14 天的排定负载
	const horizon = 14
	days := make([]string, 0, horizon)
	for i := 0; i < horizon; i++ {
		days = append(days, addDays(day, i))
	}
	end := days[len(days)-1]
	frows, err := s.db.Query(`SELECT due_day, COUNT(*) FROM review
		WHERE graduated = 0 AND due_day >= ? AND due_day <= ?
		GROUP BY due_day`, day, end)
	if err != nil {
		return plan, err
	}
	loads := map[string]int{}
	for frows.Next() {
		var d string
		var n int
		if err := frows.Scan(&d, &n); err != nil {
			frows.Close()
			return plan, err
		}
		loads[d] = n
	}
	frows.Close()
	if err := frows.Err(); err != nil {
		return plan, err
	}

	for i, d := range days {
		load := ReviewDayLoad{Day: d, Count: loads[d]}
		if i == 0 {
			// 今天这一格要算上逾期积压，否则「今天没事」的假象会误导人
			load.Count = plan.DueTotal
			load.Overdue = plan.Backlog
		}
		plan.Forecast = append(plan.Forecast, load)
	}

	// 今天没事做时，告诉用户下次是哪天，而不是留一个空白页
	if plan.DueTotal == 0 {
		var next sql.NullString
		if err := s.db.QueryRow(`SELECT MIN(due_day) FROM review WHERE graduated = 0 AND due_day > ?`, day).
			Scan(&next); err == nil && next.Valid {
			plan.NextDueDay = next.String
		}
	} else {
		plan.NextDueDay = day
	}

	return plan, nil
}

// ReviewLogsByDay 返回最近若干天每天复习的条数，用于趋势展示。
func (s *Store) ReviewLogsByDay(sinceDay string) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT day, COUNT(*) FROM review_log WHERE day >= ? GROUP BY day`, sinceDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		out[d] = n
	}
	return out, rows.Err()
}

// ClearReviews 清空整个复习计划（含历史日志）。
func (s *Store) ClearReviews() error {
	if _, err := s.db.Exec(`DELETE FROM review`); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM review_log`)
	return err
}

// ResetReviewItem 把某条内容退回「刚掌握」状态（用于手动重来）。
func (s *Store) ResetReviewItem(docID, itemID, day string) error {
	if day == "" {
		day = time.Now().Format(dayLayout)
	}
	cfg, err := s.GetReviewConfig()
	if err != nil {
		return err
	}
	gaps := s.stageGaps(day, cfg)
	due, err := s.pickDueDay(day, gapOf(gaps, 0), cfg.DailyCap)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`UPDATE review SET stage = 0, lapses = 0, review_count = 0,
		last_day = ?, due_day = ?, last_grade = -1, graduated = 0, updated_at = ?
		WHERE doc_id = ? AND item_id = ?`, day, due, now, docID, itemID)
	return err
}
