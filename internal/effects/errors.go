// Package effects implements Phase 3's animation model: Go-coded primitives,
// YAML-defined multi-stage effects compiled into timelines, templates
// mapping application states to colors or effects, and the Engine that runs
// effects on keys.
package effects

import "errors"

// ErrInvalid marks a malformed effect or template definition — fatal at
// load time.
var ErrInvalid = errors.New("effects: invalid")

// ErrUnknownEffect is returned for a request naming an effect that doesn't
// exist — internal/api maps it to a 404.
var ErrUnknownEffect = errors.New("effects: unknown effect")

// ErrUnknownState is returned for a request naming a template state that
// doesn't exist — internal/api maps it to a 404.
var ErrUnknownState = errors.New("effects: unknown template state")
