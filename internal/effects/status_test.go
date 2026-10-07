package effects

import (
	"errors"
	"io"
	"log/slog"
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

// A write the engine rejects leaves the previous status record untouched.
func TestFailedWriteKeepsOldRecord(t *testing.T) {
	out := &fakeSetter{}
	e := NewEngine(out, nil)
	t0 := time.Unix(1000, 0)
	old := Origin{Type: "color", Ref: "red", Owner: "a"}
	if err := e.SetColorFrom(tgt("a", 1), color.HSV{}, old, t0); err != nil {
		t.Fatal(err)
	}
	out.err = errors.New("queue full")
	if err := e.SetColorFrom(tgt("a", 1), color.HSV{}, Origin{Type: "color", Ref: "blue", Owner: "b"}, t0.Add(time.Second)); err == nil {
		t.Fatal("SetColorFrom: want error")
	}
	if err := e.StartFrom(tgt("a", 1), statusTimeline(t, "pulse", tenSecondRed), t0.Add(time.Second), Origin{Type: "effect", Ref: "pulse"}); err == nil {
		t.Fatal("StartFrom: want error")
	}
	if st, ok := e.Status(tgt("a", 1), t0.Add(2*time.Second)); !ok || st.Origin != old || !st.SetAt.Equal(t0) {
		t.Errorf("Status = %+v, %v; want the old record", st, ok)
	}
}

// An effect Tick drops after a failed write is reported as failed, at the
// time it stopped, not as a normal finish.
func TestTickWriteFailureMarksRecordFailed(t *testing.T) {
	out := &fakeSetter{}
	e := NewEngine(out, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t0 := time.Unix(1000, 0)
	tl := statusTimeline(t, "pulse", "stages:\n  - { duration: 5s, color: red }\n  - { duration: 5s, color: blue }\nfinal_state: \"#000000\"\n")
	if err := e.StartFrom(tgt("a", 1), tl, t0, Origin{Type: "effect", Ref: "pulse"}); err != nil {
		t.Fatal(err)
	}
	out.err = errors.New("gone")
	e.Tick(t0.Add(6 * time.Second)) // frame changes to blue; write fails
	st, ok := e.Status(tgt("a", 1), t0.Add(20*time.Second))
	if !ok || st.Effect == nil {
		t.Fatalf("Status = %+v, %v", st, ok)
	}
	if !st.Effect.Failed || st.Effect.Running || st.Effect.Elapsed != 6*time.Second {
		t.Errorf("effect status = %+v, want Failed, not running, elapsed 6s", st.Effect)
	}
	// A later successful write replaces the record, clearing the marker.
	out.err = nil
	_ = e.StartFrom(tgt("a", 1), tl, t0.Add(30*time.Second), Origin{Type: "effect", Ref: "pulse"})
	if st, _ := e.Status(tgt("a", 1), t0.Add(31*time.Second)); st.Effect == nil || st.Effect.Failed || !st.Effect.Running {
		t.Errorf("restarted effect status = %+v", st.Effect)
	}
}

// ClearIfOwner blanks (and cancels, and drops the record) only when there is
// no record or its Owner matches, in one lock hold.
func TestClearIfOwner(t *testing.T) {
	out := &fakeSetter{}
	e := NewEngine(out, nil)
	t0 := time.Unix(1000, 0)
	tl := statusTimeline(t, "pulse", tenSecondRed)
	_ = e.StartFrom(tgt("a", 1), tl, t0, Origin{Type: "effect", Ref: "pulse", Owner: "b"})

	if cleared, err := e.ClearIfOwner(tgt("a", 1), "a"); err != nil || cleared {
		t.Fatalf("other owner: cleared %v, %v; want false", cleared, err)
	}
	if st, ok := e.Status(tgt("a", 1), t0); !ok || st.Origin.Owner != "b" || !st.Effect.Running {
		t.Errorf("mismatch must leave the key alone: %+v, %v", st, ok)
	}
	n := len(out.writes)
	if cleared, err := e.ClearIfOwner(tgt("a", 1), "b"); err != nil || !cleared {
		t.Fatalf("matching owner: cleared %v, %v; want true", cleared, err)
	}
	if len(out.writes) != n+1 || out.writes[n].c != (color.HSV{}) {
		t.Errorf("writes = %+v, want one blank", out.writes[n:])
	}
	if _, ok := e.Status(tgt("a", 1), t0); ok {
		t.Error("record must be dropped")
	}
	e.Tick(t0.Add(time.Second))
	if len(out.writes) != n+1 {
		t.Error("effect kept running after ClearIfOwner")
	}
	// No record: proceeds (see the F8 ruling in the spec).
	if cleared, err := e.ClearIfOwner(tgt("a", 2), "a"); err != nil || !cleared {
		t.Errorf("no record: cleared %v, %v; want true", cleared, err)
	}
	// Untagged record with an owner given: mismatch.
	_ = e.SetColorFrom(tgt("a", 3), color.HSV{V: 1}, Origin{Type: "color", Ref: "x"}, t0)
	if cleared, _ := e.ClearIfOwner(tgt("a", 3), "a"); cleared {
		t.Error("untagged record must not be cleared by an owner")
	}
	// A failed blank keeps the record and reports the error.
	_ = e.SetColorFrom(tgt("a", 4), color.HSV{V: 1}, Origin{Type: "color", Ref: "x", Owner: "a"}, t0)
	out.err = errors.New("queue full")
	if cleared, err := e.ClearIfOwner(tgt("a", 4), "a"); err == nil || cleared {
		t.Errorf("failed blank: cleared %v, %v", cleared, err)
	}
	if _, ok := e.Status(tgt("a", 4), t0); !ok {
		t.Error("failed blank dropped the record")
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
