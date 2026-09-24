package effects

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
)

// stage is one compiled stage. dur == 0 marks an open-ended stage, which
// is only ever the last one.
type stage struct {
	dur   time.Duration
	frame func(elapsed time.Duration) color.HSV
}

// Timeline is one compiled effect. It is immutable, so a single Timeline is
// shared by every key running that effect; each run's start time is kept
// by the Engine.
type Timeline struct {
	stages []stage
	final  color.HSV
}

// At returns the effect's color at elapsed time since it started, and
// whether it has ended (the color is then its final_state). Timing comes
// from elapsed wall-clock time, never frame counts, so it doesn't drift.
func (t *Timeline) At(elapsed time.Duration) (color.HSV, bool) {
	for _, s := range t.stages {
		if s.dur == 0 || elapsed < s.dur {
			return s.frame(elapsed), false
		}
		elapsed -= s.dur
	}
	return t.final, true
}

// Total returns the effect's total duration, or false if it's open-ended.
func (t *Timeline) Total() (time.Duration, bool) {
	var total time.Duration
	for _, s := range t.stages {
		if s.dur == 0 {
			return 0, false
		}
		total += s.dur
	}
	return total, true
}

// Action is what a template state maps to: exactly one of Color or Timeline.
type Action struct {
	Color    *color.HSV
	Timeline *Timeline
}

// Library is the loaded, fully compiled set of effects and template states.
// Read-only after construction; safe for concurrent use.
type Library struct {
	effects map[string]*Timeline
	states  map[string]Action // "program/state" -> action
}

// Effect returns the compiled effect called name.
func (l *Library) Effect(name string) (*Timeline, error) {
	tl, ok := l.effects[name]
	if !ok {
		return nil, fmt.Errorf("%w %q; known: %s", ErrUnknownEffect, name, strings.Join(sortedKeys(l.effects), ", "))
	}
	return tl, nil
}

// compileEffects decodes and compiles every effect definition (name ->
// YAML source). Any invalid definition fails the whole set, naming its file.
func compileEffects(src map[string][]byte) (map[string]*Timeline, error) {
	c := &compiler{defs: make(map[string]*effectDef, len(src)), done: make(map[string]*Timeline, len(src))}
	for _, name := range sortedKeys(src) {
		var def effectDef
		if err := decodeStrict(src[name], &def); err != nil {
			return nil, fmt.Errorf("effects/%s.yaml: %w: %v", name, ErrInvalid, err)
		}
		c.defs[name] = &def
	}
	for _, name := range sortedKeys(c.defs) {
		if _, err := c.compile(name, nil); err != nil {
			return nil, fmt.Errorf("effects/%s.yaml: %w", name, err)
		}
	}
	return c.done, nil
}

// compiler memoizes compiled effects so a nested effect compiles once and
// its Timeline is shared by every effect that references it.
type compiler struct {
	defs map[string]*effectDef
	done map[string]*Timeline
}

// compile compiles effect name; stack is the chain of effects currently
// being compiled, for cycle detection.
func (c *compiler) compile(name string, stack []string) (*Timeline, error) {
	if tl, ok := c.done[name]; ok {
		return tl, nil
	}
	def, ok := c.defs[name]
	if !ok {
		return nil, fmt.Errorf("%w: unknown effect %q", ErrInvalid, name)
	}
	if slices.Contains(stack, name) {
		return nil, fmt.Errorf("%w: effect cycle %s -> %s", ErrInvalid, strings.Join(stack, " -> "), name)
	}
	stack = append(slices.Clone(stack), name)

	if len(def.Stages) == 0 {
		return nil, fmt.Errorf("%w: effect %q has no stages", ErrInvalid, name)
	}
	tl := &Timeline{}
	for i, sd := range def.Stages {
		st, err := c.compileStage(sd, i == len(def.Stages)-1, stack)
		if err != nil {
			return nil, fmt.Errorf("effect %q stage %d: %w", name, i, err)
		}
		tl.stages = append(tl.stages, st)
	}
	_, finite := tl.Total()
	switch {
	case def.FinalState != "":
		final, err := parseColor(def.FinalState)
		if err != nil {
			return nil, fmt.Errorf("effect %q final_state: %w", name, err)
		}
		tl.final = final
	case finite:
		return nil, fmt.Errorf("%w: effect %q ends but has no final_state", ErrInvalid, name)
	}
	c.done[name] = tl
	return tl, nil
}

func (c *compiler) compileStage(sd stageDef, last bool, stack []string) (stage, error) {
	kinds := 0
	for _, s := range []string{sd.Color, sd.Primitive, sd.Effect} {
		if s != "" {
			kinds++
		}
	}
	if kinds != 1 {
		return stage{}, fmt.Errorf("%w: need exactly one of color, primitive, effect", ErrInvalid)
	}
	if sd.Settings != nil && sd.Primitive == "" {
		return stage{}, fmt.Errorf("%w: settings is only valid with primitive", ErrInvalid)
	}
	var dur time.Duration
	if sd.Duration != "" {
		d, err := parseDuration(sd.Duration)
		if err != nil {
			return stage{}, err
		}
		dur = d
	}
	var frame func(time.Duration) color.HSV
	switch {
	case sd.Color != "":
		c, err := parseColor(sd.Color)
		if err != nil {
			return stage{}, err
		}
		frame = func(time.Duration) color.HSV { return c }
	case sd.Primitive != "":
		f, err := bindPrimitive(sd.Primitive, sd.Settings)
		if err != nil {
			return stage{}, err
		}
		frame = f
	default:
		nested, err := c.compile(sd.Effect, stack)
		if err != nil {
			return stage{}, err
		}
		if dur == 0 {
			if total, finite := nested.Total(); finite {
				dur = total
			}
		}
		frame = func(el time.Duration) color.HSV {
			col, _ := nested.At(el)
			return col
		}
	}
	if dur == 0 && !last {
		return stage{}, fmt.Errorf("%w: duration is required on every stage but the last (and an open-ended nested effect can only be last)", ErrInvalid)
	}
	return stage{dur: dur, frame: frame}, nil
}

// parseColor is color.ParseHSV with errors marked ErrInvalid.
func parseColor(s string) (color.HSV, error) {
	c, err := color.ParseHSV(s)
	if err != nil {
		return color.HSV{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return c, nil
}

func parseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%w: duration %q: %v (want e.g. 1500ms, 90s, 3m)", ErrInvalid, s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%w: duration %q must be > 0", ErrInvalid, s)
	}
	return d, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
