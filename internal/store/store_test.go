package store

import (
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestProgressLifecycle(t *testing.T) {
	s := newStore(t)

	// 标记已掌握
	p, err := s.SetProgress("java", "item-1", StatusDone, nil, "2026-01-02")
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != StatusDone || p.DoneDay != "2026-01-02" {
		t.Fatalf("状态或完成日期不对: %+v", p)
	}

	// 加星不改变状态
	star := true
	p, err = s.SetProgress("java", "item-1", -1, &star, "2026-01-02")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Starred || p.Status != StatusDone {
		t.Fatalf("加星后状态被破坏: %+v", p)
	}

	// 撤销已掌握，done_day 应清空但保留加星
	p, err = s.SetProgress("java", "item-1", StatusUnread, nil, "2026-01-02")
	if err != nil {
		t.Fatal(err)
	}
	if p.DoneDay != "" {
		t.Fatalf("撤销后 doneDay 应为空: %+v", p)
	}

	// 取消加星且状态为未开始 → 整行删除
	unstar := false
	if _, err := s.SetProgress("java", "item-1", -1, &unstar, ""); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Progress) != 0 {
		t.Fatalf("空行未被清理: %+v", snap.Progress)
	}
}

func TestProgressUpsert(t *testing.T) {
	s := newStore(t)
	if _, err := s.SetProgress("redis", "a", StatusInProgress, nil, "2026-01-02"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProgress("redis", "a", StatusDone, nil, "2026-01-03"); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Snapshot()
	if len(snap.Progress) != 1 {
		t.Fatalf("应为 1 条记录，实际 %d", len(snap.Progress))
	}
	if snap.Progress[0].Status != StatusDone || snap.Progress[0].DoneDay != "2026-01-03" {
		t.Fatalf("upsert 结果不对: %+v", snap.Progress[0])
	}
}

func TestNotesCRUD(t *testing.T) {
	s := newStore(t)

	n, err := s.CreateNote("mysql", "idx", "第一条笔记", "2026-01-02")
	if err != nil {
		t.Fatal(err)
	}
	if n.ID == 0 || n.CreatedDy != "2026-01-02" {
		t.Fatalf("新建笔记返回不对: %+v", n)
	}

	updated, err := s.UpdateNote(n.ID, "改过的内容")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Content != "改过的内容" {
		t.Fatalf("更新失败: %+v", updated)
	}

	if _, err := s.UpdateNote(9999, "x"); err != ErrNotFound {
		t.Fatalf("不存在的笔记应返回 ErrNotFound，实际 %v", err)
	}

	if err := s.DeleteNote(n.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNote(n.ID); err != ErrNotFound {
		t.Fatalf("重复删除应返回 ErrNotFound，实际 %v", err)
	}

	snap, _ := s.Snapshot()
	if len(snap.Notes) != 0 {
		t.Fatalf("笔记未删除: %+v", snap.Notes)
	}
}

func TestMultipleNotesPerItem(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 3; i++ {
		if _, err := s.CreateNote("jvm", "gc", "笔记", "2026-01-02"); err != nil {
			t.Fatal(err)
		}
	}
	snap, _ := s.Snapshot()
	if len(snap.Notes) != 3 {
		t.Fatalf("同一题应允许多条笔记，实际 %d", len(snap.Notes))
	}
}

func TestMeta(t *testing.T) {
	s := newStore(t)
	v, err := s.Meta("revision")
	if err != nil || v != "" {
		t.Fatalf("不存在的键应返回空串: %q %v", v, err)
	}
	if err := s.SetMeta("revision", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta("revision", "def"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Meta("revision"); v != "def" {
		t.Fatalf("meta 未更新: %q", v)
	}
}

func TestPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "learn.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.SetProgress("os", "x", StatusDone, nil, "2026-01-02"); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.CreateNote("os", "x", "持久化检查", "2026-01-02"); err != nil {
		t.Fatal(err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	snap, err := s2.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Progress) != 1 || len(snap.Notes) != 1 {
		t.Fatalf("重新打开后数据丢失: %+v", snap)
	}
}
