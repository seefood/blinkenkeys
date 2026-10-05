package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

func TestPutDisplacedClaimIsReappliedOnItsNewKey(t *testing.T) {
	disp := knownPad()
	disp.moved = &dispatcher.Moved{Name: "x", From: 3, To: 4}
	from := effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.LED, N: 3}}
	to := effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.LED, N: 4}}
	w := &fakeWriter{status: map[effects.Target]effects.Status{
		from: {Origin: effects.Origin{Type: "color", Ref: "red", Owner: "s1"}},
	}}
	h := NewHandler(disp, w, &fakeLibrary{}, nil)
	if rec := put(t, h, "/devices/0/keys/led:3", `{"color":"blue"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; %s", rec.Code, rec.Body)
	}
	if len(w.colors) != 2 || w.colors[0] != from || w.colors[1] != to {
		t.Fatalf("writes = %+v, want incoming on 3 then displaced on 4", w.colors)
	}
	if w.origins[1] != (effects.Origin{Type: "color", Ref: "red", Owner: "s1"}) {
		t.Errorf("carried origin = %+v", w.origins[1])
	}
}

// F12 ruling: a failed carry replay is logged at Warn with the displaced
// claim's name, owner and both keys; no API change.
func TestPutFailedCarryReplayLogsWarnWithKeyAndOwner(t *testing.T) {
	disp := knownPad()
	disp.moved = &dispatcher.Moved{Name: "x", From: 3, To: 4}
	from := effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.LED, N: 3}}
	w := &fakeWriter{err: errors.New("queue full"), status: map[effects.Target]effects.Status{
		from: {Origin: effects.Origin{Type: "color", Ref: "red", Owner: "s1"}},
	}}
	var buf bytes.Buffer
	h := NewHandler(disp, w, &fakeLibrary{}, slog.New(slog.NewJSONHandler(&buf, nil)))
	put(t, h, "/devices/0/keys/led:3", `{"color":"blue"}`)
	var found bool
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if rec["msg"] != "displace: re-apply failed" {
			continue
		}
		found = true
		if rec["level"] != "WARN" || rec["name"] != "x" || rec["owner"] != "s1" || rec["from"] != "led:3" || rec["to"] != "led:4" || rec["err"] != "queue full" {
			t.Errorf("log record = %v, want WARN with name x, owner s1, from led:3, to led:4, err", rec)
		}
	}
	if !found {
		t.Errorf("no re-apply failure logged; log: %s", buf.String())
	}
}

func TestPutBadBodyDisplacesNothing(t *testing.T) {
	disp := knownPad()
	disp.moved = &dispatcher.Moved{Name: "x", From: 3, To: 4}
	placed := false
	disp.onPlace = func() { placed = true }
	h := NewHandler(disp, &fakeWriter{}, &fakeLibrary{}, nil)
	if rec := put(t, h, "/devices/0/keys/led:3", `{"color":"red","effect":"x"}`); rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if placed {
		t.Error("Place ran before the body was validated")
	}
}

func TestPutDisplacedWithNoStatusOrDroppedWritesOnlyTheIncoming(t *testing.T) {
	for _, m := range []*dispatcher.Moved{
		{Name: "x", From: 3, To: 4},         // moved, but nothing recorded to carry
		{Name: "x", From: 3, Dropped: true}, // pool full: handler must not touch To
	} {
		disp := knownPad()
		disp.moved = m
		w := &fakeWriter{}
		h := NewHandler(disp, w, &fakeLibrary{}, nil)
		if rec := put(t, h, "/devices/0/keys/led:3", `{"color":"blue"}`); rec.Code != http.StatusNoContent {
			t.Fatalf("%+v: status = %d", m, rec.Code)
		}
		if len(w.colors) != 1 {
			t.Errorf("%+v: writes = %+v, want only the incoming one", m, w.colors)
		}
	}
}

// Full pool with a recorded status on the key: a Dropped Moved must not be
// replayed (To is meaningless when Dropped, and would be LED 0).
func TestPutDroppedMovedWithStatusDoesNotReapply(t *testing.T) {
	disp := knownPad()
	disp.moved = &dispatcher.Moved{Name: "x", From: 3, Dropped: true}
	from := effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.LED, N: 3}}
	w := &fakeWriter{status: map[effects.Target]effects.Status{
		from: {Origin: effects.Origin{Type: "color", Ref: "red"}},
	}}
	h := NewHandler(disp, w, &fakeLibrary{}, nil)
	if rec := put(t, h, "/devices/0/keys/led:3", `{"color":"blue"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(w.colors) != 1 {
		t.Errorf("writes = %+v, want only the incoming one", w.colors)
	}
}
