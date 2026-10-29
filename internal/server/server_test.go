package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
			Slug    string `json:"slug"`
			Library string `json:"library"`
		} `json:"docs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}

	// 两个内容库都要在：小林coding 八股题 + 代码随想录算法教程。
	byLib := map[string]int{}
	for _, d := range m.Docs {
		if d.Slug == "" {
			t.Fatal("存在没有 slug 的文档")
		}
		byLib[d.Library]++
	}
	if byLib["interview"] != 16 {
		t.Fatalf("应有 16 篇八股文档，实际 %d", byLib["interview"])
	}
	if byLib["algo"] < 14 {
		t.Fatalf("应有至少 14 个算法专题，实际 %d", byLib["algo"])
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
