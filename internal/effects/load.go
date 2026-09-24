package effects

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var namePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

// Load reads dir/effects/*.yaml (effect name = file name) and
// dir/templates/*.yaml (namespace = file name), compiling every effect and
// resolving every template state now, so an invalid definition is reported
// at startup (or by -check-config) rather than at request time. Missing
// directories are fine; non-.yaml files are ignored.
func Load(dir string) (*Library, error) {
	effectSrc, err := readYAMLDir(filepath.Join(dir, "effects"))
	if err != nil {
		return nil, err
	}
	compiled, err := compileEffects(effectSrc)
	if err != nil {
		return nil, err
	}
	templateSrc, err := readYAMLDir(filepath.Join(dir, "templates"))
	if err != nil {
		return nil, err
	}
	states, err := compileTemplates(templateSrc, compiled)
	if err != nil {
		return nil, err
	}
	return &Library{effects: compiled, states: states}, nil
}

// State returns the action for a "program/state" template reference.
func (l *Library) State(ref string) (Action, error) {
	act, ok := l.states[ref]
	if !ok {
		return Action{}, fmt.Errorf("%w %q; known: %s", ErrUnknownState, ref, strings.Join(sortedKeys(l.states), ", "))
	}
	return act, nil
}

// compileTemplates decodes every template file (program -> YAML source) and
// resolves each state against the compiled effects.
func compileTemplates(src map[string][]byte, effects map[string]*Timeline) (map[string]Action, error) {
	out := make(map[string]Action)
	for _, program := range sortedKeys(src) {
		var states map[string]stateDef
		if err := decodeStrict(src[program], &states); err != nil {
			return nil, fmt.Errorf("templates/%s.yaml: %w: %v", program, ErrInvalid, err)
		}
		for _, state := range sortedKeys(states) {
			if !namePattern.MatchString(state) {
				return nil, fmt.Errorf("templates/%s.yaml: %w: state name %q must match [a-z0-9_-]+", program, ErrInvalid, state)
			}
			act, err := resolveState(states[state], effects)
			if err != nil {
				return nil, fmt.Errorf("templates/%s.yaml: state %q: %w", program, state, err)
			}
			out[program+"/"+state] = act
		}
	}
	return out, nil
}

func resolveState(sd stateDef, effects map[string]*Timeline) (Action, error) {
	switch {
	case (sd.Color == "") == (sd.Effect == ""):
		return Action{}, fmt.Errorf("%w: need exactly one of color, effect", ErrInvalid)
	case sd.Color != "":
		c, err := parseColor(sd.Color)
		if err != nil {
			return Action{}, err
		}
		return Action{Color: &c}, nil
	default:
		tl, ok := effects[sd.Effect]
		if !ok {
			return Action{}, fmt.Errorf("%w: unknown effect %q", ErrInvalid, sd.Effect)
		}
		return Action{Timeline: tl}, nil
	}
}

// readYAMLDir returns name -> content for every *.yaml file in dir (name =
// file name without .yaml). A missing dir yields an empty map.
func readYAMLDir(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("effects: read %s: %w", dir, err)
	}
	out := make(map[string][]byte)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		if !namePattern.MatchString(name) {
			return nil, fmt.Errorf("%s: %w: file name must match [a-z0-9_-]+.yaml", filepath.Join(dir, e.Name()), ErrInvalid)
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- operator-supplied config dir, not untrusted network input
		if err != nil {
			return nil, fmt.Errorf("effects: read %s: %w", e.Name(), err)
		}
		out[name] = data
	}
	return out, nil
}
