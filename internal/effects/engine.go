package effects

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// TickInterval is the engine's frame rate: 5 fps.
const TickInterval = 200 * time.Millisecond

// Target is one key effects run on. Addr is canonical (led:N) whenever the
// device's capabilities are known — see dispatcher.Canonical.
type Target struct {
	Device string
	Addr   keyaddr.Address
}

// Setter is the frame-buffer write the engine drives; *dispatcher.Dispatcher
// implements it. Write must not block.
type Setter interface {
	Write(device string, addr keyaddr.Address, c color.HSV) error
}

type running struct {
	tl    *Timeline
	start time.Time
	last  color.HSV
}

// Engine owns every running effect, keyed by target, and is the only write
// path the HTTP layer uses — so "the new command always wins" is enforced
// in one place. The mutex is held across Setter.Write calls, which is fine
// because Write never blocks.
type Engine struct {
	mu      sync.Mutex
	out     Setter
	running map[Target]*running
	logger  *slog.Logger
}

// NewEngine creates an Engine writing through out.
func NewEngine(out Setter, logger *slog.Logger) *Engine {
	return &Engine{out: out, running: make(map[Target]*running), logger: logger}
}

// SetColor writes c to t and cancels any effect running there (without its
// final_state — the new color replaces it immediately).
func (e *Engine) SetColor(t Target, c color.HSV) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.out.Write(t.Device, t.Addr, c); err != nil {
		return err
	}
	delete(e.running, t)
	return nil
}

// Start writes tl's first frame to t and runs tl there from now on,
// replacing any effect already running on t without its final_state.
func (e *Engine) Start(t Target, tl *Timeline, now time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, _ := tl.At(0)
	if err := e.out.Write(t.Device, t.Addr, c); err != nil {
		return err
	}
	e.running[t] = &running{tl: tl, start: now, last: c}
	return nil
}

// Tick advances every running effect to now: writes each frame that
// changed since the last one emitted, writes final_state for effects that
// ended and removes them, and drops (with a warning) any effect whose
// write fails.
func (e *Engine) Tick(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for t, r := range e.running {
		elapsed := now.Sub(r.start)
		if elapsed < 0 {
			// Start's now (captured before it takes e.mu) can be a hair
			// after this tick's now if the two race; clamp instead of
			// handing Timeline.At a negative duration, which would wrap to
			// just before the end of the timeline.
			elapsed = 0
		}
		c, done := r.tl.At(elapsed)
		if done || c != r.last {
			if err := e.out.Write(t.Device, t.Addr, c); err != nil {
				e.logger.Warn("effect write failed; stopping effect", "device", t.Device, "addr", t.Addr.String(), "err", err)
				delete(e.running, t)
				continue
			}
			r.last = c
		}
		if done {
			delete(e.running, t)
		}
	}
}

// Run calls Tick every interval until ctx is canceled.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			e.Tick(now)
		case <-ctx.Done():
			return
		}
	}
}
