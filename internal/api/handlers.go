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
	ListDevices(ctx context.Context) ([]dispatcher.DeviceSummary, error)
	GetCapabilities(ctx context.Context, device string) (dispatcher.Capabilities, error)
	ReleaseClaim(device, name string) error
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
	disp   Dispatcher
	w      Writer
	lib    Library
	logger *slog.Logger
}

// NewHandler constructs a Handler. logger may be nil, in which case
// best-effort warnings (e.g. a failed blank-on-release) are discarded.
func NewHandler(disp Dispatcher, w Writer, lib Library, logger *slog.Logger) *Handler {
	return &Handler{disp: disp, w: w, lib: lib, logger: logger}
}

// Routes builds blinkenkeysd's route table (Go 1.22+ ServeMux method+wildcard
// patterns — no external router dependency needed).
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /devices/{name}/keys/{pos}", h.writeKey)
	mux.HandleFunc("DELETE /devices/{name}/keys/{pos}", h.releaseKey)
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
	addr, err = h.disp.Canonical(r.Context(), device, addr)
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

// releaseKey releases a named key's claim, returning it to the device's
// unclaimed pool. Only a Name {pos} is accepted — a direct (R,C/led:/idx:)
// address has no name to release by.
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
	if addr.Kind != keyaddr.Name {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%w: release requires a key name, got %q", errBadRequest, addr))
		return
	}
	// Cancel any running effect and blank the key before freeing the claim
	// — otherwise an effect started under this name (e.g. timer5min) keeps
	// animating the LED indefinitely, since nothing else supersedes it once
	// the name is unclaimed. Must resolve the canonical address (and blank)
	// while the claim still exists: resolving after release would
	// auto-allocate a fresh claim under this same name instead.
	if canonical, cerr := h.disp.Canonical(r.Context(), device, addr); cerr == nil {
		if err := h.w.SetColor(effects.Target{Device: device, Addr: canonical}, color.HSV{}); err != nil && h.logger != nil {
			h.logger.Warn("blank-on-release failed", "device", device, "key", addr.Name, "err", err)
		}
	} else if h.logger != nil {
		h.logger.Warn("could not resolve key to blank on release", "device", device, "key", addr.Name, "err", cerr)
	}
	if err := h.disp.ReleaseClaim(device, addr.Name); err != nil {
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
	writeJSON(w, http.StatusOK, caps)
}
