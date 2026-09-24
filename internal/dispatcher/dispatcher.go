package dispatcher

import (
	"context"
	"log/slog"

	"github.com/seefood/blinkenkeys/internal/hid"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

type opKind int

const (
	opSetKey opKind = iota
	opListDevices
	opGetCapabilities
)

type job struct {
	kind   opKind
	device string
	key    hid.KeyColor // valid when kind == opSetKey
	reply  chan jobResult
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

// SetKey submits one LED color change and blocks for its result.
func (d *Dispatcher) SetKey(ctx context.Context, device string, index uint16, h, s, v uint8) error {
	res, err := d.submit(ctx, job{
		kind:   opSetKey,
		device: device,
		key:    hid.KeyColor{Index: index, H: h, S: s, V: v},
		reply:  make(chan jobResult, 1),
	})
	if err != nil {
		return err
	}
	return res.err
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
// registry exclusively. On each cycle it takes one job, then
// non-blockingly drains any others already queued and batches same-device
// contiguous SetKey jobs (see batch.go) before issuing them. It runs until
// ctx is canceled.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		var first job
		select {
		case first = <-d.queue:
		case <-ctx.Done():
			return
		}
		batch := []job{first}
	drain:
		for {
			select {
			case j := <-d.queue:
				batch = append(batch, j)
			default:
				break drain
			}
		}
		for _, group := range groupForSend(batch) {
			d.dispatchGroup(group)
		}
	}
}

func (d *Dispatcher) dispatchGroup(group []job) {
	switch group[0].kind {
	case opSetKey:
		d.dispatchSetKeys(group)
	case opListDevices:
		group[0].reply <- jobResult{list: d.registry.Summaries()}
	case opGetCapabilities:
		caps, err := d.fetchCapabilities(group[0].device)
		group[0].reply <- jobResult{caps: caps, err: err}
	}
}

func (d *Dispatcher) dispatchSetKeys(group []job) {
	device := group[0].device
	ctrl, ok := d.registry.Get(device)
	if !ok {
		failAll(group, ErrDeviceNotFound)
		return
	}
	keys := make([]hid.KeyColor, len(group))
	for i, j := range group {
		keys[i] = j.key
	}
	err := ctrl.SetKeys(keys)
	if err == nil {
		d.cache.Update(device, keys)
	}
	for _, j := range group {
		j.reply <- jobResult{err: err}
	}
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
		return Capabilities{}, err
	}
	d.registry.SetCaps(device, caps, func(pending []PendingWrite) {
		d.applyPending(device, caps, pending)
	})
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

// applyPending resolves pending writes (arrival order, so the latest write
// per key wins) into the cache. Called by Registry.SetCaps under its lock.
func (d *Dispatcher) applyPending(device string, caps Capabilities, pending []PendingWrite) {
	for _, w := range pending {
		idx, ok := keyaddr.Resolve(w.Addr, caps.LEDCount, caps.Positions)
		if !ok {
			d.logger.Warn("dropping pending write: key not on device", "device", device, "addr", w.Addr.String())
			continue
		}
		d.cache.Update(device, []hid.KeyColor{{Index: idx, H: w.Color.H, S: w.Color.S, V: w.Color.V}})
	}
}

func failAll(group []job, err error) {
	for _, j := range group {
		j.reply <- jobResult{err: err}
	}
}
