package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDevicesAndListKeysDecode(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/devices" {
			_, _ = w.Write([]byte(`[{"name":"d1","connected":true},{"name":"d2","connected":false}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"device":"d","key":"idx:0"},{"device":"d","key":"idx:1"}]`))
	})
	devs, err := c.Devices(context.Background())
	if err != nil || len(devs) != 2 || devs[0].Name != "d1" || !devs[0].Connected {
		t.Errorf("devices = %+v, %v", devs, err)
	}
	keys, err := c.ListKeys(context.Background(), "d")
	if err != nil || len(keys) != 2 || keys[1].Key != "idx:1" {
		t.Errorf("keys = %+v, %v", keys, err)
	}
}

func TestClientFormattingNeverShowsToken(t *testing.T) {
	const secret = "s3cr3t-sentinel"
	c := New(Endpoint{Kind: "http", BaseURL: "http://h", Token: secret})
	for _, v := range []any{c, *c} {
		for _, f := range []string{"%v", "%+v", "%#v", "%s"} {
			if got := fmt.Sprintf(f, v); strings.Contains(got, secret) {
				t.Errorf("%s of %T leaks token: %s", f, v, got)
			}
		}
	}
}

func TestUnreachableKeepsCause(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.Put(ctx, "d", "k", PutBody{Color: "red"})
	if !errors.Is(err, ErrUnreachable) || !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want ErrUnreachable and context.Canceled", err)
	}
}

func TestNonJSONErrorBodyFallsBackToStatusText(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>oops</html>"))
	})
	err := c.Put(context.Background(), "d", "k", PutBody{Color: "red"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Message != "Bad Gateway" {
		t.Errorf("err = %v", err)
	}
}

func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Endpoint{Kind: "http", BaseURL: srv.URL, Token: "tok"})
}

func TestPutSendsBodyAuthAndEscapedKey(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotBody map[string]string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotCT = r.URL.EscapedPath(), r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.Put(context.Background(), "uid-1", "idx:2", PutBody{State: "claude/idle", Owner: "me"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok" || gotCT != "application/json" || gotPath != "/devices/uid-1/keys/idx:2" {
		t.Errorf("path %q auth %q ct %q", gotPath, gotAuth, gotCT)
	}
	if len(gotBody) != 2 || gotBody["state"] != "claude/idle" || gotBody["owner"] != "me" {
		t.Errorf("body = %v (empty fields must be omitted)", gotBody)
	}
}

func TestDeleteOwnerQuery(t *testing.T) {
	var gotQuery string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) { gotQuery = r.URL.RawQuery; w.WriteHeader(204) })
	_ = c.Delete(context.Background(), "d", "esc", "a b")
	if gotQuery != "owner=a+b" {
		t.Errorf("query = %q", gotQuery)
	}
	_ = c.Delete(context.Background(), "d", "esc", "")
	if gotQuery != "" {
		t.Errorf("unconditional delete sent query %q", gotQuery)
	}
}

func TestErrorClassification(t *testing.T) {
	status := 0
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	})
	for code, check := range map[int]func(error) bool{
		401: func(e error) bool { return errors.Is(e, ErrAuth) },
		403: func(e error) bool { return errors.Is(e, ErrAuth) },
		404: IsNotFound,
		409: IsConflict,
	} {
		status = code
		err := c.Put(context.Background(), "d", "k", PutBody{Color: "red"})
		var ae *APIError
		if !check(err) || !errors.As(err, &ae) || ae.Message != "nope" || ae.Status != code {
			t.Errorf("status %d: err = %v", code, err)
		}
	}
	status = 503
	if err := c.Put(context.Background(), "d", "k", PutBody{Color: "red"}); err == nil || errors.Is(err, ErrAuth) || IsNotFound(err) {
		t.Errorf("503: err = %v", err)
	}
}

func TestUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	err := New(Endpoint{Kind: "http", BaseURL: url, Token: "t"}).Put(context.Background(), "d", "k", PutBody{Color: "red"})
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}

func TestGetKeyAndCapabilitiesDecode(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/devices/d" {
			_, _ = w.Write([]byte(`{"led_count":12,"positions":[{"index":0,"row":0,"col":0}],"layout":{"tabs":[0,1,2]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"device":"d","key":"idx:0","kind":"direct","led":0,"row":0,"col":0,"connected":true,
		  "color":{"h":1,"s":2,"v":3,"hex":"#010203"},
		  "source":{"type":"state","ref":"claude/idle","owner":"o","set_at":"2026-10-05T12:00:00Z","age_ms":5},
		  "effect":{"name":"timer5min","running":true,"elapsed_ms":7,"duration_ms":null}}`))
	})
	caps, err := c.Capabilities(context.Background(), "d")
	if err != nil || caps.LEDCount != 12 || len(caps.Layout.Tabs) != 3 {
		t.Errorf("caps = %+v, %v", caps, err)
	}
	k, err := c.GetKey(context.Background(), "d", "idx:0")
	if err != nil || k.Source == nil || k.Source.Ref != "claude/idle" || k.Effect == nil || k.Effect.DurationMS != nil || k.Color.Hex != "#010203" {
		t.Errorf("key = %+v, %v", k, err)
	}
}
