package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const keyJSON = `{"device":"d","key":"idx:1","kind":"direct","led":1,"row":0,"col":1,"connected":true,
 "color":{"h":21,"s":255,"v":255,"hex":"#ff8000"},
 "source":{"type":"state","ref":"claude/working","owner":"testhost.iterm-ABCDEF01","set_at":"2026-10-05T12:00:00Z","age_ms":12400},
 "effect":{"name":"breathe_orange","running":true,"elapsed_ms":12400,"duration_ms":null}}`

type getCall struct{ Method, Path string }

// getFake is a minimal daemon for the read-only commands.
type getFake struct {
	mu      sync.Mutex
	tabs    string // JSON array for layout.tabs
	keys    map[string]string
	keyList string
	calls   []getCall
}

func (f *getFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, getCall{r.Method, r.URL.Path})
	f.mu.Unlock()
	switch {
	case r.URL.Path == "/devices":
		_, _ = w.Write([]byte(`[{"name":"d","connected":true}]`))
	case r.URL.Path == "/devices/d":
		_, _ = w.Write([]byte(`{"led_count":3,"positions":[{"index":0,"row":0,"col":0},{"index":1,"row":0,"col":1},{"index":2,"row":255,"col":255}],"layout":{"tabs":` + f.tabs + `}}`))
	case r.Method == "GET" && f.keys[r.URL.Path] != "":
		_, _ = w.Write([]byte(f.keys[r.URL.Path]))
	case r.Method == "GET" && r.URL.Path == "/devices/d/keys":
		_, _ = w.Write([]byte(f.keyList))
	default:
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":"not registered"}`))
	}
}

func getApp(t *testing.T, f *getFake, env map[string]string) *app {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	e := map[string]string{"BLINKENKEYS_URL": srv.URL, "BLINKENKEYS_TOKEN": "sekrit"}
	for k, v := range env {
		e[k] = v
	}
	a, _, _ := testApp(e)
	return a
}

func itermTab2() map[string]string {
	return map[string]string{"ITERM_SESSION_ID": "w0t1p0:ABCDEF01-0000-0000-0000-000000000000"}
}

func TestGetRendersKey(t *testing.T) {
	f := &getFake{tabs: "[0,1,2,3,4,5]", keys: map[string]string{"/devices/d/keys/idx:1": keyJSON}}
	a := getApp(t, f, itermTab2())
	out := &strings.Builder{}
	a.stdout = out
	if code := a.run([]string{"get"}); code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"idx:1", "led:1", "claude/working", "breathe_orange", "#ff8000", "12.4s", "owner testhost.iterm-ABCDEF01", "loops"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "sekrit") {
		t.Error("token printed")
	}
}

func TestGetNotRegisteredExits66(t *testing.T) {
	f := &getFake{tabs: "[0,1,2,3,4,5]"}
	a := getApp(t, f, itermTab2())
	out := &strings.Builder{}
	a.stdout = out
	if code := a.run([]string{"get", "-q"}); code != exitNotFound || out.Len() != 0 {
		t.Errorf("code %d, stdout %q", code, out.String())
	}
}

func TestGetNamedFallsBackToSharedSlot(t *testing.T) {
	f := &getFake{tabs: "[0,1,2,3,4,5]"}
	a := getApp(t, f, map[string]string{"CLAUDE_CODE_SESSION_ID": "sess1"})
	a.run([]string{"get"})
	var gets []string
	for _, c := range f.calls {
		if c.Method != "GET" {
			t.Errorf("get issued %s %s", c.Method, c.Path)
		}
		if strings.Contains(c.Path, "/keys/") {
			gets = append(gets, c.Path)
		}
	}
	if len(gets) != 2 || !strings.Contains(gets[0], "claude-sess1") || !strings.Contains(gets[1], "/keys/idx:") {
		t.Errorf("GETs = %v", gets)
	}
}

// unixServer serves h on a Unix socket in a short temp dir (sun_path is ~104 bytes).
func unixServer(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "bk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: h}}
	srv.Start()
	t.Cleanup(srv.Close)
	return sock
}

// get must look up the same name set claimed: unqualified on a local
// socket, host-qualified on a remote endpoint.
func TestGetNamedMatchesSetOverLocalSocket(t *testing.T) {
	env := map[string]string{"CLAUDE_CODE_SESSION_ID": "sess1"}
	sf := &setFakeDaemon{}
	env["BLINKENKEYS_SOCKET"] = unixServer(t, sf)
	a, _, errb := testApp(env)
	if code := a.run([]string{"set", "-c", "red"}); code != 0 {
		t.Fatalf("set: code %d: %s", code, errb)
	}
	w := sf.writes()
	if len(w) != 1 || strings.Contains(w[0].Path, "testhost") {
		t.Fatalf("set writes = %+v", w)
	}
	gf := &getFake{tabs: "[0,1,2,3,4,5]", keys: map[string]string{w[0].Path: keyJSON}}
	env["BLINKENKEYS_SOCKET"] = unixServer(t, gf)
	a, out, errb := testApp(env)
	if code := a.run([]string{"get"}); code != 0 || !strings.Contains(out.String(), "idx:1") {
		t.Errorf("get after set (%s): code %d, stdout %q, stderr %q, calls %v", w[0].Path, code, out, errb, gf.calls)
	}

	rf := &getFake{tabs: "[0,1,2,3,4,5]"}
	b := getApp(t, rf, map[string]string{"CLAUDE_CODE_SESSION_ID": "sess1"})
	b.run([]string{"get"})
	if len(rf.calls) < 2 || rf.calls[1].Path != "/devices/d/keys/testhost.claude-sess1" {
		t.Errorf("remote get must host-qualify; calls %v", rf.calls)
	}
}

func TestGetExplicitKeyAndSlotsOverride(t *testing.T) {
	f := &getFake{tabs: "[]", keys: map[string]string{"/devices/d/keys/idx:1": keyJSON}}
	a := getApp(t, f, nil)
	out := &strings.Builder{}
	a.stdout = out
	if code := a.run([]string{"get", "-k", "idx:1"}); code != 0 || !strings.Contains(out.String(), "idx:1") {
		t.Errorf("-k: %d %s", code, out.String())
	}
	// no layout tabs, tab 9 wraps over -m 4 -> idx:(9-1)%4 = 0
	b := getApp(t, f, map[string]string{"ITERM_SESSION_ID": "w0t8p0:X"})
	b.run([]string{"get", "-m", "4"})
	last := f.calls[len(f.calls)-1]
	if last.Path != "/devices/d/keys/idx:0" {
		t.Errorf("last call %v", last)
	}
}

func TestGetBadFlagsExit64(t *testing.T) {
	for _, args := range [][]string{{"get", "--bogus"}, {"get", "-m"}, {"get", "-m", "-1"}, {"devices", "--bogus"}} {
		a := getApp(t, &getFake{tabs: "[]"}, itermTab2())
		if code := a.run(args); code != exitUsage {
			t.Errorf("%v: code %d", args, code)
		}
	}
}

func TestGetJSONAndAll(t *testing.T) {
	f := &getFake{tabs: "[0,1,2,3,4,5]", keys: map[string]string{"/devices/d/keys/idx:1": keyJSON}, keyList: "[" + keyJSON + "]"}
	a := getApp(t, f, itermTab2())
	out := &strings.Builder{}
	a.stdout = out
	if code := a.run([]string{"get", "--json"}); code != 0 || !strings.Contains(out.String(), `"hex": "#ff8000"`) {
		t.Errorf("--json: %d %s", code, out.String())
	}
	out.Reset()
	if code := a.run([]string{"get", "-a"}); code != 0 || !strings.Contains(out.String(), "idx:1") || !strings.Contains(out.String(), "claude/working") {
		t.Errorf("-a: %d %s", code, out.String())
	}
}

func TestDevicesCommand(t *testing.T) {
	f := &getFake{tabs: "[0,1,2]"}
	a := getApp(t, f, nil)
	out := &strings.Builder{}
	a.stdout = out
	if code := a.run([]string{"devices"}); code != 0 || !strings.Contains(out.String(), "d  connected") {
		t.Errorf("list: %d %s", code, out.String())
	}
	out.Reset()
	if code := a.run([]string{"devices", "d"}); code != 0 || !strings.Contains(out.String(), "tabs") || !strings.Contains(out.String(), "0,1,2") {
		t.Errorf("show: %d %s", code, out.String())
	}
}
