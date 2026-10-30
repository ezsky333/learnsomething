package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// 进度状态。
const (
	StatusUnread     = 0 // 没开始
	StatusInProgress = 1 // 学过但没吃透
	StatusDone       = 2 // 已掌握
)

// Progress 是一道题的学习状态。
type Progress struct {
	DocID     string `json:"docId"`
	ItemID    string `json:"itemId"`
	Status    int    `json:"status"`
	Starred   bool   `json:"starred"`
	UpdatedAt string `json:"updatedAt"`
	DoneDay   string `json:"doneDay,omitempty"`
}

// Note 是一条笔记（同一道题可以有多条）。
type Note struct {
	ID        int64  `json:"id"`
	DocID     string `json:"docId"`
	ItemID    string `json:"itemId"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	CreatedDy string `json:"createdDay"`
}

// Goal 是学习任务：选中的模块 + 截止日期 + 每天的可用时间。
//
// 「每天该学多少」不落库，由服务端按时间预算逐日推算 —— 因为学习和复习
// 争抢同一份时间，静态的「总量/天数」无法反映「后期复习变重、能学的新内容变少」
// 这条曲线。
type Goal struct {
	// DocSlugs 是勾选要学的模块（一个 doc = 一个模块，算法专题也算模块）。
	DocSlugs []string `json:"docSlugs"`
	// StartDay / DueDay 都是本地日期 YYYY-MM-DD。
	StartDay string `json:"startDay"`
	DueDay   string `json:"dueDay"`
	// DailyTarget 是用户自定的每日目标条数；0 表示按时间预算自动推算。
	// 保留它是为了兼容旧数据，新逻辑以 DailyMinutes 为准。
	DailyTarget int `json:"dailyTarget"`
	// DailyMinutes 是每天能投入的分钟数 —— 计划推算的核心输入。
	DailyMinutes int `json:"dailyMinutes"`
	// RestDays 是停滯日（一次性），WeeklyRest 是每周固定休息（0=周日…6=周六）。
	RestDays   []string `json:"restDays"`
	WeeklyRest []int    `json:"weeklyRest"`
	// MinutesPerItem 是各库「学一条」的分钟数，算法与八股差异很大，必须分开。
	MinutesPerItem map[string]int `json:"minutesPerItem"`
	CreatedAt      string         `json:"createdAt"`
	UpdatedAt      string         `json:"updatedAt"`
}

// Snapshot 是一次全量同步的载荷。
type Snapshot struct {
	Progress []Progress `json:"progress"`
	Notes    []Note     `json:"notes"`
	Goal     *Goal      `json:"goal"`
	// Review 只带在籍（未毕业）的复习状态，前端用它标注「下次复习 X 天后」。
	Review   []ReviewItem `json:"review"`
	Revision string       `json:"revision"`
}

type Store struct {
	db *sql.DB

	// docLib 是 docID → 内容库的映射，由 server 启动时注入，
	// 用于给不同库分配不同的复习额度（算法一条要十几分钟，八股几分钟）。
	libMu  sync.RWMutex
	docLib map[string]string
}

// Open 打开（必要时创建）SQLite 数据库。
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据目录: %w", err)
		}
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite 单写者，串行化最省心
	s := &Store{db: db, docLib: map[string]string{}}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS progress (
  doc_id     TEXT    NOT NULL,
  item_id    TEXT    NOT NULL,
  status     INTEGER NOT NULL DEFAULT 0,
  starred    INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT    NOT NULL,
  done_day   TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (doc_id, item_id)
);

CREATE TABLE IF NOT EXISTS notes (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  doc_id     TEXT    NOT NULL,
  item_id    TEXT    NOT NULL,
  content    TEXT    NOT NULL,
  created_at TEXT    NOT NULL,
  updated_at TEXT    NOT NULL,
  created_day TEXT   NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_notes_item ON notes(doc_id, item_id);

CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- 复习计划：一条内容一行，标记「已掌握」时自动入列。
CREATE TABLE IF NOT EXISTS review (
  doc_id       TEXT    NOT NULL,
  item_id      TEXT    NOT NULL,
  stage        INTEGER NOT NULL DEFAULT 0,
  lapses       INTEGER NOT NULL DEFAULT 0,
  review_count INTEGER NOT NULL DEFAULT 0,
  last_day     TEXT    NOT NULL DEFAULT '',
  due_day      TEXT    NOT NULL DEFAULT '',
  last_grade   INTEGER NOT NULL DEFAULT -1,
  graduated    INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT    NOT NULL,
  updated_at   TEXT    NOT NULL,
  PRIMARY KEY (doc_id, item_id)
);
CREATE INDEX IF NOT EXISTS idx_review_due ON review(graduated, due_day);

-- 复习流水：只记真正做过评分的，顺延不算。
CREATE TABLE IF NOT EXISTS review_log (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  doc_id       TEXT    NOT NULL,
  item_id      TEXT    NOT NULL,
  grade        INTEGER NOT NULL,
  day          TEXT    NOT NULL,
  stage_before INTEGER NOT NULL,
  stage_after  INTEGER NOT NULL,
  created_at   TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_review_log_day ON review_log(day);
`)
	return err
}

// Meta 读取一个键值，不存在时返回空串。
func (s *Store) Meta(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetMeta 写入一个键值。
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Snapshot 读出全部进度与笔记。
func (s *Store) Snapshot() (Snapshot, error) {
	snap := Snapshot{Progress: []Progress{}, Notes: []Note{}, Review: []ReviewItem{}}

	goal, err := s.GetGoal()
	if err != nil {
		return snap, err
	}
	snap.Goal = goal

	review, err := s.ActiveReviews()
	if err != nil {
		return snap, err
	}
	today := time.Now().Format(dayLayout)
	for i := range review {
		review[i].finalize(today)
	}
	snap.Review = review

	rows, err := s.db.Query(`SELECT doc_id, item_id, status, starred, updated_at, done_day
		FROM progress ORDER BY doc_id, item_id`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Progress
		var starred int
		if err := rows.Scan(&p.DocID, &p.ItemID, &p.Status, &starred, &p.UpdatedAt, &p.DoneDay); err != nil {
			return snap, err
		}
		p.Starred = starred == 1
		snap.Progress = append(snap.Progress, p)
	}
	if err := rows.Err(); err != nil {
		return snap, err
	}

	nrows, err := s.db.Query(`SELECT id, doc_id, item_id, content, created_at, updated_at, created_day
		FROM notes ORDER BY doc_id, item_id, id`)
	if err != nil {
		return snap, err
	}
	defer nrows.Close()
	for nrows.Next() {
		var n Note
		if err := nrows.Scan(&n.ID, &n.DocID, &n.ItemID, &n.Content, &n.CreatedAt, &n.UpdatedAt, &n.CreatedDy); err != nil {
			return snap, err
		}
		snap.Notes = append(snap.Notes, n)
	}
	return snap, nrows.Err()
}

// SetProgress 写入一道题的状态；status 为 0 且未加星时删除该行（保持表干净）。
func (s *Store) SetProgress(docID, itemID string, status int, starred *bool, day string) (Progress, error) {
	var cur Progress
	var curStarred int
	err := s.db.QueryRow(`SELECT status, starred, updated_at, done_day FROM progress
		WHERE doc_id = ? AND item_id = ?`, docID, itemID).
		Scan(&cur.Status, &curStarred, &cur.UpdatedAt, &cur.DoneDay)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return cur, err
	}
	cur.DocID, cur.ItemID = docID, itemID
	cur.Starred = curStarred == 1
	if starred != nil {
		cur.Starred = *starred
	}
	if status >= 0 {
		cur.Status = status
	}
	now := time.Now().UTC()
	cur.UpdatedAt = now.Format(time.RFC3339)

	switch {
	case cur.Status == StatusDone:
		if cur.DoneDay == "" {
			cur.DoneDay = day
		}
	case cur.Status != StatusDone:
		cur.DoneDay = ""
	}

	if cur.Status == StatusUnread && !cur.Starred && cur.DoneDay == "" {
		_, err = s.db.Exec(`DELETE FROM progress WHERE doc_id = ? AND item_id = ?`, docID, itemID)
		if err == nil {
			// 不再是「已掌握」就不该再占用复习额度。
			err = s.DropReview(docID, itemID)
		}
		return cur, err
	}

	_, err = s.db.Exec(`INSERT INTO progress(doc_id, item_id, status, starred, updated_at, done_day)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(doc_id, item_id) DO UPDATE SET
			status = excluded.status,
			starred = excluded.starred,
			updated_at = excluded.updated_at,
			done_day = excluded.done_day`,
		docID, itemID, cur.Status, boolToInt(cur.Starred), cur.UpdatedAt, cur.DoneDay)
	if err != nil {
		return cur, err
	}

	// 标记「已掌握」即自动进入复习计划；撤销则移出。
	// 已经复习过的内容重复标记是幂等的，不会把进度打回原点。
	if cur.Status == StatusDone {
		err = s.EnrollReview(docID, itemID, cur.DoneDay)
	} else {
		err = s.DropReview(docID, itemID)
	}
	return cur, err
}

// CreateNote 新建笔记。
func (s *Store) CreateNote(docID, itemID, content, day string) (Note, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`INSERT INTO notes(doc_id, item_id, content, created_at, updated_at, created_day)
		VALUES(?, ?, ?, ?, ?, ?)`, docID, itemID, content, now, now, day)
	if err != nil {
		return Note{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Note{}, err
	}
	return Note{ID: id, DocID: docID, ItemID: itemID, Content: content,
		CreatedAt: now, UpdatedAt: now, CreatedDy: day}, nil
}

// UpdateNote 修改笔记内容。
func (s *Store) UpdateNote(id int64, content string) (Note, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`UPDATE notes SET content = ?, updated_at = ? WHERE id = ?`, content, now, id)
	if err != nil {
		return Note{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Note{}, ErrNotFound
	}
	var note Note
	err = s.db.QueryRow(`SELECT id, doc_id, item_id, content, created_at, updated_at, created_day
		FROM notes WHERE id = ?`, id).
		Scan(&note.ID, &note.DocID, &note.ItemID, &note.Content, &note.CreatedAt, &note.UpdatedAt, &note.CreatedDy)
	return note, err
}

// DeleteNote 删除笔记。
func (s *Store) DeleteNote(id int64) error {
	res, err := s.db.Exec(`DELETE FROM notes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DoneCountByDoc 统计每篇文档已掌握的条数。
//
// 计划推算要知道「真实还剩多少」，这个数只能从 progress 表来。
func (s *Store) DoneCountByDoc() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT doc_id, COUNT(*) FROM progress
		WHERE status = ? GROUP BY doc_id`, StatusDone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var doc string
		var n int
		if err := rows.Scan(&doc, &n); err != nil {
			return nil, err
		}
		out[doc] = n
	}
	return out, rows.Err()
}

// ErrNotFound 表示记录不存在。
var ErrNotFound = errors.New("记录不存在")

// goalKey 是 goal 在 meta 表里的键。学习任务是一份 JSON，直接存 meta 即可。
const goalKey = "goal"

// GetGoal 读取当前学习任务；没有设置过时返回 nil。
func (s *Store) GetGoal() (*Goal, error) {
	raw, err := s.Meta(goalKey)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var g Goal
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		// 数据损坏时当作没有目标，不让整个快照接口失败。
		return nil, nil
	}
	return &g, nil
}

// SetGoal 保存学习任务。
func (s *Store) SetGoal(g Goal) (*Goal, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	prev, _ := s.GetGoal()
	if prev != nil {
		g.CreatedAt = prev.CreatedAt
	}
	if g.CreatedAt == "" {
		g.CreatedAt = now
	}
	g.UpdatedAt = now

	// 去重并保持顺序，避免前端重复提交同一模块。
	seen := make(map[string]bool, len(g.DocSlugs))
	dedup := make([]string, 0, len(g.DocSlugs))
	for _, slug := range g.DocSlugs {
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		dedup = append(dedup, slug)
	}
	g.DocSlugs = dedup

	// 停滞日去重排序，保持稳定输出
	if len(g.RestDays) > 0 {
		seenDay := make(map[string]bool, len(g.RestDays))
		days := make([]string, 0, len(g.RestDays))
		for _, d := range g.RestDays {
			if d == "" || seenDay[d] {
				continue
			}
			seenDay[d] = true
			days = append(days, d)
		}
		sort.Strings(days)
		g.RestDays = days
	}
	if len(g.WeeklyRest) > 0 {
		seenW := make(map[int]bool, len(g.WeeklyRest))
		ws := make([]int, 0, len(g.WeeklyRest))
		for _, w := range g.WeeklyRest {
			if w < 0 || w > 6 || seenW[w] {
				continue
			}
			seenW[w] = true
			ws = append(ws, w)
		}
		sort.Ints(ws)
		g.WeeklyRest = ws
	}

	raw, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	if err := s.SetMeta(goalKey, string(raw)); err != nil {
		return nil, err
	}
	return &g, nil
}

// ClearGoal 删除学习任务。
func (s *Store) ClearGoal() error {
	_, err := s.db.Exec(`DELETE FROM meta WHERE key = ?`, goalKey)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
