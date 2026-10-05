// Package api implements blinkenkeysd's HTTP surface: the PUT/GET routes from
// the design specs, backed directly by an in-process dispatcher and effects
// engine (no RPC layer — single binary, single process).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// Dispatcher is the subset of *dispatcher.Dispatcher the handlers need.
type Dispatcher interface {
	ResolveDevice(ref string) (string, bool)
	Canonical(ctx context.Context, device string, addr keyaddr.Address) (keyaddr.Address, error)
	Place(ctx context.Context, device string, addr keyaddr.Address) (keyaddr.Address, *dispatcher.Moved, error)
	ListDevices(ctx context.Context) ([]dispatcher.DeviceSummary, error)
	GetCapabilities(ctx context.Context, device string) (dispatcher.Capabilities, error)
	ReleaseClaim(device, name string) error
	Lookup(ctx context.Context, device string, addr keyaddr.Address) (keyaddr.Address, error)
	KeyInfo(device string, led uint16) dispatcher.KeyInfo
	CurrentColor(device string, led uint16) (color.HSV, bool)
	Layout(device string) dispatcher.Layout
	Connected(device string) bool
}

// Writer is the effects engine: the only write path, so supersession is
// enforced in one place. *effects.Engine implements it.
type Writer interface {
	SetColor(t effects.Target, c color.HSV) error
	ClearIfOwner(t effects.Target, owner string) (bool, error)
	SetColorFrom(t effects.Target, c color.HSV, o effects.Origin, now time.Time) error
	StartFrom(t effects.Target, tl *effects.Timeline, now time.Time, o effects.Origin) error
	Status(t effects.Target, now time.Time) (effects.Status, bool)
	Statuses(device string, now time.Time) map[effects.Target]effects.Status
}

// Library resolves effect and template-state requests. *effects.Library
// implements it.
type Library interface {
	Effect(name string) (*effects.Timeline, error)
	State(ref string) (effects.Action, error)
}

// Handler holds blinkenkeysd's HTTP dependencies and builds its route table.
type Handler struct {
	disp   Dispatcher
	w      Writer
	lib    Library
	logger *slog.Logger

	claimIdle time.Duration
}

// NewHandler constructs a Handler. logger may be nil, in which case
// best-effort warnings (e.g. a failed blank-on-release) are discarded.
func NewHandler(disp Dispatcher, w Writer, lib Library, logger *slog.Logger) *Handler {
	return &Handler{disp: disp, w: w, lib: lib, logger: logger, claimIdle: dispatcher.DefaultClaimIdleTimeout}
}

// SetClaimIdleTimeout sets the claim idle timeout used to report a named
// claim's expiry; it should match config's claims.idle_timeout.
func (h *Handler) SetClaimIdleTimeout(d time.Duration) { h.claimIdle = d }

// Routes builds blinkenkeysd's route table (Go 1.22+ ServeMux method+wildcard
// patterns — no external router dependency needed).
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /devices/{name}/keys/{pos}", h.writeKey)
	mux.HandleFunc("DELETE /devices/{name}/keys/{pos}", h.releaseKey)
	mux.HandleFunc("GET /devices/{name}/keys", h.listKeys)
	mux.HandleFunc("GET /devices/{name}/keys/{pos}", h.getKey)
	mux.HandleFunc("GET /devices", h.listDevices)
	mux.HandleFunc("GET /devices/{name}", h.getCapabilities)
	return mux
}

// errBadRequest marks request-content errors that aren't already typed by
// the package that detected them.
var errBadRequest = errors.New("api: bad request")

// writeBody is the PUT body: exactly one of Color, Effect, State.
type writeBody struct {
	Color  *string `json:"color"`
	Effect *string `json:"effect"`
	State  *string `json:"state"`
	Owner  *string `json:"owner"` // optional tag recorded with the write; see releaseKey
}

func (h *Handler) writeKey(w http.ResponseWriter, r *http.Request) {
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
	// The body is fully validated (decode, color parse, effect/state lookup)
	// before Place: a bad request must not displace anything.
	body, err := decodeWriteBody(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	run, err := h.prepare(body)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	addr, moved, err := h.disp.Place(r.Context(), device, addr)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	now := time.Now()
	target := effects.Target{Device: device, Addr: addr}
	// Capture the displaced key's last request before the incoming write
	// replaces its status record.
	// The status read here and the replay below are not atomic with Place:
	// a concurrent write to the key in between can be replayed stale. Best
	// effort by design.
	var carry func(effects.Target, time.Time) error
	if moved != nil && !moved.Dropped {
		if st, ok := h.w.Status(target, now); ok {
			if b, ok := bodyOf(st.Origin); ok {
				if c, err := h.prepare(b); err == nil {
					carry = c
				}
			}
		}
	}
	writeErr := run(target, now)
	// Replay even when the incoming write failed after Place (queue full,
	// HID error): the claim has already moved, so it must not be left dark.
	if carry != nil {
		to := effects.Target{Device: device, Addr: keyaddr.Address{Kind: keyaddr.LED, N: moved.To}}
		// Best effort: a failure to restore the displaced key must not fail
		// the incoming write.
		if err := carry(to, now); err != nil && h.logger != nil {
			h.logger.Warn("displace: re-apply failed", "device", device, "name", moved.Name, "err", err)
		}
	}
	if writeErr != nil {
		writeError(w, statusFor(writeErr), writeErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// bodyOf rebuilds the request that produced o, so a displaced key's color,
// effect or state can be replayed on its new key. ok is false for an origin
// with no replayable source. Replaying an effect restarts its timeline at the
// replay time; it does not resume the phase it had on the old key.
func bodyOf(o effects.Origin) (writeBody, bool) {
	var b writeBody
	switch o.Type {
	case "color":
		b.Color = &o.Ref
	case "effect":
		b.Effect = &o.Ref
	case "state":
		b.State = &o.Ref
	default:
		return writeBody{}, false
	}
	if o.Owner != "" {
		b.Owner = &o.Owner
	}
	return b, true
}

// releaseKey blanks a key (cancelling any running effect) and, if {pos} is a
// name, frees its claim. Any {pos} form is accepted: a direct (R,C/led:/idx:)
// key has no claim, so it is only blanked. With ?owner=TAG the call is a
// no-op (204) whenever a status record exists whose Owner differs (including
// an empty Owner), so a session ending never blanks a key another session or
// an untagged write has since taken over; it proceeds only when there is no
// status record at all, or no ?owner=. If the blank fails, the claim is kept
// and the error is returned.
//
// The key is resolved read-only (Lookup): resolving a name through Canonical
// would claim it first, then blank and release the claim it just made.
func (h *Handler) releaseKey(w http.ResponseWriter, r *http.Request) {
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
	led, err := h.disp.Lookup(r.Context(), device, addr)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	target := effects.Target{Device: device, Addr: led}
	// Blank before releasing: a still-running effect (e.g. timer5min) would
	// otherwise keep animating an LED nothing owns anymore.
	// If the blank fails, keep the claim: freeing it while the LED may still
	// hold its color/effect would leave an orphaned, unowned key.
	// With ?owner= the owner check and the blank are one engine call, so a
	// racing write by another owner is never blanked.
	if owner := r.URL.Query().Get("owner"); owner != "" {
		var cleared bool
		if cleared, err = h.w.ClearIfOwner(target, owner); err == nil && !cleared {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	} else {
		err = h.w.SetColor(target, color.HSV{})
	}
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("blank-on-release failed", "device", device, "key", addr.String(), "err", err)
		}
		writeError(w, statusFor(err), err)
		return
	}
	if addr.Kind == keyaddr.Name {
		if err := h.disp.ReleaseClaim(device, addr.Name); err != nil {
			writeError(w, statusFor(err), err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeWriteBody(r io.Reader) (writeBody, error) {
	var b writeBody
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return b, fmt.Errorf("%w: invalid request body: %v", errBadRequest, err)
	}
	n := 0
	for _, p := range []*string{b.Color, b.Effect, b.State} {
		if p != nil {
			n++
		}
	}
	if n != 1 {
		return b, fmt.Errorf("%w: body needs exactly one of color, effect, state", errBadRequest)
	}
	if b.Owner != nil && (len(*b.Owner) == 0 || len(*b.Owner) > 128) {
		return b, fmt.Errorf("%w: owner must be 1-128 bytes", errBadRequest)
	}
	return b, nil
}

func (h *Handler) apply(t effects.Target, b writeBody, now time.Time) error {
	run, err := h.prepare(b)
	if err != nil {
		return err
	}
	return run(t, now)
}

// prepare does all of b's semantic validation (color parse, effect/state
// lookup) and returns the write to run, so writeKey can reject a bad request
// before Place displaces anything. Only write failures remain in run.
func (h *Handler) prepare(b writeBody) (func(effects.Target, time.Time) error, error) {
	owner := ""
	if b.Owner != nil {
		owner = *b.Owner
	}
	switch {
	case b.Color != nil:
		c, err := color.ParseHSV(*b.Color)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errBadRequest, err)
		}
		o := effects.Origin{Type: "color", Ref: *b.Color, Owner: owner}
		return func(t effects.Target, now time.Time) error { return h.w.SetColorFrom(t, c, o, now) }, nil
	case b.Effect != nil:
		tl, err := h.lib.Effect(*b.Effect)
		if err != nil {
			return nil, err
		}
		o := effects.Origin{Type: "effect", Ref: *b.Effect, Owner: owner}
		return func(t effects.Target, now time.Time) error { return h.w.StartFrom(t, tl, now, o) }, nil
	default:
		act, err := h.lib.State(*b.State)
		if err != nil {
			return nil, err
		}
		o := effects.Origin{Type: "state", Ref: *b.State, Owner: owner}
		if act.Color != nil {
			return func(t effects.Target, now time.Time) error { return h.w.SetColorFrom(t, *act.Color, o, now) }, nil
		}
		return func(t effects.Target, now time.Time) error { return h.w.StartFrom(t, act.Timeline, now, o) }, nil
	}
}

// statusFor maps typed errors to the Phase 3 spec's status table; anything
// untyped (a full queue, a HID error, a canceled context) is a 503.
func statusFor(err error) int {
	switch {
	case errors.Is(err, dispatcher.ErrDeviceNotFound),
		errors.Is(err, dispatcher.ErrKeyNotFound),
		errors.Is(err, dispatcher.ErrClaimNotFound),
		errors.Is(err, effects.ErrUnknownEffect),
		errors.Is(err, effects.ErrUnknownState):
		return http.StatusNotFound
	case errors.Is(err, dispatcher.ErrNoUnclaimedKeys):
		return http.StatusConflict
	case errors.Is(err, errBadRequest),
		errors.Is(err, keyaddr.ErrInvalid):
		return http.StatusBadRequest
	default:
		return http.StatusServiceUnavailable
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) {
	result, err := h.disp.ListDevices(r.Context())
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) getCapabilities(w http.ResponseWriter, r *http.Request) {
	device, ok := h.disp.ResolveDevice(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("%w %q", dispatcher.ErrDeviceNotFound, r.PathValue("name")))
		return
	}
	caps, err := h.disp.GetCapabilities(r.Context(), device)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	view := capsView{Capabilities: caps, Layout: newLayoutView(h.disp.Layout(device))}
	writeJSON(w, http.StatusOK, view)
}
