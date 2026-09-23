package dispatcher

import (
	"sort"
	"sync"

	"github.com/seefood/blinkenkeys/internal/hid"
)

// Cache remembers the last color successfully written to each key of each
// device, since VialRGB Direct-mode colors live in the keyboard's RAM only
// and are lost on any reset/replug/brownout with no way to read them back
// (design spec's "State persistence & refresh"). Not persisted to disk — on
// a blinkenkeysd restart there's nothing more trustworthy to reload than an
// empty cache.
type Cache struct {
	mu       sync.Mutex
	byDevice map[string]map[uint16]hid.KeyColor
}

// NewCache creates an empty Cache.
func NewCache() *Cache {
	return &Cache{byDevice: make(map[string]map[uint16]hid.KeyColor)}
}

// Update records keys as the last color successfully written to device.
func (c *Cache) Update(device string, keys []hid.KeyColor) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.byDevice[device]
	if !ok {
		m = make(map[uint16]hid.KeyColor)
		c.byDevice[device] = m
	}
	for _, k := range keys {
		m[k.Index] = k
	}
}

// Snapshot returns every cached key/color for device, in ascending index
// order (so callers can batch contiguous runs the same way live SetKey
// traffic does), or nil if the device has never had a color set.
func (c *Cache) Snapshot(device string) []hid.KeyColor {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.byDevice[device]
	if !ok || len(m) == 0 {
		return nil
	}
	out := make([]hid.KeyColor, 0, len(m))
	for _, k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// Forget deletes device's cache entry entirely — called when Registry's
// Reconcile reports device as Evicted (untethered longer than
// UntetheredMaxAge), so a later device reconnecting under that identity
// starts with an empty cache rather than replaying stale colors.
func (c *Cache) Forget(device string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byDevice, device)
}
