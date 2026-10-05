package api

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// capsView is GET /devices/{name}: capabilities plus the key layout hint
// blincli uses to map tab numbers to keys.
type capsView struct {
	dispatcher.Capabilities
	Layout layoutView `json:"layout"`
}

// layoutView mirrors dispatcher.Layout. Pool is omitted for the default pool
// and [] for an explicitly empty one, so clients can tell the two apart.
type layoutView struct {
	Tabs      []uint16  `json:"tabs,omitempty"`
	Pool      *[]uint16 `json:"pool,omitempty"`
	Collision string    `json:"collision"` // "last-wins" or "displace"
}

func newLayoutView(l dispatcher.Layout) layoutView {
	v := layoutView{Tabs: l.Tabs, Collision: "last-wins"}
	if l.Displace {
		v.Collision = "displace"
	}
	if !l.DefaultPool {
		pool := append([]uint16{}, l.Pool...)
		v.Pool = &pool
	}
	return v
}

type colorView struct {
	H   uint8  `json:"h"`
	S   uint8  `json:"s"`
	V   uint8  `json:"v"`
	Hex string `json:"hex"`
}

type sourceView struct {
	Type  string    `json:"type"`
	Ref   string    `json:"ref"`
	Owner string    `json:"owner,omitempty"`
	SetAt time.Time `json:"set_at"`
	AgeMS int64     `json:"age_ms"`
}

type effectView struct {
	Name       string `json:"name"`
	Running    bool   `json:"running"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	DurationMS *int64 `json:"duration_ms"`
	Failed     bool   `json:"failed,omitempty"` // stopped after a write failed
}

type claimView struct {
	LastWrite time.Time `json:"last_write"`
	ExpiresAt time.Time `json:"expires_at"`
}

// keyView is one key's registration and state, as returned by the GET key
// routes. Color is the frame-buffer (desired) value, not a hardware read-back.
type keyView struct {
	Device    string      `json:"device"`
	Key       string      `json:"key"`
	Kind      string      `json:"kind"` // "name" or "direct"
	LED       uint16      `json:"led"`
	Row       *uint8      `json:"row,omitempty"`
	Col       *uint8      `json:"col,omitempty"`
	Connected bool        `json:"connected"`
	Color     *colorView  `json:"color,omitempty"`
	Source    *sourceView `json:"source,omitempty"`
	Effect    *effectView `json:"effect,omitempty"`
	Claim     *claimView  `json:"claim,omitempty"`
}

// buildView assembles led's view. label is how the caller addressed it; a
// name-claimed key is always labelled with its claimed name. Read-only.
func (h *Handler) buildView(device string, led uint16, label string, now time.Time) keyView {
	info := h.disp.KeyInfo(device, led)
	v := keyView{Device: device, Key: label, Kind: "direct", LED: led, Connected: h.disp.Connected(device)}
	if info.Name != "" {
		v.Kind, v.Key = "name", info.Name
		v.Claim = &claimView{LastWrite: info.LastWrite, ExpiresAt: info.LastWrite.Add(h.claimIdle)}
	}
	if info.HasPos {
		row, col := info.Row, info.Col
		v.Row, v.Col = &row, &col
	}
	if c, ok := h.disp.CurrentColor(device, led); ok {
		v.Color = &colorView{H: c.H, S: c.S, V: c.V, Hex: c.Hex()}
	}
	if st, ok := h.w.Status(effects.Target{Device: device, Addr: keyaddr.Address{Kind: keyaddr.LED, N: led}}, now); ok {
		v.Source = &sourceView{Type: st.Origin.Type, Ref: st.Origin.Ref, Owner: st.Origin.Owner, SetAt: st.SetAt, AgeMS: now.Sub(st.SetAt).Milliseconds()}
		if st.Effect != nil {
			ev := &effectView{Name: st.Effect.Name, Running: st.Effect.Running, ElapsedMS: st.Effect.Elapsed.Milliseconds(), Failed: st.Effect.Failed}
			if st.Effect.Finite {
				ms := st.Effect.Total.Milliseconds()
				ev.DurationMS = &ms
			}
			v.Effect = ev
		}
	}
	return v
}

func (h *Handler) getKey(w http.ResponseWriter, r *http.Request) {
	device, ok := h.disp.ResolveDevice(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("%w %q", dispatcher.ErrDeviceNotFound, r.PathValue("name")))
		return
	}
	addr, err := keyaddr.Parse(r.PathValue("pos"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// Read-only: Canonical would claim a name / mark a direct key as a side effect.
	led, err := h.disp.Lookup(r.Context(), device, addr)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	now := time.Now()
	view := h.buildView(device, led.N, addr.String(), now)
	if addr.Kind != keyaddr.Name && view.Source == nil && view.Kind == "direct" {
		if info := h.disp.KeyInfo(device, led.N); !info.Direct {
			writeError(w, http.StatusNotFound, fmt.Errorf("%w: %s is not registered", dispatcher.ErrKeyNotFound, addr))
			return
		}
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	device, ok := h.disp.ResolveDevice(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("%w %q", dispatcher.ErrDeviceNotFound, r.PathValue("name")))
		return
	}
	now := time.Now()
	// Owned LEDs plus LEDs with a status record: a claimed key can have no
	// record (blanked internally), and a record can outlive ownership.
	leds := map[uint16]bool{}
	for _, led := range h.disp.OwnedLEDs(device) {
		leds[led] = true
	}
	for t := range h.w.Statuses(device, now) {
		if t.Addr.Kind == keyaddr.LED {
			leds[t.Addr.N] = true
		}
	}
	views := []keyView{}
	for led := range leds {
		views = append(views, h.buildView(device, led, keyaddr.Address{Kind: keyaddr.LED, N: led}.String(), now))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].LED < views[j].LED })
	writeJSON(w, http.StatusOK, views)
}
