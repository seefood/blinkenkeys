package dispatcher

import (
	"context"
	"log/slog"
	"time"
)

// RedrawInterval is the unconditional periodic redraw cadence from the
// design spec's "State persistence & refresh": every registered device's
// full cached color set is replayed every 5 seconds, always, regardless of
// reconnect detection.
const RedrawInterval = 5 * time.Second

// Redraw replays device's full cached color set through the normal SetKey
// dispatch path (so it's serialized/batched exactly like any other write).
// A device with an empty cache (never had a color set) is a no-op.
func (d *Dispatcher) Redraw(ctx context.Context, device string) error {
	for _, k := range d.cache.Snapshot(device) {
		if err := d.SetKey(ctx, device, k.Index, k.H, k.S, k.V); err != nil {
			return err
		}
	}
	return nil
}

// RunPeriodicRedraw redraws every currently Connected device's cache every
// interval, independent of any reconnect detection, until ctx is canceled.
// Untethered devices (Registry.Summaries now reports those too, per Task 6)
// are skipped — there's no live controller to write to until a rewire
// reconnects one.
func (d *Dispatcher) RunPeriodicRedraw(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			for _, dev := range d.registry.Summaries() {
				if !dev.Connected {
					continue
				}
				if err := d.Redraw(ctx, dev.Name); err != nil {
					logger.Warn("periodic redraw failed", "device", dev.Name, "err", err)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// RedrawReconnected redraws every name in reconnected — the set returned by
// Registry.Reconcile when an Untethered slot rewires back to Connected — so
// a replug doesn't have to wait for RunPeriodicRedraw's next tick.
func (d *Dispatcher) RedrawReconnected(ctx context.Context, reconnected []string, logger *slog.Logger) {
	for _, name := range reconnected {
		if err := d.Redraw(ctx, name); err != nil {
			logger.Warn("reconnect redraw failed", "device", name, "err", err)
		}
	}
}
