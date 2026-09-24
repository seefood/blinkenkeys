package dispatcher

import (
	"context"
	"time"
)

// RedrawInterval is the unconditional periodic redraw cadence from the
// design spec's "State persistence & refresh": every registered device's
// full cached color set is replayed every 5 seconds, always, regardless of
// reconnect detection.
const RedrawInterval = 5 * time.Second

// Redraw schedules delivery of device's whole cached frame as a single
// queue job, expanded at dispatch time (so it reads the newest colors and
// can't overflow the queue on large boards). It never writes to the cache.
func (d *Dispatcher) Redraw(device string) {
	d.tryEnqueue(job{kind: opRedraw, device: device})
}

// EnsureCapabilities fetches and stores device's capabilities if the slot
// lacks them (resolving pending writes), then schedules a full redraw.
// Called by the poll loop for every connected device without capabilities
// and every reconnected one.
func (d *Dispatcher) EnsureCapabilities(ctx context.Context, device string) error {
	if _, err := d.GetCapabilities(ctx, device); err != nil {
		return err
	}
	d.Redraw(device)
	return nil
}

// RunPeriodicRedraw redraws every currently Connected device's cache every
// interval, independent of any reconnect detection, until ctx is canceled.
func (d *Dispatcher) RunPeriodicRedraw(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			for _, dev := range d.registry.Summaries() {
				if dev.Connected {
					d.Redraw(dev.Name)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
