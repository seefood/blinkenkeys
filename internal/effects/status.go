package effects

import "time"

// Origin is what a write asked for. Type is "color", "effect" or "state";
// Ref is the requested color string, effect name or program/state; Owner is
// an optional caller-supplied tag (blincli sends its terminal identity) used
// for conditional clears.
type Origin struct {
	Type  string
	Ref   string
	Owner string
}

// record is the engine's memory of the last API write to a target.
type record struct {
	origin Origin
	setAt  time.Time
	tl     *Timeline // nil unless the write started an effect
	start  time.Time
}

// Status is a key's last request and, if it started an effect, progress.
type Status struct {
	Origin Origin
	SetAt  time.Time
	Effect *EffectStatus
}

// EffectStatus is an effect's progress. Total is meaningful only if Finite.
type EffectStatus struct {
	Name    string
	Running bool
	Elapsed time.Duration
	Total   time.Duration
	Finite  bool
}

// statusLocked builds t's Status from rec; callers hold e.mu.
func (e *Engine) statusLocked(t Target, rec *record, now time.Time) Status {
	st := Status{Origin: rec.origin, SetAt: rec.setAt}
	if rec.tl == nil {
		return st
	}
	total, finite := rec.tl.Total()
	es := &EffectStatus{Name: rec.tl.Name, Total: total, Finite: finite}
	if _, running := e.running[t]; running {
		es.Running = true
		if es.Elapsed = now.Sub(rec.start); es.Elapsed < 0 {
			es.Elapsed = 0
		}
	} else if finite {
		es.Elapsed = total
	}
	st.Effect = es
	return st
}

// Status returns t's last-write record, if any. A record outlives its effect
// (Running turns false) and is dropped by the next plain SetColor/Start.
func (e *Engine) Status(t Target, now time.Time) (Status, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rec, ok := e.records[t]
	if !ok {
		return Status{}, false
	}
	return e.statusLocked(t, rec, now), true
}

// Statuses returns every record for device.
func (e *Engine) Statuses(device string, now time.Time) map[Target]Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[Target]Status)
	for t, rec := range e.records {
		if t.Device == device {
			out[t] = e.statusLocked(t, rec, now)
		}
	}
	return out
}
