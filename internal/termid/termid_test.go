package termid

import (
	"context"
	"errors"
	"testing"
	"time"
)

func envOf(vars map[string]string, run func(ctx context.Context, name string, args ...string) (string, error)) Env {
	if run == nil {
		run = func(context.Context, string, ...string) (string, error) { return "", errors.New("no helper") }
	}
	return Env{Getenv: func(k string) string { return vars[k] }, Run: run}
}

func TestDetectITerm(t *testing.T) {
	id := Detect(context.Background(), envOf(map[string]string{"ITERM_SESSION_ID": "w0t2p1:C3D91F33-3805-47E2-A3F6-B8AED6EC2209"}, nil))
	if id.Terminal != "iterm" || id.Tab != 3 || id.InstanceID != "C3D91F33" || id.Name() != "iterm-C3D91F33" {
		t.Errorf("got %+v (%q)", id, id.Name())
	}
	bare := Detect(context.Background(), envOf(map[string]string{"ITERM_SESSION_ID": "w1t0p0"}, nil))
	if bare.Tab != 1 || bare.InstanceID != "w1t0p0" {
		t.Errorf("no-UUID form: %+v", bare)
	}
	odd := Detect(context.Background(), envOf(map[string]string{"ITERM_SESSION_ID": "garbage/value"}, nil))
	if odd.Terminal != "iterm" || odd.Tab != 0 || odd.InstanceID != "garbage-value" {
		t.Errorf("unparseable: %+v", odd)
	}
}

func TestDetectTmuxTabIsWindowIndexMinusBase(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) (string, error) {
		if name != "tmux" {
			return "", errors.New("unexpected " + name)
		}
		if args[0] == "display-message" {
			return "3\n", nil
		}
		return "1\n", nil // show-options -gv base-index
	}
	id := Detect(context.Background(), envOf(map[string]string{"TMUX_PANE": "%7", "ITERM_SESSION_ID": "w0t0p0:X"}, run))
	if id.Terminal != "tmux" || id.Tab != 3 || id.InstanceID != "7" {
		t.Errorf("got %+v; tmux must win over the outer terminal and 3-1+1 = 3", id)
	}
}

func TestDetectTmuxHelperFailureKeepsInstance(t *testing.T) {
	id := Detect(context.Background(), envOf(map[string]string{"TMUX_PANE": "%7"}, nil))
	if id.Terminal != "tmux" || id.Tab != 0 || id.InstanceID != "7" {
		t.Errorf("got %+v", id)
	}
}

const weztermList = `[
 {"window_id":0,"tab_id":0,"pane_id":0},
 {"window_id":0,"tab_id":4,"pane_id":9},
 {"window_id":0,"tab_id":4,"pane_id":10},
 {"window_id":1,"tab_id":2,"pane_id":5},
 {"window_id":0,"tab_id":7,"pane_id":12}
]`

func TestDetectWezTermTabIsRankWithinWindow(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) (string, error) { return weztermList, nil }
	for pane, want := range map[string]int{"0": 1, "9": 2, "10": 2, "12": 3, "5": 1} {
		id := Detect(context.Background(), envOf(map[string]string{"WEZTERM_PANE": pane}, run))
		if id.Terminal != "wezterm" || id.Tab != want || id.InstanceID != pane {
			t.Errorf("pane %s: got %+v, want tab %d", pane, id, want)
		}
	}
}

func TestDetectWezTermBadOutputDegrades(t *testing.T) {
	for _, out := range []string{"not json", "[]", `[{"window_id":0,"tab_id":0,"pane_id":3}]`} {
		run := func(context.Context, string, ...string) (string, error) { return out, nil }
		id := Detect(context.Background(), envOf(map[string]string{"WEZTERM_PANE": "9"}, run))
		if id.Terminal != "wezterm" || id.Tab != 0 || id.InstanceID != "9" {
			t.Errorf("output %q: got %+v", out, id)
		}
	}
}

func TestDetectHangingHelperIsBounded(t *testing.T) {
	run := func(ctx context.Context, _ string, _ ...string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	start := time.Now()
	id := Detect(context.Background(), envOf(map[string]string{"WEZTERM_PANE": "9"}, run))
	if time.Since(start) > 2*time.Second || id.Tab != 0 || id.InstanceID != "9" {
		t.Errorf("hang not bounded: %v, %+v", time.Since(start), id)
	}
}

func TestDetectKittyAndNone(t *testing.T) {
	id := Detect(context.Background(), envOf(map[string]string{"KITTY_WINDOW_ID": "4"}, nil))
	if id.Terminal != "kitty" || id.Tab != 0 || id.Name() != "kitty-4" {
		t.Errorf("kitty: %+v", id)
	}
	if id := Detect(context.Background(), envOf(nil, nil)); id != (Identity{}) || id.Name() != "" {
		t.Errorf("none: %+v", id)
	}
}

func TestExecRunMissingHelperDegrades(t *testing.T) {
	env := Env{
		Getenv: func(k string) string {
			if k == "WEZTERM_PANE" {
				return "9"
			}
			return ""
		},
		Run: func(ctx context.Context, _ string, args ...string) (string, error) {
			return ExecRun(ctx, "blinkenkeys-no-such-helper", args...)
		},
	}
	if id := Detect(context.Background(), env); id.Terminal != "wezterm" || id.Tab != 0 || id.InstanceID != "9" {
		t.Errorf("got %+v", id)
	}
}

func TestExecRunHangingHelperIsBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := ExecRun(ctx, "sleep", "30"); err == nil {
		t.Error("expected error from killed helper")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("ExecRun not bounded by ctx: %v", d)
	}
}

// A helper whose grandchild keeps stdout open must not outlive the context:
// without cmd.WaitDelay, Output() blocks until the pipe closes.
func TestExecRunGrandchildHoldingStdoutIsBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	type result struct{ err error }
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		_, err := ExecRun(ctx, "sh", "-c", "sleep 30 & wait")
		done <- result{err}
	}()
	select {
	case r := <-done:
		if r.err == nil {
			t.Error("expected error from killed helper")
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("ExecRun not bounded by ctx: %v", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ExecRun hung: grandchild holding stdout kept Output() blocked past the deadline")
	}
}

func TestSanitizeDotSegments(t *testing.T) {
	for in, want := range map[string]string{".": "", "..": "", "...": "...", "a.b": "a.b", "": ""} {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFallbackNameOrderAndSanitize(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	name, src := FallbackName(get(map[string]string{"CLAUDE_CODE_SESSION_ID": "abc-123", "STY": "x"}))
	if name != "claude-abc-123" || src != "CLAUDE_CODE_SESSION_ID" {
		t.Errorf("got %q from %q", name, src)
	}
	name, _ = FallbackName(get(map[string]string{"BLINKENKEYS_NAME": "my/key,1", "CLAUDE_CODE_SESSION_ID": "z"}))
	if name != "my-key-1" {
		t.Errorf("BLINKENKEYS_NAME first and sanitized: got %q", name)
	}
	if name, src := FallbackName(get(nil)); name != "" || src != "" {
		t.Errorf("empty env: %q %q", name, src)
	}
}
