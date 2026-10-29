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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"learnsomething/internal/config"
	"learnsomething/internal/store"
)

// Server 承载全部 HTTP 路由。
type Server struct {
	cfg     config.Config
	store   *store.Store
	content embed.FS // content/manifest.json, content/docs/*.json, content/img/*
	web     embed.FS // web/dist/*
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
