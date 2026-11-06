package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"learnsomething/internal/store"
)

// 换题库后，旧任务里残留的归档模块必须被剔除并如实告知。
//
// 这条路径很容易写错：如果直接跳过归档模块又不吭声，用户会看到
// 「八股怎么从计划里消失了」，却没有任何解释。
func TestGoalDropsArchivedModules(t *testing.T) {
	_, h := newTestServer(t)
	cookie := login(t, h)

	due := time.Now().AddDate(0, 2, 0).Format("2006-01-02")
	// redis 是已归档的旧题库文档，jds-jvm 是现役模块
	body := `{"docSlugs":["redis","jds-jvm"],"dueDay":"` + due + `","dailyMinutes":120,
		"minutesPerItem":{"jds":9,"interview":5}}`
	if w := do(t, h, "PUT", "/api/goal", body, cookie); w.Code != http.StatusOK {
		t.Fatalf("保存任务失败: %d %s", w.Code, w.Body.String())
	}

	w := do(t, h, "GET", "/api/snapshot", "", cookie)
	var snap store.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Goal == nil {
		t.Fatal("应仍有任务（现役模块还在）")
	}
	if len(snap.Goal.DocSlugs) != 1 || snap.Goal.DocSlugs[0] != "jds-jvm" {
		t.Fatalf("归档模块应被剔除，只剩 jds-jvm，实际 %v", snap.Goal.DocSlugs)
	}
	if len(snap.Goal.ArchivedRefs) != 1 || snap.Goal.ArchivedRefs[0] != "redis" {
		t.Fatalf("应记下被剔除的归档模块，实际 %v", snap.Goal.ArchivedRefs)
	}
}

// 全部模块都归档时，等价于没有任务，界面应回到「请设置任务」而不是空白计划。
func TestGoalAllArchivedCountsAsNone(t *testing.T) {
	_, h := newTestServer(t)
	cookie := login(t, h)

	due := time.Now().AddDate(0, 2, 0).Format("2006-01-02")
	body := `{"docSlugs":["redis","mysql"],"dueDay":"` + due + `","dailyMinutes":120}`
	if w := do(t, h, "PUT", "/api/goal", body, cookie); w.Code != http.StatusOK {
		t.Fatalf("保存任务失败: %d", w.Code)
	}

	w := do(t, h, "GET", "/api/plan", "", cookie)
	var resp struct {
		HasGoal bool `json:"hasGoal"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.HasGoal {
		t.Fatal("全是归档模块时不该当作有效任务")
	}
}

// 归档题库的复习不应进入队列：换库后旧题抢额度，新库就永远排不上。
func TestArchivedReviewsExcludedFromQueue(t *testing.T) {
	_, h := newTestServer(t)
	cookie := login(t, h)

	// 旧题库（redis）与新库（jds-jvm）各标记一条已掌握
	for _, doc := range []string{"redis", "jds-jvm"} {
		if w := do(t, h, "PUT", "/api/progress",
			`{"docId":"`+doc+`","itemId":"q1","status":2}`, cookie); w.Code != http.StatusOK {
			t.Fatalf("标记掌握失败: %d", w.Code)
		}
	}

	w := do(t, h, "GET", "/api/review", "", cookie)
	var plan store.ReviewPlan
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	// 在籍量只算现役内容：redis 那条已被归档排除
	if plan.Enrolled != 1 {
		t.Fatalf("在籍应只有现役的 1 条，实际 %d", plan.Enrolled)
	}
	for _, it := range plan.Queue {
		if it.DocID == "redis" {
			t.Fatalf("归档题库不该出现在复习队列里: %+v", it)
		}
	}
}

// 补录时也要跳过归档内容，否则会把退役题库整批塞进队列。
func TestBackfillSkipsArchived(t *testing.T) {
	_, h := newTestServer(t)
	cookie := login(t, h)

	for _, doc := range []string{"redis", "mysql", "jds-jvm"} {
		if w := do(t, h, "PUT", "/api/progress",
			`{"docId":"`+doc+`","itemId":"b1","status":2}`, cookie); w.Code != http.StatusOK {
			t.Fatalf("标记掌握失败: %d", w.Code)
		}
	}
	// 先清空复习计划，制造「有进度但没入列」的状态
	if w := do(t, h, "DELETE", "/api/review", "", cookie); w.Code != http.StatusNoContent {
		t.Fatalf("清空复习失败: %d", w.Code)
	}

	w := do(t, h, "POST", "/api/review/backfill", "", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("补录失败: %d %s", w.Code, w.Body.String())
	}
	var res store.BackfillResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 {
		t.Fatalf("只该补录现役的 1 条，实际 %d", res.Added)
	}
	if res.Total != 1 {
		t.Fatalf("总数也只该算现役内容，实际 %d", res.Total)
	}
}
