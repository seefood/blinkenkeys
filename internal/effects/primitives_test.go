package effects

import (
	"errors"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
)

func mustBind(t *testing.T, name string, raw map[string]any) func(time.Duration) color.HSV {
	t.Helper()
	f, err := bindPrimitive(name, raw)
	if err != nil {
		t.Fatalf("bindPrimitive(%s, %v): %v", name, raw, err)
	}
	return f
}

var (
	red   = color.HSV{H: 0, S: 255, V: 255}
	green = color.HSV{H: 85, S: 255, V: 255}
	black = color.HSV{}
)

func TestBreathe(t *testing.T) {
	// "0,255,200": a color with V 200, so levels are easy to read.
	tests := []struct {
		duty  float64
		at    time.Duration
		wantV uint8
	}{
		{0.5, 0, 0},
		{0.5, 250 * time.Millisecond, 100},
		{0.5, 500 * time.Millisecond, 200},
		{0.5, 750 * time.Millisecond, 100},
		{0.5, 1000 * time.Millisecond, 0}, // next period
		{1.0, 500 * time.Millisecond, 100}, // rising sawtooth
		{0.0, 0, 200},                      // falling sawtooth
		{0.0, 500 * time.Millisecond, 100},
	}
	for _, tt := range tests {
		f := mustBind(t, "breathe", map[string]any{"color": "0,255,200", "frequency_hz": 1.0, "duty_cycle": tt.duty})
		got := f(tt.at)
		if got.V != tt.wantV || got.H != 0 || got.S != 255 {
			t.Errorf("breathe duty %.1f at %v = %+v, want V %d with H/S held", tt.duty, tt.at, got, tt.wantV)
		}
	}
}

func TestAlternateSwitchPoints(t *testing.T) {
	tests := []struct {
		duty float64
		at   time.Duration
		want color.HSV
	}{
		{0.2, 0, red},
		{0.2, 199 * time.Millisecond, red},
		{0.2, 200 * time.Millisecond, green},
		{0.2, 999 * time.Millisecond, green},
		{0.2, 1000 * time.Millisecond, red},
		{0.8, 799 * time.Millisecond, red},
		{0.8, 800 * time.Millisecond, green},
	}
	for _, tt := range tests {
		f := mustBind(t, "alternate", map[string]any{
			"color_a": "#ff0000", "color_b": "#00ff00", "frequency_hz": uint64(1), "duty_cycle": tt.duty,
		})
		if got := f(tt.at); got != tt.want {
			t.Errorf("alternate duty %.1f at %v = %+v, want %+v", tt.duty, tt.at, got, tt.want)
		}
	}
}

func TestBlinkIsAlternateWithBlack(t *testing.T) {
	blink := mustBind(t, "blink", map[string]any{"color": "#ff0000", "frequency_hz": 2.0, "duty_cycle": 0.5})
	alt := mustBind(t, "alternate", map[string]any{"color_a": "#ff0000", "color_b": "black", "frequency_hz": 2.0, "duty_cycle": 0.5})
	for ms := 0; ms < 1000; ms += 50 {
		at := time.Duration(ms) * time.Millisecond
		if blink(at) != alt(at) {
			t.Errorf("at %v blink = %+v, alternate-with-black = %+v", at, blink(at), alt(at))
		}
	}
	if blink(300*time.Millisecond) != black {
		t.Error("blink at 2Hz, 300ms should be in its black half")
	}
}

func TestBindPrimitiveErrors(t *testing.T) {
	// breathe returns a complete, valid breathe setting set, with over
	// applied and drop removed — so each case breaks exactly one thing.
	breathe := func(over map[string]any, drop ...string) map[string]any {
		m := map[string]any{"color": "red", "frequency_hz": 1.0, "duty_cycle": 0.5}
		for k, v := range over {
			m[k] = v
		}
		for _, k := range drop {
			delete(m, k)
		}
		return m
	}
	if _, err := bindPrimitive("breathe", breathe(nil)); err != nil {
		t.Fatalf("baseline breathe settings rejected: %v", err)
	}
	tests := []struct {
		name string
		prim string
		raw  map[string]any
	}{
		{"unknown primitive", "sparkle", breathe(nil)},
		{"missing color", "breathe", breathe(nil, "color")},
		{"missing frequency", "breathe", breathe(nil, "frequency_hz")},
		{"missing duty cycle", "breathe", breathe(nil, "duty_cycle")},
		{"unknown setting", "breathe", breathe(map[string]any{"speed": 1.0})},
		{"zero frequency", "breathe", breathe(map[string]any{"frequency_hz": 0.0})},
		{"negative frequency", "breathe", breathe(map[string]any{"frequency_hz": int64(-1)})},
		{"duty above 1", "breathe", breathe(map[string]any{"duty_cycle": 1.5})},
		{"bad color", "breathe", breathe(map[string]any{"color": "not-a-color"})},
		{"color not a string", "breathe", breathe(map[string]any{"color": 5.0})},
		{"number not a number", "breathe", breathe(map[string]any{"duty_cycle": "half"})},
	}
	for _, tt := range tests {
		if _, err := bindPrimitive(tt.prim, tt.raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", tt.name, err)
		}
	}
}
