package effects

import (
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

type statusSetter struct{ writes int }

func (s *statusSetter) Write(string, keyaddr.Address, color.HSV) error { s.writes++; return nil }

func statusTimeline(t *testing.T, name, src string) *Timeline {
	t.Helper()
	tls, err := compileEffects(map[string][]byte{name: []byte(src)})
	if err != nil {
		t.Fatalf("compileEffects: %v", err)
	}
	return tls[name]
}

const tenSecondRed = "stages:\n  - color: red\n    duration: 10s\nfinal_state: \"#000000\"\n"

func tgt(dev string, n uint16) Target {
	return Target{Device: dev, Addr: keyaddr.Address{Kind: keyaddr.LED, N: n}}
}

func TestTimelineHasName(t *testing.T) {
	if tl := statusTimeline(t, "pulse", tenSecondRed); tl.Name != "pulse" {
		t.Errorf("Name = %q", tl.Name)
	}
}

func TestSetColorFromRecordsOrigin(t *testing.T) {
	e := NewEngine(&statusSetter{}, nil)
	t0 := time.Unix(1000, 0)
	o := Origin{Type: "color", Ref: "#ff0000", Owner: "laptop.iterm-ab12"}
	if err := e.SetColorFrom(tgt("a", 1), color.HSV{S: 255, V: 255}, o, t0); err != nil {
		t.Fatal(err)
	}
	st, ok := e.Status(tgt("a", 1), t0.Add(3*time.Second))
	if !ok || st.Origin != o || !st.SetAt.Equal(t0) || st.Effect != nil {
		t.Errorf("Status = %+v, %v", st, ok)
	}
	if _, ok := e.Status(tgt("a", 2), t0); ok {
		t.Error("status for never-written target")
	}
}

func TestStartFromTracksEffectProgress(t *testing.T) {
	e := NewEngine(&statusSetter{}, nil)
	tl := statusTimeline(t, "pulse", tenSecondRed)
	t0 := time.Unix(1000, 0)
	o := Origin{Type: "state", Ref: "claude/working"}
	if err := e.StartFrom(tgt("a", 1), tl, t0, o); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Status(tgt("a", 1), t0.Add(4*time.Second))
	if st.Effect == nil || !st.Effect.Running || st.Effect.Name != "pulse" ||
		st.Effect.Elapsed != 4*time.Second || !st.Effect.Finite || st.Effect.Total != 10*time.Second {
		t.Errorf("running status = %+v", st.Effect)
	}
	e.Tick(t0.Add(11 * time.Second)) // finishes, writes final_state, drops running
	st, ok := e.Status(tgt("a", 1), t0.Add(12*time.Second))
	if !ok || st.Effect == nil || st.Effect.Running || st.Effect.Elapsed != 10*time.Second {
		t.Errorf("finished status = %+v, %v (record must outlive the effect)", st.Effect, ok)
	}
}

func TestPlainSetColorAndStartDropRecord(t *testing.T) {
	e := NewEngine(&statusSetter{}, nil)
	tl := statusTimeline(t, "pulse", tenSecondRed)
	t0 := time.Unix(1000, 0)
	_ = e.StartFrom(tgt("a", 1), tl, t0, Origin{Type: "effect", Ref: "pulse"})
	if err := e.SetColor(tgt("a", 1), color.HSV{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Status(tgt("a", 1), t0); ok {
		t.Error("SetColor must drop the record")
	}
	_ = e.SetColorFrom(tgt("a", 1), color.HSV{}, Origin{Type: "color", Ref: "x"}, t0)
	_ = e.Start(tgt("a", 1), tl, t0)
	if _, ok := e.Status(tgt("a", 1), t0); ok {
		t.Error("Start must drop the record")
	}
}

func TestSetColorFromCancelsRunningEffect(t *testing.T) {
	e := NewEngine(&statusSetter{}, nil)
	tl := statusTimeline(t, "pulse", tenSecondRed)
	t0 := time.Unix(1000, 0)
	_ = e.StartFrom(tgt("a", 1), tl, t0, Origin{Type: "effect", Ref: "pulse"})
	_ = e.SetColorFrom(tgt("a", 1), color.HSV{}, Origin{Type: "color", Ref: "black"}, t0.Add(time.Second))
	st, _ := e.Status(tgt("a", 1), t0.Add(2*time.Second))
	if st.Origin.Type != "color" || st.Effect != nil {
		t.Errorf("status after supersede = %+v", st)
	}
}

func TestStatusesFiltersByDevice(t *testing.T) {
	e := NewEngine(&statusSetter{}, nil)
	t0 := time.Unix(1000, 0)
	_ = e.SetColorFrom(tgt("a", 1), color.HSV{}, Origin{Type: "color", Ref: "x"}, t0)
	_ = e.SetColorFrom(tgt("a", 2), color.HSV{}, Origin{Type: "color", Ref: "y"}, t0)
	_ = e.SetColorFrom(tgt("b", 1), color.HSV{}, Origin{Type: "color", Ref: "z"}, t0)
	if got := e.Statuses("a", t0); len(got) != 2 {
		t.Errorf("Statuses(a) = %+v", got)
	}
}
