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

type write struct {
	t Target
	c color.HSV
}

type fakeSetter struct {
	writes []write
	err    error
}

func (f *fakeSetter) Write(device string, addr keyaddr.Address, c color.HSV) error {
	if f.err != nil {
		return f.err
	}
	f.writes = append(f.writes, write{Target{device, addr}, c})
	return nil
}

var key = Target{Device: "a", Addr: keyaddr.Address{Kind: keyaddr.LED, N: 0}}

func newTestEngine() (*Engine, *fakeSetter) {
	out := &fakeSetter{}
	return NewEngine(out, slog.New(slog.NewTextHandler(io.Discard, nil))), out
}

func engineLib(t *testing.T) *Library {
	return libFrom(t, map[string]string{
		"two": `
stages:
  - { duration: 1s, color: red }
  - { duration: 1s, color: "#00ff00" }
final_state: blue
`,
		"solid": "stages:\n  - { color: white }\n",
	})
}

func TestStartWritesFrameZeroThenTicksChanges(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	if err := e.Start(key, mustEffect(t, engineLib(t), "two"), t0); err != nil {
		t.Fatal(err)
	}
	e.Tick(t0.Add(200 * time.Millisecond)) // still red: no write
	e.Tick(t0.Add(1200 * time.Millisecond))
	if len(out.writes) != 2 || out.writes[0].c != red || out.writes[1].c != pureGreen {
		t.Errorf("writes = %+v, want [red, green]", out.writes)
	}
}

func TestNaturalEndWritesFinalAndStops(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	_ = e.Start(key, mustEffect(t, engineLib(t), "two"), t0)
	e.Tick(t0.Add(2 * time.Second))
	n := len(out.writes)
	if out.writes[n-1].c != (color.HSV{H: 170, S: 255, V: 255}) {
		t.Errorf("last write = %+v, want final blue", out.writes[n-1])
	}
	e.Tick(t0.Add(3 * time.Second))
	if len(out.writes) != n {
		t.Error("finished effect kept writing")
	}
}

func TestSetColorCancelsEffect(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	_ = e.Start(key, mustEffect(t, engineLib(t), "two"), t0)
	if err := e.SetColor(key, black); err != nil {
		t.Fatal(err)
	}
	n := len(out.writes)
	e.Tick(t0.Add(1500 * time.Millisecond))
	e.Tick(t0.Add(5 * time.Second)) // past natural end: no final_state either
	if len(out.writes) != n {
		t.Errorf("writes after SetColor: %+v", out.writes[n:])
	}
}

func TestStartSupersedesWithoutFinalState(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	lib := engineLib(t)
	_ = e.Start(key, mustEffect(t, lib, "two"), t0)
	_ = e.Start(key, mustEffect(t, lib, "solid"), t0.Add(500*time.Millisecond))
	e.Tick(t0.Add(5 * time.Second))
	for _, w := range out.writes {
		if w.c == (color.HSV{H: 170, S: 255, V: 255}) {
			t.Errorf("superseded effect wrote its final_state: %+v", out.writes)
		}
	}
	if last := out.writes[len(out.writes)-1].c; last != (color.HSV{S: 0, V: 255}) {
		t.Errorf("last write = %+v, want solid white", last)
	}
}

func TestTargetsAreIndependent(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	other := Target{Device: "a", Addr: keyaddr.Address{Kind: keyaddr.LED, N: 1}}
	lib := engineLib(t)
	_ = e.Start(key, mustEffect(t, lib, "two"), t0)
	_ = e.SetColor(other, black) // must not cancel key's effect
	e.Tick(t0.Add(1200 * time.Millisecond))
	if last := out.writes[len(out.writes)-1]; last.t != key || last.c != pureGreen {
		t.Errorf("last write = %+v, want key -> green", last)
	}
}

func TestStartWriteErrorNotRegistered(t *testing.T) {
	e, out := newTestEngine()
	out.err = errors.New("boom")
	if err := e.Start(key, mustEffect(t, engineLib(t), "two"), time.Unix(1000, 0)); err == nil {
		t.Fatal("Start: want error")
	}
	out.err = nil
	e.Tick(time.Unix(1001, 500))
	if len(out.writes) != 0 {
		t.Errorf("unregistered effect wrote: %+v", out.writes)
	}
}

func TestTickWriteErrorDropsEffect(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	_ = e.Start(key, mustEffect(t, engineLib(t), "two"), t0)
	out.err = errors.New("gone")
	e.Tick(t0.Add(1200 * time.Millisecond))
	out.err = nil
	e.Tick(t0.Add(5 * time.Second))
	if len(out.writes) != 1 {
		t.Errorf("writes = %+v, want only Start's frame", out.writes)
	}
}
