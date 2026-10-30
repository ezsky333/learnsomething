package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	ls "learnsomething"
	"learnsomething/internal/config"
	"learnsomething/internal/store"
)

func newTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := config.Config{
		Addr:      ":0",
		DataDir:   t.TempDir(),
		Password:  "secret",
		JWTSecret: []byte("test-secret-key"),
		TokenTTL:  1,
	}
	s, err := New(cfg, st, ls.ContentFS, ls.WebFS)
	if err != nil {
		t.Fatal(err)
	}
	return s, s.Handler()
}

func do(t *testing.T, h http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func login(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	w := do(t, h, "POST", "/api/login", `{"password":"secret"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("登录失败: %d %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "ls_token" {
			return c
		}
	}
	t.Fatal("没有下发 cookie")
	return nil
}

func TestLoginFlow(t *testing.T) {
	_, h := newTestServer(t)

	if w := do(t, h, "POST", "/api/login", `{"password":"wrong"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("错误密码应 401，实际 %d", w.Code)
	}
	if w := do(t, h, "POST", "/api/login", `{"password":"secret"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("正确密码应 200，实际 %d", w.Code)
	}
	cookie := login(t, h)
	if w := do(t, h, "GET", "/api/me", "", cookie); w.Code != http.StatusOK {
		t.Fatalf("/api/me 应 200，实际 %d", w.Code)
	}
}

func TestMeWithoutAuth(t *testing.T) {
	_, h := newTestServer(t)
	w := do(t, h, "GET", "/api/me", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("未登录 /api/me 也应 200（返回 authed:false），实际 %d", w.Code)
	}
	var body meResp
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Authed {
		t.Fatal("未登录时 authed 应为 false")
	}
}

func TestProtectedRequiresAuth(t *testing.T) {
	_, h := newTestServer(t)
	for _, path := range []string{"/api/snapshot"} {
		if w := do(t, h, "GET", path, "", nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s 未登录应 401，实际 %d", path, w.Code)
		}
	}
	if w := do(t, h, "PUT", "/api/progress", `{"docId":"a","itemId":"b","status":2}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("PUT /api/progress 未登录应 401，实际 %d", w.Code)
	}
}

func TestProgressAndNotesViaAPI(t *testing.T) {
	_, h := newTestServer(t)
	cookie := login(t, h)

	w := do(t, h, "PUT", "/api/progress", `{"docId":"java","itemId":"概念","status":2}`, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("保存进度失败: %d %s", w.Code, w.Body.String())
	}
	var p store.Progress
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Status != 2 || p.DoneDay == "" {
		t.Fatalf("返回的进度不对: %+v", p)
	}

	// 非法 status
	if w := do(t, h, "PUT", "/api/progress", `{"docId":"java","itemId":"x","status":9}`, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("非法 status 应 400，实际 %d", w.Code)
	}
	// 缺少字段
	if w := do(t, h, "PUT", "/api/progress", `{"status":1}`, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 docId 应 400，实际 %d", w.Code)
	}

	// 新建笔记
	w = do(t, h, "POST", "/api/notes", `{"docId":"java","itemId":"概念","content":"这是一条笔记"}`, cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("新建笔记失败: %d %s", w.Code, w.Body.String())
	}
	var n store.Note
	if err := json.Unmarshal(w.Body.Bytes(), &n); err != nil {
		t.Fatal(err)
	}

	// 空内容应拒绝
	if w := do(t, h, "POST", "/api/notes", `{"docId":"java","itemId":"概念","content":"   "}`, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("空笔记应 400，实际 %d", w.Code)
	}

	// 快照里两样都在
	w = do(t, h, "GET", "/api/snapshot", "", cookie)
	var snap store.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Progress) != 1 || len(snap.Notes) != 1 {
		t.Fatalf("快照内容不对: %+v", snap)
	}
	if snap.Revision == "" {
		t.Fatal("修改后应产生 revision")
	}

	// 删除笔记
	if w := do(t, h, "DELETE", "/api/notes/"+itoa(n.ID), "", cookie); w.Code != http.StatusNoContent {
		t.Fatalf("删除笔记应 204，实际 %d", w.Code)
	}
	if w := do(t, h, "DELETE", "/api/notes/"+itoa(n.ID), "", cookie); w.Code != http.StatusNotFound {
		t.Fatalf("重复删除应 404，实际 %d", w.Code)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestContentEndpoints(t *testing.T) {
	_, h := newTestServer(t)

	w := do(t, h, "GET", "/api/content/manifest", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("manifest 应 200，实际 %d", w.Code)
	}
	var m struct {
		Docs []struct {
			Slug        string `json:"slug"`
			Library     string `json:"library"`
			ReadingList bool   `json:"readingList"`
		} `json:"docs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}

	// 两个内容库都要在：小林coding 八股题 + 代码随想录算法教程。
	byLib := map[string]int{}
	readingLists := 0
	for _, d := range m.Docs {
		if d.Slug == "" {
			t.Fatal("存在没有 slug 的文档")
		}
		byLib[d.Library]++
		if d.ReadingList {
			readingLists++
		}
	}
	if byLib["interview"] < 24 {
		t.Fatalf("应有至少 24 篇八股文档，实际 %d", byLib["interview"])
	}
	if byLib["algo"] < 14 {
		t.Fatalf("应有至少 14 个算法专题，实际 %d", byLib["algo"])
	}
	// 「后端技术进阶之路」这类只含外链目录的文档要标记为阅读清单，
	// 否则会把几十条几乎没有正文的条目算进学习任务的理论进度。
	if readingLists < 1 {
		t.Fatalf("应至少标记 1 篇阅读清单文档，实际 %d", readingLists)
	}

	// 两个库各抽查一篇，确认能正常取到正文。
	for _, slug := range []string{"java", "algo-array"} {
		if w := do(t, h, "GET", "/api/content/docs/"+slug, "", nil); w.Code != http.StatusOK {
			t.Fatalf("doc %s 应 200，实际 %d", slug, w.Code)
		}
	}
	if w := do(t, h, "GET", "/api/content/docs/nope", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("不存在的 doc 应 404，实际 %d", w.Code)
	}
	// 路径穿越必须被拒绝。Go 的 ServeMux 会先把 /a/../b 清洗成 /a/b 并返回 307，
	// 清洗后若仍落在 docs 路由下就会走 validSlug 校验，因此 3xx 也是安全的。
	for _, bad := range []string{"../manifest", "a/b", "JAVA", "a.b"} {
		code := do(t, h, "GET", "/api/content/docs/"+bad, "", nil).Code
		if code == http.StatusOK {
			t.Fatalf("非法 slug %q 不应返回文档内容", bad)
		}
	}
}

func TestImageServed(t *testing.T) {
	_, h := newTestServer(t)
	w := do(t, h, "GET", "/assets/img/493c0c2391b388b6.webp", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("图片应 200，实际 %d", w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Cache-Control"), "public") {
		t.Fatalf("图片应带长缓存头，实际 %q", w.Header().Get("Cache-Control"))
	}
}

func TestUnknownAPIIs404(t *testing.T) {
	_, h := newTestServer(t)
	if w := do(t, h, "GET", "/api/nope", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("未知接口应 404，实际 %d", w.Code)
	}
}

// 学习任务：创建 / 读取 / 去重 / 校验 / 删除。
func TestGoalLifecycle(t *testing.T) {
	_, h := newTestServer(t)
	cookie := login(t, h)

	due := time.Now().AddDate(0, 0, 10).Format("2006-01-02")

	// 未设置时快照里 goal 为 null
	w := do(t, h, "GET", "/api/snapshot", "", cookie)
	var snap store.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Goal != nil {
		t.Fatalf("初始不应有任务，实际 %+v", snap.Goal)
	}

	// 创建任务（含重复 slug，应被去重）
	body := `{"docSlugs":["java","java","algo-array"],"dueDay":"` + due + `","dailyTarget":0}`
	w = do(t, h, "PUT", "/api/goal", body, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("创建任务失败: %d %s", w.Code, w.Body.String())
	}
	var g store.Goal
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.DocSlugs) != 2 {
		t.Fatalf("重复 slug 应被去重成 2 个，实际 %d: %v", len(g.DocSlugs), g.DocSlugs)
	}
	if g.StartDay == "" || g.DueDay != due {
		t.Fatalf("日期不对: start=%q due=%q", g.StartDay, g.DueDay)
	}
	if g.CreatedAt == "" || g.UpdatedAt == "" {
		t.Fatalf("时间戳缺失: %+v", g)
	}

	// 重新设置时应保留原 createdAt
	createdAt := g.CreatedAt
	body2 := `{"docSlugs":["redis"],"dueDay":"` + due + `","dailyTarget":8}`
	w = do(t, h, "PUT", "/api/goal", body2, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("更新任务失败: %d", w.Code)
	}
	var g2 store.Goal
	_ = json.Unmarshal(w.Body.Bytes(), &g2)
	if g2.CreatedAt != createdAt {
		t.Fatalf("更新应保留 createdAt: %q -> %q", createdAt, g2.CreatedAt)
	}
	if g2.DailyTarget != 8 || len(g2.DocSlugs) != 1 {
		t.Fatalf("更新结果不对: %+v", g2)
	}

	// 快照里能读到
	w = do(t, h, "GET", "/api/snapshot", "", cookie)
	snapChk := store.Snapshot{}
	_ = json.Unmarshal(w.Body.Bytes(), &snapChk)
	if snapChk.Goal == nil || snapChk.Goal.DueDay != due {
		t.Fatalf("快照未包含任务: %+v", snapChk.Goal)
	}
	if snapChk.Revision == "" {
		t.Fatal("设置任务后应有 revision")
	}

	// 删除
	if w := do(t, h, "DELETE", "/api/goal", "", cookie); w.Code != http.StatusNoContent {
		t.Fatalf("删除任务应 204，实际 %d", w.Code)
	}
	w = do(t, h, "GET", "/api/snapshot", "", cookie)
	snapChk = store.Snapshot{}
	_ = json.Unmarshal(w.Body.Bytes(), &snapChk)
	if snapChk.Goal != nil {
		t.Fatalf("删除后不应有任务: %+v", snapChk.Goal)
	}
}

func TestGoalValidation(t *testing.T) {
	_, h := newTestServer(t)
	cookie := login(t, h)

	future := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	past := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	cases := []struct {
		name string
		body string
		want int
	}{
		{"空模块", `{"docSlugs":[],"dueDay":"` + future + `"}`, http.StatusBadRequest},
		{"缺截止日", `{"docSlugs":["java"]}`, http.StatusBadRequest},
		{"日期格式错", `{"docSlugs":["java"],"dueDay":"2026/1/1"}`, http.StatusBadRequest},
		{"日期不存在", `{"docSlugs":["java"],"dueDay":"2026-02-31"}`, http.StatusBadRequest},
		{"过去日期", `{"docSlugs":["java"],"dueDay":"` + past + `"}`, http.StatusBadRequest},
		{"负数每日目标", `{"docSlugs":["java"],"dueDay":"` + future + `","dailyTarget":-5}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if w := do(t, h, "PUT", "/api/goal", c.body, cookie); w.Code != c.want {
			t.Fatalf("%s 应 %d，实际 %d (%s)", c.name, c.want, w.Code, w.Body.String())
		}
	}

	// 未登录不能设置
	if w := do(t, h, "PUT", "/api/goal", `{"docSlugs":["java"],"dueDay":"`+future+`"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，实际 %d", w.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	_, h := newTestServer(t)
	for i := 0; i < 5; i++ {
		do(t, h, "POST", "/api/login", `{"password":"wrong"}`, nil)
	}
	w := do(t, h, "POST", "/api/login", `{"password":"wrong"}`, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("连续失败后应 429，实际 %d", w.Code)
	}
}
