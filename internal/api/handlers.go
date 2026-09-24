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
	Canonical(device string, addr keyaddr.Address) (keyaddr.Address, error)
	ListDevices(ctx context.Context) ([]dispatcher.DeviceSummary, error)
	GetCapabilities(ctx context.Context, device string) (dispatcher.Capabilities, error)
}

// Writer is the effects engine: the only write path, so supersession is
// enforced in one place. *effects.Engine implements it.
type Writer interface {
	SetColor(t effects.Target, c color.HSV) error
	Start(t effects.Target, tl *effects.Timeline, now time.Time) error
}

// Library resolves effect and template-state requests. *effects.Library
// implements it.
type Library interface {
	Effect(name string) (*effects.Timeline, error)
	State(ref string) (effects.Action, error)
}

// Handler holds blinkenkeysd's HTTP dependencies and builds its route table.
type Handler struct {
	disp Dispatcher
	w    Writer
	lib  Library
}

// NewHandler constructs a Handler.
func NewHandler(disp Dispatcher, w Writer, lib Library) *Handler {
	return &Handler{disp: disp, w: w, lib: lib}
}

// Routes builds blinkenkeysd's route table (Go 1.22+ ServeMux method+wildcard
// patterns — no external router dependency needed).
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /devices/{name}/keys/{pos}", h.writeKey)
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
	if addr.Kind == keyaddr.Name {
		writeError(w, http.StatusNotImplemented, fmt.Errorf("api: named keys (%q) are not implemented yet; use R,C, led:N or idx:N", addr.Name))
		return
	}
	addr, err = h.disp.Canonical(device, addr)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	body, err := decodeWriteBody(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.apply(effects.Target{Device: device, Addr: addr}, body); err != nil {
		writeError(w, statusFor(err), err)
		return
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
	return b, nil
}

func (h *Handler) apply(t effects.Target, b writeBody) error {
	switch {
	case b.Color != nil:
		c, err := color.ParseHSV(*b.Color)
		if err != nil {
			return fmt.Errorf("%w: %v", errBadRequest, err)
		}
		return h.w.SetColor(t, c)
	case b.Effect != nil:
		tl, err := h.lib.Effect(*b.Effect)
		if err != nil {
			return err
		}
		return h.w.Start(t, tl, time.Now())
	default:
		act, err := h.lib.State(*b.State)
		if err != nil {
			return err
		}
		if act.Color != nil {
			return h.w.SetColor(t, *act.Color)
		}
		return h.w.Start(t, act.Timeline, time.Now())
	}
}

// statusFor maps typed errors to the Phase 3 spec's status table; anything
// untyped (a full queue, a HID error, a canceled context) is a 503.
func statusFor(err error) int {
	switch {
	case errors.Is(err, dispatcher.ErrDeviceNotFound),
		errors.Is(err, dispatcher.ErrKeyNotFound),
		errors.Is(err, effects.ErrUnknownEffect),
		errors.Is(err, effects.ErrUnknownState):
		return http.StatusNotFound
	case errors.Is(err, dispatcher.ErrNamedKeyUnsupported):
		return http.StatusNotImplemented
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
	writeJSON(w, http.StatusOK, caps)
}
