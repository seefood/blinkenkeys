package dispatcher

import (
	"context"
	"fmt"

	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// Moved reports what a direct write did to a named claim sitting on its key.
// The zero value means no claim was there.
type Moved struct {
	Name     string
	From, To uint16 // LED indexes; To is valid only when !Dropped
	Dropped  bool   // no free pool key, so the claim was released instead of moved
}

// MarkDirectDisplacing is MarkDirect with the device's collision policy: if
// idx holds a named claim and the layout has Displace, the claim moves to the
// next unclaimed pool key and idx becomes direct-owned. With no free key (or
// with last-wins) the claim is released, as MarkDirect does. The release and
// the move happen under one lock hold, so a claim is never released without
// the new state being fully recorded.
func (r *Registry) MarkDirectDisplacing(device string, idx uint16) Moved {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.slots[device]
	if !ok {
		return Moved{}
	}
	owner, held := s.owners[idx]
	if !held || owner.Name == "" {
		markDirectLocked(s, idx)
		return Moved{}
	}
	l := r.layoutLocked(device)
	if l.Displace && s.caps != nil {
		if to, ok := nextUnclaimedLocked(s, l); ok {
			c := s.claims[owner.Name]
			c.index = to
			s.claims[owner.Name] = c
			s.owners[to] = keyOwner{Name: owner.Name}
			s.owners[idx] = keyOwner{Direct: true}
			return Moved{Name: owner.Name, From: idx, To: to}
		}
	}
	markDirectLocked(s, idx)
	return Moved{Name: owner.Name, From: idx, Dropped: true}
}

// Place is Canonical with the collision policy applied: identical for a name
// or an unclaimed/direct key; for a direct address onto a named claim it calls
// MarkDirectDisplacing and returns the Moved only if the claim moved. The
// caller must re-apply the displaced key's last request on Moved.To (the
// dispatcher cannot: effects live in effects.Engine).
func (d *Dispatcher) Place(ctx context.Context, device string, addr keyaddr.Address) (keyaddr.Address, *Moved, error) {
	if addr.Kind == keyaddr.Name {
		a, err := d.Canonical(ctx, device, addr)
		return a, nil, err
	}
	caps, err := d.GetCapabilities(ctx, device)
	if err != nil {
		return keyaddr.Address{}, nil, err
	}
	idx, ok := keyaddr.Resolve(addr, caps.LEDCount, caps.Positions)
	if !ok {
		return keyaddr.Address{}, nil, fmt.Errorf("%w: %s", ErrKeyNotFound, addr)
	}
	m := d.registry.MarkDirectDisplacing(device, idx)
	led := keyaddr.Address{Kind: keyaddr.LED, N: idx}
	switch {
	case m.Name == "":
		return led, nil, nil
	case m.Dropped:
		if d.registry.Layout(device).Displace {
			d.logger.Warn("displace: pool full, named claim released", "device", device, "name", m.Name, "led", idx)
		}
		return led, nil, nil
	}
	d.logger.Info("displace: named claim moved", "device", device, "name", m.Name, "from", m.From, "to", m.To)
	return led, &m, nil
}
