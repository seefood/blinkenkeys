package effects

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
)

func sources(effects map[string]string) map[string][]byte {
	src := make(map[string][]byte, len(effects))
	for name, s := range effects {
		src[name] = []byte(s)
	}
	return src
}

// libFrom compiles effect name -> YAML source into a Library, exactly as
// Load does for effects/*.yaml.
func libFrom(t *testing.T, effects map[string]string) *Library {
	t.Helper()
	compiled, err := compileEffects(sources(effects))
	if err != nil {
		t.Fatalf("compileEffects: %v", err)
	}
	return &Library{effects: compiled}
}

func mustEffect(t *testing.T, l *Library, name string) *Timeline {
	t.Helper()
	tl, err := l.Effect(name)
	if err != nil {
		t.Fatalf("Effect(%s): %v", name, err)
	}
	return tl
}

const timer5min = `
stages:
  - duration: 3m
    color: "#00ff00"
  - duration: 1m
    primitive: alternate
    settings: { color_a: "#00ff00", color_b: "#ff0000", frequency_hz: 1, duty_cycle: 0.8 }
  - duration: 1m
    primitive: alternate
    settings: { color_a: "#00ff00", color_b: "#ff0000", frequency_hz: 1, duty_cycle: 0.2 }
final_state: "#ff0000"
`

var (
	pureGreen = color.HSV{H: 85, S: 255, V: 255}
	blue      = color.HSV{H: 170, S: 255, V: 255}
	white     = color.HSV{H: 0, S: 0, V: 255}
)

func TestTimer5minStageBoundaries(t *testing.T) {
	tl := mustEffect(t, libFrom(t, map[string]string{"timer5min": timer5min}), "timer5min")
	tests := []struct {
		at       time.Duration
		want     color.HSV
		wantDone bool
	}{
		{0, pureGreen, false},
		{179900 * time.Millisecond, pureGreen, false},
		{180 * time.Second, pureGreen, false},                // stage 2, phase 0 < 0.8
		{180*time.Second + 900*time.Millisecond, red, false}, // stage 2, phase 0.9
		{240 * time.Second, pureGreen, false},                // stage 3, phase 0 < 0.2
		{240*time.Second + 500*time.Millisecond, red, false}, // stage 3, phase 0.5
		{300 * time.Second, red, true},                       // final_state
		{time.Hour, red, true},
	}
	for _, tt := range tests {
		got, done := tl.At(tt.at)
		if got != tt.want || done != tt.wantDone {
			t.Errorf("At(%v) = %+v, %v; want %+v, %v", tt.at, got, done, tt.want, tt.wantDone)
		}
	}
	if total, finite := tl.Total(); !finite || total != 5*time.Minute {
		t.Errorf("Total = %v, %v", total, finite)
	}
}

func TestOpenEndedNeverFinishes(t *testing.T) {
	tl := mustEffect(t, libFrom(t, map[string]string{"b": `
stages:
  - primitive: breathe
    settings: { color: blue, frequency_hz: 1, duty_cycle: 0.5 }
`}), "b")
	if _, done := tl.At(24 * time.Hour); done {
		t.Error("open-ended effect reported done")
	}
	if _, finite := tl.Total(); finite {
		t.Error("open-ended effect reported finite total")
	}
}

const inner = `
stages:
  - { duration: 1s, color: red }
  - { duration: 1s, color: "#00ff00" }
final_state: blue
`

func TestNestedCutOffHoldAndDefaultDuration(t *testing.T) {
	l := libFrom(t, map[string]string{
		"inner": inner,
		"cut": `
stages:
  - { duration: 1500ms, effect: inner }
  - { duration: 1s, color: white }
final_state: black
`,
		"hold": `
stages:
  - { duration: 3s, effect: inner }
final_state: black
`,
		"auto": `
stages:
  - { effect: inner }
  - { duration: 1s, color: white }
final_state: black
`,
	})
	checks := []struct {
		effect string
		at     time.Duration
		want   color.HSV
	}{
		{"cut", 1200 * time.Millisecond, pureGreen},
		{"cut", 1500 * time.Millisecond, white}, // inner cut off
		{"hold", 2500 * time.Millisecond, blue}, // inner's final_state held
		{"auto", 1999 * time.Millisecond, pureGreen},
		{"auto", 2000 * time.Millisecond, white}, // stage took inner's 2s total
	}
	for _, c := range checks {
		if got, _ := mustEffect(t, l, c.effect).At(c.at); got != c.want {
			t.Errorf("%s.At(%v) = %+v, want %+v", c.effect, c.at, got, c.want)
		}
	}
}

func TestOpenEndedNestedAsLastStage(t *testing.T) {
	l := libFrom(t, map[string]string{
		"breathe_blue": "stages:\n  - primitive: breathe\n    settings: { color: blue, frequency_hz: 0.5, duty_cycle: 0.5 }\n",
		"flash_then_breathe": `
stages:
  - { duration: 1s, primitive: blink, settings: { color: red, frequency_hz: 3, duty_cycle: 0.5 } }
  - { effect: breathe_blue }
`,
	})
	if _, done := mustEffect(t, l, "flash_then_breathe").At(time.Hour); done {
		t.Error("outer effect should be open-ended")
	}
}

func TestUnknownEffectListsKnown(t *testing.T) {
	_, err := libFrom(t, map[string]string{"timer5min": timer5min}).Effect("timer5mn")
	if !errors.Is(err, ErrUnknownEffect) || !strings.Contains(err.Error(), "timer5min") {
		t.Errorf("err = %v, want ErrUnknownEffect listing timer5min", err)
	}
}

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		name    string
		effects map[string]string
	}{
		{"malformed yaml", map[string]string{"e": "stages: [\n"}},
		{"unknown effect field", map[string]string{"e": "stages:\n  - { duration: 1s, color: red }\nfinal_state: red\nloop: true\n"}},
		{"unknown stage field", map[string]string{"e": "stages:\n  - { duration: 1s, color: red, colour: blue }\nfinal_state: red\n"}},
		{"no stages", map[string]string{"e": "final_state: red\nstages: []\n"}},
		{"two kinds in a stage", map[string]string{"e": "stages:\n  - { color: red, primitive: blink }\n"}},
		{"no kind in a stage", map[string]string{"e": "stages:\n  - { duration: 1s }\nfinal_state: red\n"}},
		{"settings with color", map[string]string{"e": "stages:\n  - { duration: 1s, color: red, settings: { x: 1 } }\nfinal_state: red\n"}},
		{"settings with effect", map[string]string{
			"inner": inner,
			"e":     "stages:\n  - { effect: inner, settings: { x: 1 } }\nfinal_state: red\n",
		}},
		{"missing primitive setting", map[string]string{"e": "stages:\n  - { primitive: breathe, settings: { color: red, frequency_hz: 1 } }\n"}},
		{"missing duration mid-effect", map[string]string{"e": "stages:\n  - { color: red }\n  - { duration: 1s, color: blue }\nfinal_state: red\n"}},
		{"zero duration", map[string]string{"e": "stages:\n  - { duration: 0s, color: red }\nfinal_state: red\n"}},
		{"bad duration", map[string]string{"e": "stages:\n  - { duration: soon, color: red }\nfinal_state: red\n"}},
		{"missing final_state when finite", map[string]string{"e": "stages:\n  - { duration: 1s, color: red }\n"}},
		{"bad final_state when open-ended", map[string]string{"e": "stages:\n  - { color: red }\nfinal_state: nope\n"}},
		{"bad color", map[string]string{"e": "stages:\n  - { duration: 1s, color: nope }\nfinal_state: red\n"}},
		{"unknown nested effect", map[string]string{"e": "stages:\n  - { duration: 1s, effect: ghost }\nfinal_state: red\n"}},
		{"self reference", map[string]string{"e": "stages:\n  - { duration: 1s, effect: e }\nfinal_state: red\n"}},
		{"cycle", map[string]string{
			"a": "stages:\n  - { duration: 1s, effect: b }\nfinal_state: red\n",
			"b": "stages:\n  - { duration: 1s, effect: a }\nfinal_state: red\n",
		}},
		{"open-ended nested not last", map[string]string{
			"open": "stages:\n  - { color: red }\n",
			"e":    "stages:\n  - { effect: open }\n  - { duration: 1s, color: blue }\nfinal_state: red\n",
		}},
	}
	for _, tt := range tests {
		if _, err := compileEffects(sources(tt.effects)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", tt.name, err)
		}
	}
}

func TestCompileErrorNamesFile(t *testing.T) {
	_, err := compileEffects(sources(map[string]string{"broken": "stages: []\n"}))
	if err == nil || !strings.Contains(err.Error(), "effects/broken.yaml") {
		t.Errorf("err = %v, want one naming effects/broken.yaml", err)
	}
}
