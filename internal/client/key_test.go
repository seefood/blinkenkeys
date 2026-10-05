package client

import (
	"errors"
	"reflect"
	"strings"
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
		{"explicit name", KeyInput{Name: "build", ID: iterm}, Plan{Mode: ModeNamed, Base: "build", Name: "build", Owner: "build", Given: true}},
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
	for _, n := range []string{"a,b", "a/b", "led:3", "idx:2", "led:x", ".", ".."} {
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
	if n, _ := PlanKey(KeyInput{Name: "foo"}); n.Qualify("laptop").Name != "foo" || n.Qualify("laptop").Owner != "foo" {
		t.Errorf("explicit -n must stay verbatim: %+v", n.Qualify("laptop"))
	}
	if fb, _ := PlanKey(KeyInput{Fallback: "claude-x"}); fb.Qualify("laptop").Name != "laptop.claude-x" {
		t.Errorf("derived fallback must be qualified: %+v", fb.Qualify("laptop"))
	}
	e, _ := PlanKey(KeyInput{Key: "0,0"})
	if e.Qualify("laptop") != e || p.Qualify("") != p {
		t.Error("explicit plans and an empty host must be unchanged")
	}
}

func TestLongDerivedNamesFitDaemonLimit(t *testing.T) {
	long := func(tail string) string { return "claude-" + strings.Repeat("x", 300) + tail }
	host := strings.Repeat("h", 60)
	plans := func(fallback string) []Plan {
		p, err := PlanKey(KeyInput{Fallback: fallback})
		if err != nil {
			t.Fatal(err)
		}
		s, err := PlanKey(KeyInput{ID: termid.Identity{Terminal: "iterm", Tab: 1, InstanceID: fallback}})
		if err != nil {
			t.Fatal(err)
		}
		return []Plan{p, p.Qualify(host), s, s.Qualify(host)}
	}
	a, a2, b := plans(long("a")), plans(long("a")), plans(long("b"))
	for i := range a {
		for _, v := range []string{a[i].Name, a[i].Owner} {
			if len(v) > 128 {
				t.Errorf("plan %d: %d bytes > 128: %q", i, len(v), v)
			}
		}
		if a[i] != a2[i] {
			t.Errorf("plan %d not deterministic: %+v vs %+v", i, a[i], a2[i])
		}
		if a[i].Owner == b[i].Owner {
			t.Errorf("plan %d: distinct inputs collide: %q", i, a[i].Owner)
		}
	}
	if q := a[1].Owner; !strings.HasPrefix(q, host+".claude-xxx") {
		t.Errorf("qualified name should keep its readable prefix: %q", q)
	}
	if short, _ := PlanKey(KeyInput{Fallback: "claude-x"}); short.Qualify("laptop").Name != "laptop.claude-x" {
		t.Error("short names must be unchanged")
	}
	if _, err := PlanKey(KeyInput{Name: strings.Repeat("n", 129)}); !errors.Is(err, ErrUsage) {
		t.Errorf("explicit -n over 128 bytes: err = %v, want ErrUsage", err)
	}
	if _, err := PlanKey(KeyInput{Name: strings.Repeat("n", 128)}); err != nil {
		t.Errorf("explicit -n of exactly 128 bytes: %v", err)
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
