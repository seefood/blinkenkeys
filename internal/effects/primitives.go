package effects

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
)

type settingKind int

const (
	kindColor settingKind = iota
	kindFrequency
	kindDuty
)

// settingSpec declares one primitive setting. Every setting is required.
type settingSpec struct {
	name string
	kind settingKind
}

// values holds bound, validated settings: colors as color.HSV, numbers as float64.
type values map[string]any

type primitive struct {
	settings []settingSpec
	frame    func(v values, elapsed time.Duration) color.HSV
}

var waveSettings = []settingSpec{
	{name: "frequency_hz", kind: kindFrequency},
	{name: "duty_cycle", kind: kindDuty},
}

// primitives is the registry of animation primitives, keyed by the name YAML
// uses. Read-only after package init; adding a primitive means adding an
// entry here — nothing else changes.
var primitives = map[string]primitive{
	"breathe": {
		settings: append([]settingSpec{{name: "color", kind: kindColor}}, waveSettings...),
		frame:    breatheFrame,
	},
	"alternate": {
		settings: append([]settingSpec{{name: "color_a", kind: kindColor}, {name: "color_b", kind: kindColor}}, waveSettings...),
		frame:    alternateFrame,
	},
	"blink": {
		settings: append([]settingSpec{{name: "color", kind: kindColor}}, waveSettings...),
		frame:    blinkFrame,
	},
}

// bindPrimitive validates raw against primitive name's declared settings —
// all required, none unknown — and returns the primitive's frame function.
func bindPrimitive(name string, raw map[string]any) (func(time.Duration) color.HSV, error) {
	p, ok := primitives[name]
	if !ok {
		known := make([]string, 0, len(primitives))
		for k := range primitives {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("%w: unknown primitive %q; known: %s", ErrInvalid, name, strings.Join(known, ", "))
	}
	vals := make(values, len(p.settings))
	for _, spec := range p.settings {
		v, ok := raw[spec.name]
		if !ok {
			return nil, fmt.Errorf("%w: primitive %s: missing setting %q", ErrInvalid, name, spec.name)
		}
		bound, err := bindValue(spec, v)
		if err != nil {
			return nil, fmt.Errorf("%w: primitive %s: setting %q: %v", ErrInvalid, name, spec.name, err)
		}
		vals[spec.name] = bound
	}
	for k := range raw {
		if _, ok := vals[k]; !ok {
			return nil, fmt.Errorf("%w: primitive %s: unknown setting %q", ErrInvalid, name, k)
		}
	}
	return func(elapsed time.Duration) color.HSV { return p.frame(vals, elapsed) }, nil
}

func bindValue(spec settingSpec, v any) (any, error) {
	if spec.kind == kindColor {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("want a color string, got %v", v)
		}
		return color.ParseHSV(s)
	}
	f, ok := toFloat(v)
	if !ok {
		return nil, fmt.Errorf("want a number, got %v", v)
	}
	switch {
	case spec.kind == kindFrequency && f <= 0:
		return nil, fmt.Errorf("must be > 0, got %v", f)
	case spec.kind == kindDuty && (f < 0 || f > 1):
		return nil, fmt.Errorf("must be between 0 and 1, got %v", f)
	}
	return f, nil
}

// toFloat accepts every numeric type YAML decoding into any produces.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case uint64:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// phase is the fraction [0,1) of the current period elapsed.
func phase(v values, elapsed time.Duration) float64 {
	p := elapsed.Seconds() * v["frequency_hz"].(float64)
	return p - math.Floor(p)
}

// breatheFrame scales color's V along a triangle wave: rising for
// duty_cycle of each period, falling for the rest; H and S held.
func breatheFrame(v values, elapsed time.Duration) color.HSV {
	c := v["color"].(color.HSV)
	d := v["duty_cycle"].(float64)
	ph := phase(v, elapsed)
	var level float64
	if ph < d {
		level = ph / d
	} else {
		level = (1 - ph) / (1 - d)
	}
	return color.HSV{H: c.H, S: c.S, V: uint8(math.Round(float64(c.V) * level))} // #nosec G115 -- level is in [0,1], so the result is <= c.V
}

// alternateFrame is a square wave: color_a for duty_cycle of each period,
// then color_b.
func alternateFrame(v values, elapsed time.Duration) color.HSV {
	if phase(v, elapsed) < v["duty_cycle"].(float64) {
		return v["color_a"].(color.HSV)
	}
	return v["color_b"].(color.HSV)
}

// blinkFrame is alternate with color_b = black.
func blinkFrame(v values, elapsed time.Duration) color.HSV {
	return alternateFrame(values{
		"color_a":      v["color"],
		"color_b":      color.HSV{},
		"frequency_hz": v["frequency_hz"],
		"duty_cycle":   v["duty_cycle"],
	}, elapsed)
}
