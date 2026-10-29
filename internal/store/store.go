package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// Snapshot 是一次全量同步的载荷。
type Snapshot struct {
	Progress []Progress `json:"progress"`
	Notes    []Note     `json:"notes"`
	Revision string     `json:"revision"`
}

type Store struct {
	db *sql.DB
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
	s := &Store{db: db}
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
	snap := Snapshot{Progress: []Progress{}, Notes: []Note{}}

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

// ErrNotFound 表示记录不存在。
var ErrNotFound = errors.New("记录不存在")

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
