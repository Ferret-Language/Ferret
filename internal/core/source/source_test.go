package source

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeCache struct {
	lines []string
	ok    bool
}

func (f fakeCache) GetLinesRange(_ string, startLine, endLine int) ([]string, bool) {
	if !f.ok || startLine < 1 || endLine < startLine {
		return nil, false
	}
	return f.lines, true
}

func TestPositionAdvance(t *testing.T) {
	p := NewPosition()
	p.Advance("a\tb\nc")
	if p.Line != 2 || p.Column != 2 {
		t.Fatalf("unexpected position: line=%d col=%d", p.Line, p.Column)
	}
	if p.Index <= 0 {
		t.Fatalf("expected positive index")
	}
}

func TestLocationGetTextAndRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fer")
	content := "hello world\nsecond line\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loc := NewLocation(path, Position{Line: 1, Column: 7}, Position{Line: 1, Column: 12})
	if got := (&loc).GetText(nil); got != "world" {
		t.Fatalf("GetText = %q, want world", got)
	}

	lines, err := GetSourceLinesRange(path, 2, 2, nil)
	if err != nil {
		t.Fatalf("GetSourceLinesRange error: %v", err)
	}
	if len(lines) != 1 || lines[0] != "second line" {
		t.Fatalf("unexpected lines: %#v", lines)
	}
}

func TestGetSourceLinesRangeUsesCache(t *testing.T) {
	lines, err := GetSourceLinesRange("ignored", 1, 1, fakeCache{ok: true, lines: []string{"cached"}})
	if err != nil {
		t.Fatalf("unexpected cache error: %v", err)
	}
	if len(lines) != 1 || lines[0] != "cached" {
		t.Fatalf("unexpected cached lines: %#v", lines)
	}
}
