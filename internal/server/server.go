package server

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"learnsomething/internal/config"
	"learnsomething/internal/plan"
	"learnsomething/internal/store"
)

// Server 承载全部 HTTP 路由。
type Server struct {
	cfg           config.Config
	store         *store.Store
	content       embed.FS // content/manifest.json, content/docs/*.json, content/img/*
	web           embed.FS // web/dist/*
	manifestCache []byte

	loginMu    sync.Mutex
	loginFails map[string]*failRecord
}

type failRecord struct {
	count int
	until time.Time
}

// New 组装服务。
func New(cfg config.Config, st *store.Store, content, web embed.FS) (*Server, error) {
	s := &Server{cfg: cfg, store: st, content: content, web: web,
		loginFails: map[string]*failRecord{}}

	m, err := fs.ReadFile(fs.FS(content), "content/manifest.json")
	if err != nil {
		return nil, err
	}
	s.manifestCache = m

	// 把 docID → 内容库 交给 store：复习额度要按库分开算
	// （算法一条复习十几分钟，八股几分钟，共用一个上限会让算法永远排不进去）。
	if docs, err := contentDocs(content); err == nil {
		libs := make(map[string]string, len(docs))
		for slug, d := range docs {
			libs[slug] = d.Library
		}
		st.SetDocLibraries(libs)
	}
	return s, nil
}

// Handler 返回带中间件的路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 认证
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/me", s.handleMe)

	// 同步
	mux.HandleFunc("GET /api/snapshot", s.auth(s.handleSnapshot))
	mux.HandleFunc("PUT /api/progress", s.auth(s.handlePutProgress))
	mux.HandleFunc("POST /api/notes", s.auth(s.handleCreateNote))
	mux.HandleFunc("PUT /api/notes/{id}", s.auth(s.handleUpdateNote))
	mux.HandleFunc("DELETE /api/notes/{id}", s.auth(s.handleDeleteNote))

	// 学习任务
	mux.HandleFunc("PUT /api/goal", s.auth(s.handlePutGoal))
	mux.HandleFunc("DELETE /api/goal", s.auth(s.handleDeleteGoal))
	mux.HandleFunc("GET /api/plan", s.auth(s.handleStudyPlan))

	// 复习计划
	mux.HandleFunc("GET /api/review", s.auth(s.handleReviewPlan))
	mux.HandleFunc("GET /api/review/item", s.auth(s.handleReviewItem))
	mux.HandleFunc("POST /api/review/grade", s.auth(s.handleGradeReview))
	mux.HandleFunc("PUT /api/review/config", s.auth(s.handlePutReviewConfig))
	mux.HandleFunc("POST /api/review/reset", s.auth(s.handleResetReview))
	mux.HandleFunc("POST /api/review/backfill", s.auth(s.handleBackfillReviews))
	mux.HandleFunc("DELETE /api/review", s.auth(s.handleClearReviews))

	// 内容
	mux.HandleFunc("GET /api/content/manifest", s.handleManifest)
	mux.HandleFunc("GET /api/content/docs/{slug}", s.handleDoc)

	// 静态资源
	mux.Handle("GET /assets/img/", s.imageHandler())
	mux.Handle("GET /assets/", s.webHandler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/", s.spaHandler())

	return withRecover(withLogging(withSecurityHeaders(mux)))
}

// ---------- 认证 ----------

type loginReq struct {
	Password string `json:"password"`
}

type meResp struct {
	Authed  bool   `json:"authed"`
	Version string `json:"version"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不正确")
		return
	}

	ip := clientIP(r)
	if msg, blocked := s.checkLoginBlocked(ip); blocked {
		writeErr(w, http.StatusTooManyRequests, msg)
		return
	}

	if !constantTimeEqual(req.Password, s.cfg.Password) {
		s.recordLoginFail(ip)
		writeErr(w, http.StatusUnauthorized, "密码不正确")
		return
	}
	s.clearLoginFails(ip)

	exp := time.Now().Add(time.Duration(s.cfg.TokenTTL) * 24 * time.Hour)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "owner",
		"exp": exp.Unix(),
		"iat": time.Now().Unix(),
	})
	signed, err := tok.SignedString(s.cfg.JWTSecret)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "签发凭证失败")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "ls_token",
		Value:    signed,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.ForceTLS,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "expiresAt": exp.UTC().Format(time.RFC3339)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: "ls_token", Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.cfg.ForceTLS,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMe 不要求登录：未登录时返回 authed:false，前端据此跳转，
// 这样正常流程里不会有 401 打扰控制台。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, meResp{Authed: s.isAuthed(r), Version: Version})
}

// isAuthed 判断请求是否带有有效的登录 cookie。
func (s *Server) isAuthed(r *http.Request) bool {
	c, err := r.Cookie("ls_token")
	if err != nil || c.Value == "" {
		return false
	}
	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(c.Value, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("签名算法不符")
		}
		return s.cfg.JWTSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	return err == nil
}

// Version 由构建时注入，用于前端提示「有新版本」。
var Version = "dev"

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAuthed(r) {
			writeErr(w, http.StatusUnauthorized, "请先登录")
			return
		}
		next(w, r)
	}
}

// 登录失败节流：连续 5 次失败锁定 10 分钟，避免公网被爆破。
func (s *Server) checkLoginBlocked(ip string) (string, bool) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	rec, ok := s.loginFails[ip]
	if !ok {
		return "", false
	}
	if time.Now().Before(rec.until) {
		left := time.Until(rec.until).Round(time.Second)
		return "尝试次数过多，请 " + left.String() + " 后再试", true
	}
	return "", false
}

func (s *Server) recordLoginFail(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	rec, ok := s.loginFails[ip]
	// 只有「上一次封禁已经过期」时才清零重来；until 为零值时不能重置计数，
	// 否则每次失败都会新建记录，永远累计不到阈值。
	if !ok || (!rec.until.IsZero() && time.Now().After(rec.until)) {
		rec = &failRecord{}
		s.loginFails[ip] = rec
	}
	rec.count++
	if rec.count >= 5 {
		rec.until = time.Now().Add(10 * time.Minute)
		rec.count = 0
	}
}

func (s *Server) clearLoginFails(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	delete(s.loginFails, ip)
}

// ---------- 同步接口 ----------

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := s.store.Snapshot()
	if err != nil {
		s.fail(w, "读取数据", err)
		return
	}
	rev, _ := s.store.Meta("revision")
	snap.Revision = rev
	writeJSON(w, http.StatusOK, snap)
}

type progressReq struct {
	DocID   string `json:"docId"`
	ItemID  string `json:"itemId"`
	Status  *int   `json:"status"`
	Starred *bool  `json:"starred"`
}

func (s *Server) handlePutProgress(w http.ResponseWriter, r *http.Request) {
	var req progressReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if req.DocID == "" || req.ItemID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 docId 或 itemId")
		return
	}
	status := -1
	if req.Status != nil {
		if *req.Status < 0 || *req.Status > 2 {
			writeErr(w, http.StatusBadRequest, "status 只能是 0/1/2")
			return
		}
		status = *req.Status
	}

	today := time.Now().Format("2006-01-02")
	p, err := s.store.SetProgress(req.DocID, req.ItemID, status, req.Starred, today)
	if err != nil {
		s.fail(w, "保存进度", err)
		return
	}
	s.bumpRevision()
	writeJSON(w, http.StatusOK, p)
}

type noteReq struct {
	DocID   string `json:"docId"`
	ItemID  string `json:"itemId"`
	Content string `json:"content"`
}

func (s *Server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	var req noteReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if req.DocID == "" || req.ItemID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 docId 或 itemId")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeErr(w, http.StatusBadRequest, "笔记内容不能为空")
		return
	}
	if len([]rune(req.Content)) > 20000 {
		writeErr(w, http.StatusBadRequest, "笔记太长了（上限 20000 字）")
		return
	}
	n, err := s.store.CreateNote(req.DocID, req.ItemID, req.Content, time.Now().Format("2006-01-02"))
	if err != nil {
		s.fail(w, "保存笔记", err)
		return
	}
	s.bumpRevision()
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleUpdateNote(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "笔记 id 不合法")
		return
	}
	var req noteReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeErr(w, http.StatusBadRequest, "笔记内容不能为空")
		return
	}
	if len([]rune(req.Content)) > 20000 {
		writeErr(w, http.StatusBadRequest, "笔记太长了（上限 20000 字）")
		return
	}
	n, err := s.store.UpdateNote(id, req.Content)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "笔记不存在")
		return
	}
	if err != nil {
		s.fail(w, "更新笔记", err)
		return
	}
	s.bumpRevision()
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "笔记 id 不合法")
		return
	}
	err = s.store.DeleteNote(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "笔记不存在")
		return
	}
	if err != nil {
		s.fail(w, "删除笔记", err)
		return
	}
	s.bumpRevision()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) bumpRevision() {
	_ = s.store.SetMeta("revision", strconv.FormatInt(time.Now().UnixNano(), 36))
}

// ---------- 学习任务 ----------

type goalReq struct {
	DocSlugs    []string `json:"docSlugs"`
	DueDay      string   `json:"dueDay"`
	DailyTarget int      `json:"dailyTarget"`
	// DailyMinutes 是每天可投入的分钟数，计划推算的核心输入。
	DailyMinutes int `json:"dailyMinutes"`
	// RestDays 是一次性停滞日，WeeklyRest 是每周固定休息（0=周日）。
	RestDays   []string `json:"restDays"`
	WeeklyRest []int    `json:"weeklyRest"`
	// MinutesPerItem 各库学一条的分钟数（算法远高于八股）。
	MinutesPerItem map[string]int `json:"minutesPerItem"`
	// Windows 是每日时间窗，为空时按 DailyMinutes 一个总预算算。
	Windows []store.PlanWindow `json:"windows"`
}

var dayRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// defaultMinutesPerItem 是没指定时各类内容的经验值（分钟/条）。
//
// 算法一篇长文要读、要自己想、还要写代码验证，和「看一眼答案就懂」的
// 八股题完全不是一个量级；用同一个数字排计划必然失真。
var defaultMinutesPerItem = map[string]int{
	"interview": 5,
	"algo":      25,
	"ai":        8,
}

func (s *Server) handlePutGoal(w http.ResponseWriter, r *http.Request) {
	var req goalReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if len(req.DocSlugs) == 0 {
		writeErr(w, http.StatusBadRequest, "至少要选择一个模块")
		return
	}
	if !dayRe.MatchString(req.DueDay) {
		writeErr(w, http.StatusBadRequest, "截止日期格式应为 YYYY-MM-DD")
		return
	}
	// 截止日期必须在今天或之后，否则「剩余天数」会变成负数。
	if _, err := time.ParseInLocation("2006-01-02", req.DueDay, time.Local); err != nil {
		writeErr(w, http.StatusBadRequest, "截止日期不是有效日期")
		return
	}
	today := time.Now().Format("2006-01-02")
	if req.DueDay < today {
		writeErr(w, http.StatusBadRequest, "截止日期不能早于今天")
		return
	}
	if req.DailyTarget < 0 {
		writeErr(w, http.StatusBadRequest, "每日目标不能为负数")
		return
	}
	if req.DailyMinutes < 0 || req.DailyMinutes > 1440 {
		writeErr(w, http.StatusBadRequest, "每天可用时间应在 0~1440 分钟之间")
		return
	}
	for _, d := range req.RestDays {
		if !dayRe.MatchString(d) {
			writeErr(w, http.StatusBadRequest, "停滞日格式应为 YYYY-MM-DD")
			return
		}
	}
	for _, wd := range req.WeeklyRest {
		if wd < 0 || wd > 6 {
			writeErr(w, http.StatusBadRequest, "每周休息日应为 0~6（0=周日）")
			return
		}
	}
	for lib, m := range req.MinutesPerItem {
		if m < 1 || m > 600 {
			writeErr(w, http.StatusBadRequest, "「"+lib+"」的每条分钟数应在 1~600 之间")
			return
		}
	}
	for _, win := range req.Windows {
		if win.Name == "" {
			writeErr(w, http.StatusBadRequest, "时间窗必须有名称")
			return
		}
		if win.Minutes < 0 || win.Minutes > 1440 {
			writeErr(w, http.StatusBadRequest, "时间窗「"+win.Name+"」的分钟数应在 0~1440 之间")
			return
		}
	}

	goal, err := s.store.SetGoal(store.Goal{
		DocSlugs:       req.DocSlugs,
		StartDay:       today,
		DueDay:         req.DueDay,
		DailyTarget:    req.DailyTarget,
		DailyMinutes:   req.DailyMinutes,
		RestDays:       req.RestDays,
		WeeklyRest:     req.WeeklyRest,
		MinutesPerItem: req.MinutesPerItem,
		Windows:        req.Windows,
	})
	if err != nil {
		s.fail(w, "保存学习任务", err)
		return
	}
	s.bumpRevision()
	writeJSON(w, http.StatusOK, goal)
}

func (s *Server) handleDeleteGoal(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearGoal(); err != nil {
		s.fail(w, "删除学习任务", err)
		return
	}
	s.bumpRevision()
	w.WriteHeader(http.StatusNoContent)
}

// ---------- 学习计划推算 ----------

// handleStudyPlan 按当前目标 + 真实剩余量逐日推演出学习计划。
//
// 为什么要放服务端算：计划依赖「已掌握多少条」「已排了多少复习」这类
// 只有服务端掌握的事实；而「学习和复习抢时间」的推演要跑几十上百天，
// 前端算会卡。
func (s *Server) handleStudyPlan(w http.ResponseWriter, r *http.Request) {
	goal, err := s.store.GetGoal()
	if err != nil {
		s.fail(w, "读取学习任务", err)
		return
	}
	if goal == nil {
		writeJSON(w, http.StatusOK, map[string]any{"hasGoal": false})
		return
	}

	// 用 manifest 里每个模块的条数 - 已掌握数 = 真实剩余量。
	docs, err := contentDocs(s.content)
	if err != nil {
		s.fail(w, "读取内容清单", err)
		return
	}
	doneBySlug, err := s.store.DoneCountByDoc()
	if err != nil {
		s.fail(w, "统计进度", err)
		return
	}

	// 把勾选的模块按内容库聚合（算法与八股的学习速度不同，要分开算）。
	libRemaining := map[string]int{}
	libReviewCap := map[string]int{}
	for _, slug := range goal.DocSlugs {
		d, ok := docs[slug]
		if !ok || d.ReadingList {
			continue
		}
		left := d.QuestionCount - doneBySlug[slug]
		if left < 0 {
			left = 0
		}
		if left == 0 {
			continue
		}
		libRemaining[d.Library] += left
		// 复习额度按库分配：算法复习一条要 15 分钟，不能让 40 条把它压死。
		libReviewCap[d.Library] = defaultReviewCap(d.Library)
	}

	in := plan.Input{
		StartDay:     time.Now().Format("2006-01-02"),
		DueDay:       goal.DueDay,
		DailyMinutes: goal.DailyMinutes,
		RestDays:     goal.RestDays,
		WeeklyRest:   goal.WeeklyRest,
	}

	// 时间窗：有配置就按窗排期（上午算法 / 下午复习+八股），
	// 否则退化成「一天一个总预算」。这一步不做的话，界面算出来的算法进度
	// 会明显乐观 —— 它会把算法摊到全天，而用户下午根本啃不动算法。
	for _, w := range goal.Windows {
		if w.Minutes <= 0 {
			continue
		}
		in.Windows = append(in.Windows, plan.Window{
			Name:       w.Name,
			Minutes:    w.Minutes,
			ReviewOnly: w.ReviewOnly,
			Libs:       w.Libs,
		})
	}
	// 有窗时总预算取各窗之和，避免 Recommend 拿 DailyMinutes 算出错误结论
	if len(in.Windows) > 0 {
		total := 0
		for _, w := range in.Windows {
			total += w.Minutes
		}
		in.DailyMinutes = total
	}
	if in.DailyMinutes <= 0 {
		in.DailyMinutes = 120
	}
	if goal.StartDay != "" && goal.StartDay < in.StartDay {
		// 计划总是从今天往后算，历史天数没有意义
		in.StartDay = time.Now().Format("2006-01-02")
	}

	for lib, left := range libRemaining {
		minutes := goal.MinutesPerItem[lib]
		if minutes <= 0 {
			minutes = defaultMinutesPerItem[lib]
		}
		if minutes <= 0 {
			minutes = 5
		}
		in.Libs = append(in.Libs, plan.LibInput{
			Library:        lib,
			Remaining:      left,
			MinutesPerItem: minutes,
			ReviewCap:      libReviewCap[lib],
			// 算法这类需要清醒头脑的内容，只允许排在专属时间窗（上午）。
			OnlyWindow: onlyWindowOf(goal.Windows, lib),
		})
	}

	// 已有的复习债必须算进去，否则会严重低估后期负担。
	active, err := s.store.ActiveReviews()
	if err != nil {
		s.fail(w, "读取复习计划", err)
		return
	}
	for _, it := range active {
		if it.DueDay == "" || it.DueDay < in.StartDay {
			continue // 已逾期的当成今天到期
		}
		lib := docLibrary(docs, it.DocID)
		in.Pending = append(in.Pending, plan.Pending{
			Library: lib,
			DueDay:  it.DueDay,
			Stage:   it.Stage,
		})
	}

	res := plan.Simulate(in)
	rec := plan.Recommend(in)

	writeJSON(w, http.StatusOK, map[string]any{
		"hasGoal": true,
		"goal":    goal,
		"plan":    res,
		"advice":  rec,
		"libs":    libRemaining,
	})
}

// onlyWindowOf 判断某库是否被某个时间窗「独占」。
//
// 若只有一个窗允许这个库的新内容，就把该窗名作为它的 OnlyWindow，
// 保证排期只往那里放。没有窗、或有多个窗可选时返回空（不限）。
func onlyWindowOf(windows []store.PlanWindow, lib string) string {
	var found []string
	for _, w := range windows {
		if w.ReviewOnly || w.Minutes <= 0 {
			continue
		}
		if len(w.Libs) == 0 {
			continue // 不限库的窗不构成独占
		}
		for _, l := range w.Libs {
			if l == lib {
				found = append(found, w.Name)
				break
			}
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	return ""
}

// defaultReviewCap 是各库每天最多安排多少条复习。
//
// 算法一条复习要十几分钟，若和八股共用 40 条的额度，
// 光复习就吃掉整天时间，新内容永远推不动。
func defaultReviewCap(library string) int {
	switch library {
	case "algo":
		return 6
	case "ai":
		return 15
	default:
		return 30
	}
}

// docLibrary 查出某篇文档属于哪个内容库。
func docLibrary(docs map[string]docMetaLite, slug string) string {
	if d, ok := docs[slug]; ok {
		return d.Library
	}
	return "interview"
}

// docMetaLite 是计划推算需要的文档元信息。
type docMetaLite struct {
	Slug         string `json:"slug"`
	Library      string `json:"library"`
	QuestionCount int   `json:"questionCount"`
	ReadingList  bool   `json:"readingList"`
}

// contentDocs 从已缓存的 manifest 解析出 slug → 元信息。
func contentDocs(content embed.FS) (map[string]docMetaLite, error) {
	raw, err := fs.ReadFile(fs.FS(content), "content/manifest.json")
	if err != nil {
		return nil, err
	}
	var m struct {
		Docs []docMetaLite `json:"docs"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := make(map[string]docMetaLite, len(m.Docs))
	for _, d := range m.Docs {
		out[d.Slug] = d
	}
	return out, nil
}

// ---------- 复习计划 ----------

// handleReviewPlan 返回「今天该复习什么」：队列、逾期积压、未来负载。
//
// ahead=N 表示顺带把未来 N 天内到期的内容也拉进队列（提前复习），
// 用于今天恰好没有到期内容、但用户仍想推进的场景。
func (s *Server) handleReviewPlan(w http.ResponseWriter, r *http.Request) {
	day := r.URL.Query().Get("day")
	if day != "" && !dayRe.MatchString(day) {
		writeErr(w, http.StatusBadRequest, "day 格式应为 YYYY-MM-DD")
		return
	}
	ahead := 0
	if v := r.URL.Query().Get("ahead"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 30 {
			writeErr(w, http.StatusBadRequest, "ahead 应为 0~30 的整数")
			return
		}
		ahead = n
	}
	plan, err := s.store.ReviewPlanAhead(day, ahead)
	if err != nil {
		s.fail(w, "读取复习计划", err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// handleReviewItem 查单条内容的复习状态，供阅读页显示「下次复习 X 天后」。
// 不在计划里时返回 200 + enrolled:false，前端据此决定是否显示提示。
func (s *Server) handleReviewItem(w http.ResponseWriter, r *http.Request) {
	docID := r.URL.Query().Get("docId")
	itemID := r.URL.Query().Get("itemId")
	if docID == "" || itemID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 docId 或 itemId")
		return
	}
	it, err := s.store.GetReview(docID, itemID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"enrolled": false})
		return
	}
	if err != nil {
		s.fail(w, "读取复习状态", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enrolled": true, "item": it})
}

type gradeReq struct {
	DocID  string `json:"docId"`
	ItemID string `json:"itemId"`
	Grade  int    `json:"grade"`
}

func (s *Server) handleGradeReview(w http.ResponseWriter, r *http.Request) {
	var req gradeReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if req.DocID == "" || req.ItemID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 docId 或 itemId")
		return
	}
	if req.Grade < store.GradeAgain || req.Grade > store.GradeDefer {
		writeErr(w, http.StatusBadRequest, "grade 只能是 0/1/2/3")
		return
	}
	it, err := s.store.GradeReview(req.DocID, req.ItemID, req.Grade, time.Now().Format("2006-01-02"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "这条内容不在复习计划里")
		return
	}
	if err != nil {
		s.fail(w, "记录复习", err)
		return
	}
	s.bumpRevision()
	writeJSON(w, http.StatusOK, it)
}

type reviewConfigReq struct {
	DailyCap int  `json:"dailyCap"`
	Enabled  bool `json:"enabled"`
	// LibCap 各内容库的每日复习条数，缺省时用默认值。
	LibCap map[string]int `json:"libCap"`
}

func (s *Server) handlePutReviewConfig(w http.ResponseWriter, r *http.Request) {
	var req reviewConfigReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if req.DailyCap <= 0 {
		writeErr(w, http.StatusBadRequest, "每日复习上限必须大于 0")
		return
	}
	if req.DailyCap > 500 {
		writeErr(w, http.StatusBadRequest, "每日复习上限最多 500 条")
		return
	}
	for lib, v := range req.LibCap {
		if v < 1 || v > 500 {
			writeErr(w, http.StatusBadRequest, "「"+lib+"」的每日复习条数应在 1~500 之间")
			return
		}
	}
	cfg, err := s.store.SetReviewConfig(store.ReviewConfig{
		DailyCap: req.DailyCap,
		Enabled:  req.Enabled,
		LibCap:   req.LibCap,
	})
	if err != nil {
		s.fail(w, "保存复习设置", err)
		return
	}
	s.bumpRevision()
	writeJSON(w, http.StatusOK, cfg)
}

type reviewItemReq struct {
	DocID  string `json:"docId"`
	ItemID string `json:"itemId"`
}

func (s *Server) handleResetReview(w http.ResponseWriter, r *http.Request) {
	var req reviewItemReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if req.DocID == "" || req.ItemID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 docId 或 itemId")
		return
	}
	if err := s.store.ResetReviewItem(req.DocID, req.ItemID, time.Now().Format("2006-01-02")); err != nil {
		s.fail(w, "重置复习进度", err)
		return
	}
	s.bumpRevision()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClearReviews(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearReviews(); err != nil {
		s.fail(w, "清空复习计划", err)
		return
	}
	s.bumpRevision()
	w.WriteHeader(http.StatusNoContent)
}

// handleBackfillReviews 把「已掌握」但还没进复习计划的内容批量纳入。
//
// 用途：升级到带复习模块的版本后，升级前的历史进度不会被自动扫到，
// 用户点一下就能全部补齐，而不用一条条重新标记。
func (s *Server) handleBackfillReviews(w http.ResponseWriter, r *http.Request) {
	res, err := s.store.BackfillReviews(time.Now().Format("2006-01-02"))
	if err != nil {
		s.fail(w, "纳入复习计划", err)
		return
	}
	if res.Added > 0 {
		s.bumpRevision()
	}
	writeJSON(w, http.StatusOK, res)
}

// ---------- 内容接口 ----------

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	etag := `"` + contentETag(s.manifestCache) + `"`
	if match := r.Header.Get("If-None-Match"); match != "" {
		for _, part := range strings.Split(match, ",") {
			if strings.TrimSpace(part) == etag || strings.TrimSpace(part) == "*" {
				w.Header().Set("ETag", etag)
				w.Header().Set("Cache-Control", "no-cache")
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(s.manifestCache)
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !validSlug(slug) {
		writeErr(w, http.StatusBadRequest, "slug 不合法")
		return
	}
	data, err := fs.ReadFile(fs.FS(s.content), "content/docs/"+slug+".json")
	if err != nil {
		writeErr(w, http.StatusNotFound, "文档不存在")
		return
	}

	// 内容跟镜像版本绑定，所以用 ETag 做协商缓存：
	// 每次都要回来问一次（no-cache），内容没变就返回 304，变了立刻拿到新内容。
	// 早期版本用了 max-age=3600，导致重抓内容后浏览器还在用旧 JSON（比如旧版里被截断的正文）。
	etag := `"` + contentETag(data) + `"`
	if match := r.Header.Get("If-None-Match"); match != "" {
		for _, part := range strings.Split(match, ",") {
			if strings.TrimSpace(part) == etag || strings.TrimSpace(part) == "*" {
				w.Header().Set("ETag", etag)
				w.Header().Set("Cache-Control", "no-cache")
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// contentETag 用长度 + 内容哈希生成稳定的 ETag。
func contentETag(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:12])
}

func validSlug(slug string) bool {
	if slug == "" || len(slug) > 40 {
		return false
	}
	for _, r := range slug {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}
	return true
}

func (s *Server) imageHandler() http.Handler {
	sub, err := fs.Sub(fs.FS(s.content), "content/img")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/assets/img/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 图片名带内容哈希，可以长期缓存。
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		files.ServeHTTP(w, r)
	})
}

func (s *Server) webHandler() http.Handler {
	sub, err := fs.Sub(fs.FS(s.web), "web/dist/assets")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.StripPrefix("/assets/", files).ServeHTTP(w, r)
	})
}

// spaHandler 提供前端页面，未知路径回退到 index.html（前端路由）。
func (s *Server) spaHandler() http.HandlerFunc {
	sub, err := fs.Sub(fs.FS(s.web), "web/dist")
	if err != nil {
		panic(err)
	}
	// index.html 必须每次重新校验：它里面引用的资源文件名带 hash，
	// 升级镜像后旧的 index.html 会让浏览器继续加载旧 JS。
	indexHTML, _ := fs.ReadFile(sub, "index.html")
	indexETag := `"` + contentETag(indexHTML) + `"`

	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, "接口不存在")
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		name := strings.TrimPrefix(clean, "/")
		if name == "" {
			name = "index.html"
		}
		f, err := sub.Open(name)
		if err == nil {
			defer f.Close()
			if st, serr := f.Stat(); serr == nil && st.IsDir() {
				name = path.Join(name, "index.html")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
				http.ServeFileFS(w, r, sub, name)
				return
			}
		}

		// 回退到 index.html
		if len(indexHTML) == 0 {
			writeErr(w, http.StatusNotFound, "前端资源未构建")
			return
		}
		if match := r.Header.Get("If-None-Match"); match != "" && match == indexETag {
			w.Header().Set("ETag", indexETag)
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", indexETag)
		_, _ = w.Write(indexHTML)
	}
}

// ---------- 工具 ----------

func decodeJSON(r *http.Request, v any) error {
	defer io.Copy(io.Discard, r.Body)
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	slog.Error(what+"失败", "err", err)
	writeErr(w, http.StatusInternalServerError, what+"失败")
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return host
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		// 仍然走一次比较，避免长度差异导致的时间差。
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$abcdefghijklmnopqrstuv"), []byte(a))
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

var _ = os.Getenv
