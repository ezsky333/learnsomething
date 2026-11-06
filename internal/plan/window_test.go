package plan

import "testing"

// NewOnly 的窗不该把预算花在复习上。
//
// 这是用户真实作息里的关键约束：上午一小时留给**新算法**，
// 复习一律放下午。没有这个开关时，算法复习会先把上午 60 分钟吃光，
// 结果是「每天都在复习，一条新算法都推不动」——
// 表面看是学不完，其实是窗口被复习占满了。
func TestNewOnlyWindowSkipsReview(t *testing.T) {
	in := windowInput(150)
	in.DueDay = "2026-06-30"
	// 上午是「只学新」的窗
	in.Windows[0].NewOnly = true
	// 造一批到期的算法复习：如果 NewOnly 没生效，这些会把上午占满
	for i := 0; i < 20; i++ {
		in.Pending = append(in.Pending, Pending{
			Library: "algo",
			DueDay:  "2026-03-01",
			Stage:   0,
		})
	}

	res := Simulate(in)
	if len(res.Days) == 0 {
		t.Fatal("应排出天数")
	}
	first := res.Days[0]
	// 上午 60 分钟全部可用于新算法：60/25 = 2 条
	if first.New["algo"] < 2 {
		t.Fatalf("上午应能排出 2 条新算法，实际 %d（复习没让路）", first.New["algo"])
	}
	// 复习被推到下午窗，仍然会发生（下午窗允许 algo）
	total := 0
	for _, d := range res.Days[:7] {
		total += d.Review["algo"]
	}
	if total == 0 {
		t.Fatal("算法复习应该被挪到下午窗，而不是被丢掉")
	}
}

// 关掉 NewOnly 时行为必须回到原样：上午先还复习债。
// 这条是回归保护 —— 免得以后把这个开关写成默认行为。
func TestNewOnlyOffStillDoesReviewFirst(t *testing.T) {
	in := windowInput(150)
	in.DueDay = "2026-06-30"
	in.Windows[0].NewOnly = false
	for i := 0; i < 20; i++ {
		in.Pending = append(in.Pending, Pending{
			Library: "algo",
			DueDay:  "2026-03-01",
			Stage:   0,
		})
	}

	res := Simulate(in)
	first := res.Days[0]
	// 上午被算法复习吃掉后，新算法条数应明显少于 NewOnly 模式
	if first.Review["algo"] == 0 {
		t.Fatal("NewOnly 关掉时，上午应该先还算法复习债")
	}
	if first.New["algo"] >= 2 {
		t.Fatalf("复习占满上午后不该还能排 2 条新算法，实际 %d", first.New["algo"])
	}
}

// ReviewOnly 与 NewOnly 同时为 true 时，NewOnly 应让该窗只做新内容，
// 但 ReviewOnly 又会拦住新内容 —— 结果就是这个窗什么都不做。
// 这种矛盾配置不该让程序崩，只是白给一段时间。
func TestBothFlagsConflict(t *testing.T) {
	in := windowInput(150)
	in.DueDay = "2026-06-30"
	in.Windows[0].ReviewOnly = true
	in.Windows[0].NewOnly = true
	res := Simulate(in)
	if len(res.Days) == 0 {
		t.Fatal("矛盾配置也不该崩")
	}
}
