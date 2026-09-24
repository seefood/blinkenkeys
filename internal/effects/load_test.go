package effects

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTree creates files (relative path -> content) under a temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func loadExamples(t *testing.T) *Library {
	t.Helper()
	lib, err := Load(filepath.Join("..", "..", "examples", "config"))
	if err != nil {
		t.Fatalf("Load(examples/config): %v", err)
	}
	return lib
}

func TestLoadExamples(t *testing.T) {
	lib := loadExamples(t)
	working, err := lib.State("claude/working")
	if err != nil || working.Timeline == nil {
		t.Fatalf("claude/working = %+v, %v; want an effect", working, err)
	}
	if _, finite := working.Timeline.Total(); finite {
		t.Error("claude/working (breathe_blue) should be open-ended")
	}
	idle, err := lib.State("claude/idle")
	if err != nil || idle.Timeline == nil {
		t.Fatalf("claude/idle = %+v, %v", idle, err)
	}
	if total, _ := idle.Timeline.Total(); total != 5*time.Minute {
		t.Errorf("claude/idle total = %v, want 5m", total)
	}
	waiting, err := lib.State("claude/waiting")
	if err != nil || waiting.Color == nil {
		t.Fatalf("claude/waiting = %+v, %v; want a color", waiting, err)
	}
	if _, err := lib.Effect("timer5min"); err != nil {
		t.Errorf("Effect(timer5min): %v", err)
	}
}

func TestLoadMissingDirsIsEmpty(t *testing.T) {
	lib, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load(empty dir): %v", err)
	}
	if _, err := lib.Effect("x"); !errors.Is(err, ErrUnknownEffect) {
		t.Errorf("Effect on empty library: err = %v", err)
	}
	if _, err := lib.State("a/b"); !errors.Is(err, ErrUnknownState) {
		t.Errorf("State on empty library: err = %v", err)
	}
}

func TestLoadIgnoresNonYAMLFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{"effects/README.md": "not yaml", "effects/notes.yaml~": "junk"})
	if _, err := Load(dir); err != nil {
		t.Errorf("Load: %v", err)
	}
}

func TestStateUnknownListsKnown(t *testing.T) {
	lib := loadExamples(t)
	for _, ref := range []string{"claude/sleeping", "nope/idle", "claude"} {
		_, err := lib.State(ref)
		if !errors.Is(err, ErrUnknownState) || !strings.Contains(err.Error(), "claude/idle") {
			t.Errorf("State(%q) err = %v, want ErrUnknownState listing claude/idle", ref, err)
		}
	}
}

func TestLoadFatalCases(t *testing.T) {
	const ok = "stages:\n  - { duration: 1s, color: red }\nfinal_state: red\n"
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"bad effect file name", map[string]string{"effects/Bad Name.yaml": ok}},
		{"bad template file name", map[string]string{"templates/UPPER.yaml": "idle: { color: red }\n"}},
		{"bad state name", map[string]string{"templates/claude.yaml": "Idle State: { color: red }\n"}},
		{"invalid effect", map[string]string{"effects/e.yaml": "stages: []\n"}},
		{"unknown state field", map[string]string{"templates/claude.yaml": "idle: { color: red, colour: blue }\n"}},
		{"state params (not in phase 3)", map[string]string{"effects/e.yaml": ok, "templates/claude.yaml": "idle: { effect: e, params: { x: 1 } }\n"}},
		{"state with color and effect", map[string]string{"effects/e.yaml": ok, "templates/claude.yaml": "idle: { color: red, effect: e }\n"}},
		{"state with neither", map[string]string{"templates/claude.yaml": "idle: {}\n"}},
		{"state bad color", map[string]string{"templates/claude.yaml": "idle: { color: nope }\n"}},
		{"state unknown effect", map[string]string{"templates/claude.yaml": "idle: { effect: ghost }\n"}},
		{"malformed template yaml", map[string]string{"templates/claude.yaml": "idle: [\n"}},
	}
	for _, tt := range tests {
		if _, err := Load(writeTree(t, tt.files)); err == nil {
			t.Errorf("%s: Load succeeded, want error", tt.name)
		}
	}
}
