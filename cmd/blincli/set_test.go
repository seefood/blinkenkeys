package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type setCall struct{ Method, Path, Query, Body string }

// setFakeDaemon records requests and answers per its fields.
type setFakeDaemon struct {
	mu        sync.Mutex
	calls     []setCall
	tabs      []uint16 // layout.tabs reported by GET /devices/d
	putStatus map[string]int
	delStatus map[string]int
}

func (f *setFakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	f.calls = append(f.calls, setCall{r.Method, r.URL.Path, r.URL.RawQuery, string(b)})
	switch {
	case r.Method == "GET" && r.URL.Path == "/devices":
		_, _ = w.Write([]byte(`[{"name":"d","connected":true}]`))
	case r.Method == "GET" && r.URL.Path == "/devices/d":
		_ = json.NewEncoder(w).Encode(map[string]any{"led_count": 12, "positions": []any{}, "layout": map[string]any{"tabs": f.tabs}})
	case r.Method == "PUT":
		if st := f.putStatus[r.URL.Path]; st != 0 {
			w.WriteHeader(st)
			_, _ = w.Write([]byte(`{"error":"no unclaimed keys available"}`))
			return
		}
		w.WriteHeader(204)
	case r.Method == "DELETE":
		if st := f.delStatus[r.URL.Path]; st != 0 {
			w.WriteHeader(st)
			_, _ = w.Write([]byte(`{"error":"gone"}`))
			return
		}
		w.WriteHeader(204)
	}
}

func (f *setFakeDaemon) writes() []setCall {
	var out []setCall
	for _, c := range f.calls {
		if c.Method == "PUT" || c.Method == "DELETE" {
			out = append(out, c)
		}
	}
	return out
}

func setDaemonApp(t *testing.T, f *setFakeDaemon, env map[string]string) (*app, *strings.Builder) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	if env == nil {
		env = map[string]string{}
	}
	env["BLINKENKEYS_URL"], env["BLINKENKEYS_TOKEN"] = srv.URL, "tok"
	a, _, errb := testApp(env)
	var sb strings.Builder
	a.stderr = &sb
	_ = errb
	return a, &sb
}

func setItermEnv() map[string]string {
	return map[string]string{"ITERM_SESSION_ID": "w0t1p0:ABCDEF0123456789"} // tab 2
}

func TestSetStateOnTabSlot(t *testing.T) {
	f := &setFakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}}
	a, errs := setDaemonApp(t, f, setItermEnv())
	if code := a.run([]string{"set", "-s", "claude/working"}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	w := f.writes()
	if len(w) != 1 || w[0].Path != "/devices/d/keys/idx:1" || !strings.Contains(w[0].Body, `"state":"claude/working"`) ||
		!strings.Contains(w[0].Body, `"owner":"testhost.iterm-ABCDEF01"`) {
		t.Errorf("writes = %+v", w)
	}
}

func TestSetTabSlotWrapsAndHonorsLayoutAndSlotsFlag(t *testing.T) {
	env := map[string]string{"ITERM_SESSION_ID": "w0t8p0:ABCDEF0123"} // tab 9
	f := &setFakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}}
	a, _ := setDaemonApp(t, f, env)
	a.run([]string{"set", "-c", "red"})
	if w := f.writes(); w[0].Path != "/devices/d/keys/idx:2" { // (9-1)%6 = 2
		t.Errorf("layout wrap: %+v", w)
	}
	f2 := &setFakeDaemon{}
	a2, _ := setDaemonApp(t, f2, map[string]string{"ITERM_SESSION_ID": "w0t8p0:ABCDEF0123"})
	a2.run([]string{"set", "-c", "red", "-m", "4"})
	if w := f2.writes(); w[0].Path != "/devices/d/keys/idx:0" { // (9-1)%4 = 0, no caps call needed
		t.Errorf("-m wrap: %+v", w)
	}
	for _, c := range f2.calls {
		if c.Path == "/devices/d" {
			t.Error("-m must skip the capabilities request")
		}
	}
}

func TestSetNoLayoutDefaultsToSixSlots(t *testing.T) {
	f := &setFakeDaemon{}
	a, _ := setDaemonApp(t, f, map[string]string{"ITERM_SESSION_ID": "w0t6p0:ABCDEF0123"}) // tab 7
	a.run([]string{"set", "-c", "red"})
	if w := f.writes(); w[0].Path != "/devices/d/keys/idx:0" {
		t.Errorf("default 6 slots: %+v", w)
	}
}

func TestSetNamedThenSharedOnConflict(t *testing.T) {
	f := &setFakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}, putStatus: map[string]int{"/devices/d/keys/testhost.claude-sess1": 409}}
	a, errs := setDaemonApp(t, f, map[string]string{"CLAUDE_CODE_SESSION_ID": "sess1"})
	if code := a.run([]string{"set", "-e", "breathe_blue"}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	w := f.writes()
	if len(w) != 2 || w[0].Path != "/devices/d/keys/testhost.claude-sess1" || !strings.HasPrefix(w[1].Path, "/devices/d/keys/idx:") ||
		!strings.Contains(w[1].Body, `"owner":"testhost.claude-sess1"`) {
		t.Errorf("writes = %+v", w)
	}
}

func TestSetExplicitKeyHasNoOwner(t *testing.T) {
	f := &setFakeDaemon{}
	a, _ := setDaemonApp(t, f, setItermEnv())
	a.run([]string{"set", "-k", "0,1", "-c", "#ff0000"})
	w := f.writes()
	if len(w) != 1 || w[0].Path != "/devices/d/keys/0%2C1" && w[0].Path != "/devices/d/keys/0,1" || strings.Contains(w[0].Body, "owner") {
		t.Errorf("writes = %+v", w)
	}
}

func TestSetNeedsExactlyOneAction(t *testing.T) {
	for _, args := range [][]string{{"set"}, {"set", "-c", "red", "-s", "a/b"}} {
		a, _ := setDaemonApp(t, &setFakeDaemon{}, setItermEnv())
		if code := a.run(args); code != exitUsage {
			t.Errorf("%v: code %d, want 64", args, code)
		}
	}
}

func TestSetNoKeyIsErrorUnlessIfDetected(t *testing.T) {
	a, errs := setDaemonApp(t, &setFakeDaemon{}, nil)
	if code := a.run([]string{"set", "-c", "red"}); code != exitUsage || !strings.Contains(errs.String(), "-k KEY") {
		t.Errorf("code %d, stderr %q", code, errs)
	}
	f := &setFakeDaemon{}
	a, errs = setDaemonApp(t, f, nil)
	if code := a.run([]string{"set", "-c", "red", "--if-detected"}); code != 0 || errs.Len() != 0 || len(f.calls) != 0 {
		t.Errorf("--if-detected: code %d, stderr %q, calls %+v (must send nothing)", code, errs, f.calls)
	}
}

func TestSetRemoteWithoutTokenFailsBeforeSending(t *testing.T) {
	a, _, errb := testApp(map[string]string{"BLINKENKEYS_URL": "http://127.0.0.1:1", "KITTY_WINDOW_ID": "1"})
	if code := a.run([]string{"set", "-c", "red"}); code != exitNoPerm || !strings.Contains(errb.String(), "token") {
		t.Errorf("code %d, stderr %q", code, errb)
	}
}

func TestClearConditionalOnOwner(t *testing.T) {
	f := &setFakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}}
	a, errs := setDaemonApp(t, f, setItermEnv())
	if code := a.run([]string{"clear"}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	w := f.writes()
	if len(w) != 1 || w[0].Method != "DELETE" || w[0].Path != "/devices/d/keys/idx:1" || w[0].Query != "owner=testhost.iterm-ABCDEF01" {
		t.Errorf("writes = %+v", w)
	}
	f2 := &setFakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}}
	a2, _ := setDaemonApp(t, f2, setItermEnv())
	a2.run([]string{"clear", "--force"})
	if w := f2.writes(); w[0].Query != "" {
		t.Errorf("--force must be unconditional: %+v", w)
	}
}

func TestClearNamedFallsBackToSharedKeyAndTreats404AsDone(t *testing.T) {
	f := &setFakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}, delStatus: map[string]int{"/devices/d/keys/testhost.claude-sess1": 404}}
	a, _ := setDaemonApp(t, f, map[string]string{"CLAUDE_CODE_SESSION_ID": "sess1"})
	if code := a.run([]string{"clear"}); code != 0 {
		t.Fatalf("code %d", code)
	}
	w := f.writes()
	if len(w) != 2 || !strings.HasPrefix(w[1].Path, "/devices/d/keys/idx:") || w[1].Query == "" {
		t.Errorf("writes = %+v", w)
	}
	// both 404 -> still success
	f3 := &setFakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}, delStatus: map[string]int{
		"/devices/d/keys/testhost.claude-sess1": 404, w[1].Path: 404}}
	a3, _ := setDaemonApp(t, f3, map[string]string{"CLAUDE_CODE_SESSION_ID": "sess1"})
	if code := a3.run([]string{"clear"}); code != 0 {
		t.Errorf("nothing to clear must exit 0, got %d", code)
	}
}

func TestExplicitNameThatIsAnAddressOrDotSegmentIsUsageError(t *testing.T) {
	for _, n := range []string{"led:3", "idx:2", ".", ".."} {
		for _, cmd := range [][]string{{"set", "-c", "red"}, {"clear"}, {"get"}} {
			f := &setFakeDaemon{}
			a, errs := setDaemonApp(t, f, nil)
			if code := a.run(append(cmd, "-n", n)); code != exitUsage || len(f.calls) != 0 {
				t.Errorf("%v -n %q: code %d, calls %+v, stderr %q", cmd, n, code, f.calls, errs)
			}
		}
	}
}

func TestClearIfDetectedWithNoKey(t *testing.T) {
	f := &setFakeDaemon{}
	a, _ := setDaemonApp(t, f, nil)
	if code := a.run([]string{"clear", "--if-detected"}); code != 0 || len(f.calls) != 0 {
		t.Errorf("code %d, calls %+v", code, f.calls)
	}
}

func TestSetBadFlagExits64(t *testing.T) {
	a, _ := setDaemonApp(t, &setFakeDaemon{}, setItermEnv())
	if code := a.run([]string{"set", "--bogus"}); code != exitUsage {
		t.Errorf("code %d, want 64", code)
	}
	if code := a.run([]string{"clear", "--bogus"}); code != exitUsage {
		t.Errorf("clear: code %d, want 64", code)
	}
}

func TestSetNeverPrintsToken(t *testing.T) {
	f := &setFakeDaemon{putStatus: map[string]int{"/devices/d/keys/idx:1": 500}}
	a, errs := setDaemonApp(t, f, setItermEnv())
	a.run([]string{"set", "-c", "red", "-m", "6", "-v"})
	if strings.Contains(errs.String(), "tok") {
		t.Errorf("token leaked: %q", errs)
	}
}
