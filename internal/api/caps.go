package api

import (
	"context"
	"sync"

	"github.com/seefood/blinkenkeys/internal/dispatcher"
)

// CapabilitiesCache implements CapabilitiesSource by calling
// Dispatcher.GetCapabilities on first use per device and caching the
// row,col->index mapping for subsequent PUTs. Phase 1+2 has no cache
// invalidation on device reconnect/hotplug — a later phase's job, not this
// one's; a device's physical matrix doesn't change across a replug anyway.
type CapabilitiesCache struct {
	disp  Dispatcher
	mu    sync.Mutex
	byDev map[string]dispatcher.Capabilities
}

// NewCapabilitiesCache constructs a CapabilitiesCache.
func NewCapabilitiesCache(disp Dispatcher) *CapabilitiesCache {
	return &CapabilitiesCache{disp: disp, byDev: make(map[string]dispatcher.Capabilities)}
}

// IndexFor implements CapabilitiesSource.
func (c *CapabilitiesCache) IndexFor(ctx context.Context, device string, row, col uint8) (uint16, bool, error) {
	caps, err := c.capabilities(ctx, device)
	if err != nil {
		return 0, false, err
	}
	for _, pos := range caps.Positions {
		if pos.Row == row && pos.Col == col {
			return pos.Index, true, nil
		}
	}
	return 0, false, nil
}

func (c *CapabilitiesCache) capabilities(ctx context.Context, device string) (dispatcher.Capabilities, error) {
	c.mu.Lock()
	caps, ok := c.byDev[device]
	c.mu.Unlock()
	if ok {
		return caps, nil
	}

	caps, err := c.disp.GetCapabilities(ctx, device)
	if err != nil {
		return dispatcher.Capabilities{}, err
	}

	c.mu.Lock()
	c.byDev[device] = caps
	c.mu.Unlock()
	return caps, nil
}
