package dispatcher

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/hid"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

type opKind int

const (
	opFlush  opKind = iota // deliver one cached LED; no reply
	opRedraw               // expanded at dispatch time into flushes of the whole cache; no reply
	opListDevices
	opGetCapabilities
)

type job struct {
	kind   opKind
	device string
	index  uint16         // valid when kind == opFlush
	reply  chan jobResult // nil for opFlush and opRedraw
}

type jobResult struct {
	err  error
	list []DeviceSummary
	caps Capabilities
}

// Dispatcher serializes all access to registry's open hid.Controllers.
type Dispatcher struct {
	registry *Registry
	cache    *Cache
	queue    chan job
	logger   *slog.Logger
}

// New creates a Dispatcher. queueDepth bounds in-flight requests (spec:
// e.g. 64) — a full queue fails fast with ErrQueueFull rather than growing
// goroutines/memory without bound.
func New(registry *Registry, cache *Cache, queueDepth int, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{registry: registry, cache: cache, queue: make(chan job, queueDepth), logger: logger}
}

func (d *Dispatcher) submit(ctx context.Context, j job) (jobResult, error) {
	select {
	case d.queue <- j:
	default:
		return jobResult{}, ErrQueueFull
	}
	select {
	case res := <-j.reply:
		return res, nil
	case <-ctx.Done():
		return jobResult{}, ctx.Err()
	}
}

// ListDevices returns every currently registered device and whether it's
// presently connected.
func (d *Dispatcher) ListDevices(ctx context.Context) ([]DeviceSummary, error) {
	res, err := d.submit(ctx, job{kind: opListDevices, reply: make(chan jobResult, 1)})
	if err != nil {
		return nil, err
	}
	return res.list, res.err
}

// GetCapabilities returns device's LED count and matrix positions. Stored
// capabilities (kept on the registry slot, surviving Untethered) are
// returned without touching the queue; otherwise a Connected device is
// queried once through the dispatcher goroutine and the result stored.
func (d *Dispatcher) GetCapabilities(ctx context.Context, device string) (Capabilities, error) {
	caps, known, exists := d.registry.Caps(device)
	if !exists {
		return Capabilities{}, ErrDeviceNotFound
	}
	if known {
		return caps, nil
	}
	res, err := d.submit(ctx, job{kind: opGetCapabilities, device: device, reply: make(chan jobResult, 1)})
	if err != nil {
		return Capabilities{}, err
	}
	return res.caps, res.err
}

// Run is the single dispatcher goroutine: it owns every hid.Controller in
// registry exclusively. On each cycle it takes one job, non-blockingly
// drains any others already queued, then processes them as a batch. It
// runs until ctx is canceled.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		select {
		case first := <-d.queue:
			d.process(d.drainAfter(first))
		case <-ctx.Done():
			return
		}
	}
}

func (d *Dispatcher) drainAfter(first job) []job {
	batch := []job{first}
	for {
		select {
		case j := <-d.queue:
			batch = append(batch, j)
		default:
			return batch
		}
	}
}

func (d *Dispatcher) process(batch []job) {
	for _, group := range groupForSend(dedupeFlushes(d.expandRedraws(batch))) {
		d.dispatchGroup(group)
	}
}

// expandRedraws replaces each redraw job with flushes of every index
// cached for its device at this moment.
func (d *Dispatcher) expandRedraws(batch []job) []job {
	out := make([]job, 0, len(batch))
	for _, j := range batch {
		if j.kind != opRedraw {
			out = append(out, j)
			continue
		}
		for _, k := range d.cache.Snapshot(j.device) {
			out = append(out, job{kind: opFlush, device: j.device, index: k.Index})
		}
	}
	return out
}

func (d *Dispatcher) dispatchGroup(group []job) {
	switch group[0].kind {
	case opFlush:
		d.dispatchFlush(group)
	case opListDevices:
		group[0].reply <- jobResult{list: d.registry.Summaries()}
	case opGetCapabilities:
		caps, err := d.fetchCapabilities(group[0].device)
		group[0].reply <- jobResult{caps: caps, err: err}
	}
}

// dispatchFlush sends one contiguous run of LEDs, reading each one's color
// from the cache now — never a color captured at enqueue time, so a flush
// can't deliver a stale frame. A non-Connected device is skipped silently;
// a HID error is logged and otherwise ignored (the next poll marks the
// device Untethered, and reconnect redraw catches it up).
func (d *Dispatcher) dispatchFlush(group []job) {
	device := group[0].device
	ctrl, ok := d.registry.Get(device)
	if !ok {
		return
	}
	keys := make([]hid.KeyColor, 0, len(group))
	for _, j := range group {
		k, ok := d.cache.Get(device, j.index)
		if !ok {
			return // device's cache was forgotten (evicted) after this flush was queued
		}
		keys = append(keys, k)
	}
	if err := ctrl.SetKeys(keys); err != nil {
		d.logger.Warn("hid write failed", "device", device, "err", err)
	}
}

// tryEnqueue queues j without blocking; a full queue drops it (the cache
// already holds the truth, and the next periodic redraw delivers it).
func (d *Dispatcher) tryEnqueue(j job) {
	select {
	case d.queue <- j:
	default:
		d.logger.Debug("dispatcher queue full, dropping job", "device", j.device, "kind", int(j.kind))
	}
}

// Write sets addr on device to c in the frame buffer and schedules delivery.
// It never blocks and never waits on hardware: the cache is updated
// synchronously (or, for a device whose capabilities aren't known yet, the
// write is kept pending on its registry slot), and a colorless flush is
// queued best-effort. A Name addr claims (or reuses) a key via the
// registry's named-key pool — see Canonical, which does this ahead of Write
// for the normal HTTP request path; Write only still needs to do it itself
// for the rare pending write whose caps became known between Canonical and
// Write.
func (d *Dispatcher) Write(device string, addr keyaddr.Address, c color.HSV) error {
	caps, pended, exists := d.registry.CapsOrPend(device, PendingWrite{Addr: addr, Color: c})
	if !exists {
		return ErrDeviceNotFound
	}
	if pended {
		return nil
	}
	idx, err := d.resolve(device, addr, caps)
	if err != nil {
		return err
	}
	d.cache.Update(device, []hid.KeyColor{{Index: idx, H: c.H, S: c.S, V: c.V}})
	d.tryEnqueue(job{kind: opFlush, device: device, index: idx})
	return nil
}

// resolve maps addr to an LED index given device's known caps: a Name addr
// claims (or reuses) a pooled key; any other form resolves against the
// matrix as before. It never marks a key claimed-by-direct — only a caller
// that still holds the address's original, pre-canonicalization kind (
// Canonical; SetCaps's pending-write resolution) does that, since by the
// time Write sees an addr it may already be the resolved led:N form of a
// name claim.
func (d *Dispatcher) resolve(device string, addr keyaddr.Address, caps Capabilities) (uint16, error) {
	if addr.Kind == keyaddr.Name {
		return d.registry.ClaimOrGet(device, addr.Name, time.Now())
	}
	idx, ok := keyaddr.Resolve(addr, caps.LEDCount, caps.Positions)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrKeyNotFound, addr)
	}
	return idx, nil
}

// Canonical resolves addr to led:N, so every form naming one key maps to one
// effects-engine target. A Name addr claims (or reuses) a key from the
// device's named-key pool; any other form marks its resolved index
// claimed-by-direct, releasing any name claim that held it (see MarkDirect).
// If device's capabilities aren't known yet, Canonical fetches them first
// (blocking on the dispatcher, same as GetCapabilities) rather than
// returning addr unresolved — a caller keying an effects.Target off an
// unresolved address would get a different map key than a later write to
// the same physical key made after caps became known, so two independently
// ticking effects could end up racing to paint one LED.
func (d *Dispatcher) Canonical(ctx context.Context, device string, addr keyaddr.Address) (keyaddr.Address, error) {
	caps, err := d.GetCapabilities(ctx, device)
	if err != nil {
		return keyaddr.Address{}, err
	}
	if addr.Kind == keyaddr.Name {
		idx, err := d.registry.ClaimOrGet(device, addr.Name, time.Now())
		if err != nil {
			return keyaddr.Address{}, err
		}
		return keyaddr.Address{Kind: keyaddr.LED, N: idx}, nil
	}
	idx, ok := keyaddr.Resolve(addr, caps.LEDCount, caps.Positions)
	if !ok {
		return keyaddr.Address{}, fmt.Errorf("%w: %s", ErrKeyNotFound, addr)
	}
	d.registry.MarkDirect(device, idx)
	return keyaddr.Address{Kind: keyaddr.LED, N: idx}, nil
}

// ReleaseClaim releases name's claim on device, returning it to the pool.
func (d *Dispatcher) ReleaseClaim(device, name string) error {
	return d.registry.ReleaseClaim(device, name)
}

// RunClaimSweep releases every named claim whose last write is older than
// maxAge, checking every interval, until ctx is canceled. onRelease, if
// non-nil, is passed through to Registry.SweepIdleClaims — see its doc
// comment for why blanking the freed key's LED can't happen here.
func (d *Dispatcher) RunClaimSweep(ctx context.Context, interval, maxAge time.Duration, onRelease func(device string, index uint16)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.registry.SweepIdleClaims(time.Now(), maxAge, onRelease)
		case <-ctx.Done():
			return
		}
	}
}

// ResolveDevice maps a {name} path segment (name or ordinal) to a device name.
func (d *Dispatcher) ResolveDevice(ref string) (string, bool) {
	return d.registry.ResolveDevice(ref)
}

// fetchCapabilities runs on the dispatcher goroutine. It re-checks for
// stored caps (a concurrent request may have fetched them since this job
// was queued), else queries the device and stores the result, resolving
// any pending writes into the cache.
func (d *Dispatcher) fetchCapabilities(device string) (Capabilities, error) {
	if caps, known, _ := d.registry.Caps(device); known {
		return caps, nil
	}
	ctrl, ok := d.registry.Get(device)
	if !ok {
		return Capabilities{}, ErrCapsUnknown
	}
	caps, err := queryCapabilities(ctrl)
	if err != nil {
		d.registry.RecordCapsFailure(device)
		return Capabilities{}, err
	}
	d.registry.SetCaps(device, caps,
		func(idx uint16, c color.HSV) {
			d.cache.Update(device, []hid.KeyColor{{Index: idx, H: c.H, S: c.S, V: c.V}})
			d.tryEnqueue(job{kind: opFlush, device: device, index: idx})
		},
		func(w PendingWrite, err error) {
			d.logger.Warn("dropping pending write", "device", device, "addr", w.Addr.String(), "err", err)
		},
	)
	return caps, nil
}

func queryCapabilities(ctrl hid.Controller) (Capabilities, error) {
	n, err := ctrl.GetNumberLEDs()
	if err != nil {
		return Capabilities{}, err
	}
	caps := Capabilities{LEDCount: int(n)}
	for i := uint16(0); i < n; i++ {
		row, col, err := ctrl.GetLEDInfo(i)
		if err != nil {
			return Capabilities{}, err
		}
		caps.Positions = append(caps.Positions, LEDPosition{Index: i, Row: row, Col: col})
	}
	return caps, nil
}
