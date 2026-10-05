package client

import (
	"errors"
	"reflect"
	"testing"

	"github.com/seefood/blinkenkeys/internal/termid"
)

func TestPlanKeyOrder(t *testing.T) {
	iterm := termid.Identity{Terminal: "iterm", Tab: 3, InstanceID: "ab12cd34"}
	wez := termid.Identity{Terminal: "wezterm", InstanceID: "17"}
	tests := []struct {
		name string
		in   KeyInput
		want Plan
	}{
		{"explicit key wins", KeyInput{Key: "0,1", Name: "n", ID: iterm}, Plan{Mode: ModeExplicit, Key: "0,1"}},
		{"explicit name", KeyInput{Name: "build", ID: iterm}, Plan{Mode: ModeNamed, Base: "build", Name: "build", Owner: "build"}},
		{"tab slot", KeyInput{ID: iterm, Fallback: "claude-x"}, Plan{Mode: ModeSlot, Tab: 3, Base: "iterm-ab12cd34", Owner: "iterm-ab12cd34"}},
		{"instance id when no tab", KeyInput{ID: wez, Fallback: "claude-x"}, Plan{Mode: ModeNamed, Base: "wezterm-17", Name: "wezterm-17", Owner: "wezterm-17"}},
		{"env fallback", KeyInput{Fallback: "claude-x"}, Plan{Mode: ModeNamed, Base: "claude-x", Name: "claude-x", Owner: "claude-x"}},
	}
	for _, tt := range tests {
		got, err := PlanKey(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("%s: got %+v, %v; want %+v", tt.name, got, err, tt.want)
		}
	}
}

func TestPlanKeyNothingAvailable(t *testing.T) {
	if _, err := PlanKey(KeyInput{}); !errors.Is(err, ErrNoKey) || !errors.Is(err, ErrUsage) {
		t.Errorf("err = %v, want ErrNoKey (a usage error)", err)
	}
}

func TestPlanKeyRejectsUnsafeExplicitName(t *testing.T) {
	for _, n := range []string{"a,b", "a/b"} {
		if _, err := PlanKey(KeyInput{Name: n}); !errors.Is(err, ErrUsage) {
			t.Errorf("%q: err = %v, want ErrUsage", n, err)
		}
	}
}

func TestQualifyPrefixesHost(t *testing.T) {
	p, _ := PlanKey(KeyInput{ID: termid.Identity{Terminal: "wezterm", InstanceID: "17"}})
	q := p.Qualify("laptop")
	if q.Name != "laptop.wezterm-17" || q.Owner != "laptop.wezterm-17" {
		t.Errorf("named: %+v", q)
	}
	s, _ := PlanKey(KeyInput{ID: termid.Identity{Terminal: "iterm", Tab: 1, InstanceID: "ab"}})
	if sq := s.Qualify("laptop"); sq.Owner != "laptop.iterm-ab" || sq.Name != "" {
		t.Errorf("slot: %+v", sq)
	}
	e, _ := PlanKey(KeyInput{Key: "0,0"})
	if e.Qualify("laptop") != e || p.Qualify("") != p {
		t.Error("explicit plans and an empty host must be unchanged")
	}
}

func TestSlotAndSharedKeys(t *testing.T) {
	tabs := []uint16{0, 1, 2, 3, 4, 5}
	for tab, want := range map[int]string{1: "idx:0", 6: "idx:5", 7: "idx:0", 13: "idx:0", 8: "idx:1"} {
		if got := SlotKey(tab, tabs); got != want {
			t.Errorf("SlotKey(%d) = %s, want %s", tab, got, want)
		}
	}
	if got := SlotKey(2, []uint16{4, 6, 9}); got != "idx:6" {
		t.Errorf("non-contiguous tabs: %s", got)
	}
	a, b := SharedKey("claude-abc", tabs), SharedKey("claude-abc", tabs)
	if a != b || a[:4] != "idx:" {
		t.Errorf("SharedKey not stable: %s %s", a, b)
	}
	seen := map[string]bool{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		seen[SharedKey(n, tabs)] = true
	}
	if len(seen) < 2 {
		t.Errorf("SharedKey does not spread names: %v", seen)
	}
	if !reflect.DeepEqual(DefaultTabs(3), []uint16{0, 1, 2}) {
		t.Errorf("DefaultTabs = %v", DefaultTabs(3))
	}
}
