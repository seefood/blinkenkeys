package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one blinkenkeysd endpoint.
type Client struct {
	ep   Endpoint
	base string
	hc   *http.Client
}

// New builds a Client for ep; a "unix" endpoint dials its socket for every request.
func New(ep Endpoint) *Client {
	tr := &http.Transport{}
	base := strings.TrimRight(ep.BaseURL, "/")
	if ep.Kind == "unix" {
		sock := ep.Socket
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}
		base = "http://blinkenkeys"
	}
	return &Client{ep: ep, base: base, hc: &http.Client{Transport: tr, Timeout: 5 * time.Second}}
}

// APIError is a non-2xx response from the daemon.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("blinkenkeysd: %d %s", e.Status, e.Message) }

// Is makes 401/403 match ErrAuth.
func (e *APIError) Is(target error) bool {
	return target == ErrAuth && (e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden)
}

func hasStatus(err error, code int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == code
}

// IsNotFound reports a 404 from the daemon.
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsConflict reports a 409 (e.g. no unclaimed keys left in the pool).
func IsConflict(err error) bool { return hasStatus(err, http.StatusConflict) }

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.ep.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.ep.Token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func keyPath(device, key string) string {
	return "/devices/" + url.PathEscape(device) + "/keys/" + url.PathEscape(key)
}

// PutBody is a write: exactly one of Color/Effect/State; Owner is optional.
type PutBody struct {
	Color  string `json:"color,omitempty"`
	Effect string `json:"effect,omitempty"`
	State  string `json:"state,omitempty"`
	Owner  string `json:"owner,omitempty"`
}

// DeviceSummary is one GET /devices entry.
type DeviceSummary struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}

// Position is one LED's matrix location.
type Position struct {
	Index uint16 `json:"index"`
	Row   uint8  `json:"row"`
	Col   uint8  `json:"col"`
}

// Capabilities is GET /devices/{name}.
type Capabilities struct {
	LEDCount  int        `json:"led_count"`
	Positions []Position `json:"positions"`
	Layout    struct {
		Tabs []uint16 `json:"tabs"`
	} `json:"layout"`
}

// KeyStatus mirrors the daemon's GET key response.
type KeyStatus struct {
	Device    string `json:"device"`
	Key       string `json:"key"`
	Kind      string `json:"kind"`
	LED       uint16 `json:"led"`
	Row       *uint8 `json:"row,omitempty"`
	Col       *uint8 `json:"col,omitempty"`
	Connected bool   `json:"connected"`
	Color     *struct {
		H   uint8  `json:"h"`
		S   uint8  `json:"s"`
		V   uint8  `json:"v"`
		Hex string `json:"hex"`
	} `json:"color,omitempty"`
	Source *struct {
		Type  string    `json:"type"`
		Ref   string    `json:"ref"`
		Owner string    `json:"owner,omitempty"`
		SetAt time.Time `json:"set_at"`
		AgeMS int64     `json:"age_ms"`
	} `json:"source,omitempty"`
	Effect *struct {
		Name       string `json:"name"`
		Running    bool   `json:"running"`
		ElapsedMS  int64  `json:"elapsed_ms"`
		DurationMS *int64 `json:"duration_ms"`
	} `json:"effect,omitempty"`
	Claim *struct {
		LastWrite time.Time `json:"last_write"`
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"claim,omitempty"`
}

// Devices lists devices.
func (c *Client) Devices(ctx context.Context) ([]DeviceSummary, error) {
	var out []DeviceSummary
	return out, c.do(ctx, http.MethodGet, "/devices", nil, &out)
}

// Capabilities fetches a device's LED layout and key layout hint.
func (c *Client) Capabilities(ctx context.Context, device string) (Capabilities, error) {
	var out Capabilities
	return out, c.do(ctx, http.MethodGet, "/devices/"+url.PathEscape(device), nil, &out)
}

// Put writes a color, effect or state to key.
func (c *Client) Put(ctx context.Context, device, key string, b PutBody) error {
	return c.do(ctx, http.MethodPut, keyPath(device, key), b, nil)
}

// Delete blanks key and releases its claim; owner != "" makes it conditional.
func (c *Client) Delete(ctx context.Context, device, key, owner string) error {
	p := keyPath(device, key)
	if owner != "" {
		p += "?" + url.Values{"owner": {owner}}.Encode()
	}
	return c.do(ctx, http.MethodDelete, p, nil, nil)
}

// GetKey reads one key's registration and state.
func (c *Client) GetKey(ctx context.Context, device, key string) (KeyStatus, error) {
	var out KeyStatus
	return out, c.do(ctx, http.MethodGet, keyPath(device, key), nil, &out)
}

// ListKeys reads every registered key on device.
func (c *Client) ListKeys(ctx context.Context, device string) ([]KeyStatus, error) {
	var out []KeyStatus
	return out, c.do(ctx, http.MethodGet, "/devices/"+url.PathEscape(device)+"/keys", nil, &out)
}
