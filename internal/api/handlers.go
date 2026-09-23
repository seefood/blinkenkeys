// Package api implements blinkenkeysd's HTTP surface: the PUT/GET routes from
// the design spec, backed directly by an in-process internal/dispatcher.Dispatcher
// (no RPC layer — single binary, single process).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/dispatcher"
)

// Dispatcher is the subset of *dispatcher.Dispatcher the HTTP handlers need,
// so tests can substitute a fake without a real dispatcher goroutine.
type Dispatcher interface {
	SetKey(ctx context.Context, device string, index uint16, h, s, v uint8) error
	ListDevices(ctx context.Context) ([]dispatcher.DeviceSummary, error)
	GetCapabilities(ctx context.Context, device string) (dispatcher.Capabilities, error)
}

// CapabilitiesSource resolves row,col to a LED index for a named device.
// Returns (_, false, dispatcher.ErrDeviceNotFound) for an unknown device,
// (_, false, nil) for a known device with no key at that position, and a
// non-nil err for a genuine dispatch failure.
type CapabilitiesSource interface {
	IndexFor(ctx context.Context, device string, row, col uint8) (uint16, bool, error)
}

// Handler holds blinkenkeysd's HTTP dependencies and builds its route table.
type Handler struct {
	disp Dispatcher
	caps CapabilitiesSource
}

// NewHandler constructs a Handler.
func NewHandler(disp Dispatcher, caps CapabilitiesSource) *Handler {
	return &Handler{disp: disp, caps: caps}
}

// Routes builds blinkenkeysd's route table (Go 1.22+ ServeMux method+wildcard
// patterns — no external router dependency needed).
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /devices/{name}/keys/{pos}", h.setKey)
	mux.HandleFunc("GET /devices", h.listDevices)
	mux.HandleFunc("GET /devices/{name}", h.getCapabilities)
	return mux
}

type setKeyBody struct {
	Color string `json:"color"`
}

func (h *Handler) setKey(w http.ResponseWriter, r *http.Request) {
	device := r.PathValue("name")
	row, col, err := parsePos(r.PathValue("pos"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var body setKeyBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("api: invalid request body: %w", err))
		return
	}
	hue, sat, val, err := color.Parse(body.Color)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	index, found, err := h.caps.IndexFor(r.Context(), device, row, col)
	if errors.Is(err, dispatcher.ErrDeviceNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: device %q has no key at %d,%d", device, row, col))
		return
	}

	if err := h.disp.SetKey(r.Context(), device, index, hue, sat, val); err != nil {
		writeError(w, statusForDispatchErr(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parsePos(pos string) (row, col uint8, err error) {
	parts := strings.Split(pos, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("api: invalid key position %q: want row,col", pos)
	}
	r, err1 := strconv.ParseUint(parts[0], 10, 8)
	c, err2 := strconv.ParseUint(parts[1], 10, 8)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("api: invalid key position %q: want row,col", pos)
	}
	return uint8(r), uint8(c), nil
}

func statusForDispatchErr(err error) int {
	if errors.Is(err, dispatcher.ErrDeviceNotFound) {
		return http.StatusNotFound
	}
	return http.StatusServiceUnavailable
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
		writeError(w, statusForDispatchErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) getCapabilities(w http.ResponseWriter, r *http.Request) {
	device := r.PathValue("name")
	caps, err := h.disp.GetCapabilities(r.Context(), device)
	if err != nil {
		writeError(w, statusForDispatchErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, caps)
}
