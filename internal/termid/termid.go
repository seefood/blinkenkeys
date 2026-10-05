// Package termid works out which terminal tab/pane the process is running in,
// from environment variables and, where needed, a short-lived helper command
// (tmux, wezterm cli). It is pure Go with no dependency on the daemon.
package termid

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Identity is what the environment says about the calling terminal.
type Identity struct {
	Terminal   string // "tmux", "iterm", "wezterm", "kitty"; "" if none recognized
	Tab        int    // 1-based tab number in its window; 0 if unknown
	InstanceID string // short id of this pane/window; "" if none
}

// Name is the identity as a key name, e.g. "wezterm-17".
func (i Identity) Name() string {
	switch {
	case i.Terminal == "":
		return ""
	case i.InstanceID == "":
		return i.Terminal
	default:
		return i.Terminal + "-" + i.InstanceID
	}
}

// Env is the process environment and helper execution, injectable for tests.
type Env struct {
	Getenv func(string) string
	Run    func(ctx context.Context, name string, args ...string) (string, error)
}

// helperTimeout bounds all helper commands of one Detect call together: a
// hook must never hang on a missing tmux server or a stuck wezterm mux.
const helperTimeout = 500 * time.Millisecond

// ExecRun is the real Env.Run.
func ExecRun(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output() // #nosec G204 -- name/args are literals chosen by this package's resolvers
	return string(out), err
}

// Detect identifies the calling terminal, most specific first: tmux (whose
// panes share the outer terminal's env), then iTerm2, WezTerm, kitty.
func Detect(ctx context.Context, env Env) Identity {
	ctx, cancel := context.WithTimeout(ctx, helperTimeout)
	defer cancel()
	for _, d := range []func(context.Context, Env) (Identity, bool){detectTmux, detectITerm, detectWezTerm, detectKitty} {
		if id, ok := d(ctx, env); ok {
			return id
		}
	}
	return Identity{}
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Sanitize makes s safe as a key name: names must not contain "," (it would
// parse as row,col) or "/" (a single URL path segment).
func Sanitize(s string) string { return unsafeName.ReplaceAllString(s, "-") }

func detectTmux(ctx context.Context, env Env) (Identity, bool) {
	pane := env.Getenv("TMUX_PANE")
	if pane == "" {
		return Identity{}, false
	}
	id := Identity{Terminal: "tmux", InstanceID: Sanitize(strings.TrimPrefix(pane, "%"))}
	idx, err1 := env.Run(ctx, "tmux", "display-message", "-p", "-t", pane, "#{window_index}")
	base, err2 := env.Run(ctx, "tmux", "show-options", "-gv", "base-index")
	if err1 == nil && err2 == nil {
		w, e1 := strconv.Atoi(strings.TrimSpace(idx))
		b, e2 := strconv.Atoi(strings.TrimSpace(base))
		if e1 == nil && e2 == nil && w-b+1 >= 1 {
			id.Tab = w - b + 1
		}
	}
	return id, true
}

// itermRE matches ITERM_SESSION_ID: w<window>t<tab>p<pane>[:<UUID>]. iTerm
// numbers tabs from 0; the variable is fixed when the shell starts, so it
// goes stale if tabs are later reordered or closed.
var itermRE = regexp.MustCompile(`^w(\d+)t(\d+)p(\d+)(?::(.+))?$`)

func detectITerm(_ context.Context, env Env) (Identity, bool) {
	raw := env.Getenv("ITERM_SESSION_ID")
	if raw == "" {
		return Identity{}, false
	}
	id := Identity{Terminal: "iterm", InstanceID: Sanitize(raw)}
	m := itermRE.FindStringSubmatch(raw)
	if m == nil {
		return id, true
	}
	t, _ := strconv.Atoi(m[2])
	id.Tab = t + 1
	if m[4] != "" {
		uuid := m[4]
		if len(uuid) > 8 {
			uuid = uuid[:8]
		}
		id.InstanceID = Sanitize(uuid)
	} else {
		id.InstanceID = Sanitize(m[0])
	}
	return id, true
}

type weztermPane struct {
	WindowID int `json:"window_id"`
	TabID    int `json:"tab_id"`
	PaneID   int `json:"pane_id"`
}

func detectWezTerm(ctx context.Context, env Env) (Identity, bool) {
	pane := env.Getenv("WEZTERM_PANE")
	if pane == "" {
		return Identity{}, false
	}
	id := Identity{Terminal: "wezterm", InstanceID: Sanitize(pane)}
	if out, err := env.Run(ctx, "wezterm", "cli", "list", "--format", "json"); err == nil {
		id.Tab = weztermTab(out, pane)
	}
	return id, true
}

// weztermTab ranks pane's tab among the distinct tab_ids of its window, in
// `wezterm cli list` order. WezTerm exposes no tab-position field, so this
// relies on the list being in tab order (checked manually; see
// docs/superpowers/manual-checks/blincli.md). 0 if pane isn't found.
func weztermTab(listJSON, pane string) int {
	want, err := strconv.Atoi(pane)
	if err != nil {
		return 0
	}
	var panes []weztermPane
	if json.Unmarshal([]byte(listJSON), &panes) != nil {
		return 0
	}
	win, tab, found := 0, 0, false
	for _, p := range panes {
		if p.PaneID == want {
			win, tab, found = p.WindowID, p.TabID, true
			break
		}
	}
	if !found {
		return 0
	}
	var seen []int
	for _, p := range panes {
		if p.WindowID == win && !slices.Contains(seen, p.TabID) {
			seen = append(seen, p.TabID)
		}
	}
	return slices.Index(seen, tab) + 1
}

// detectKitty reports kitty by window id only: KITTY_WINDOW_ID is not a tab
// number, and tab info would need remote control enabled.
func detectKitty(_ context.Context, env Env) (Identity, bool) {
	w := env.Getenv("KITTY_WINDOW_ID")
	if w == "" {
		return Identity{}, false
	}
	return Identity{Terminal: "kitty", InstanceID: Sanitize(w)}, true
}

// fallbackVars are checked, in order, when no terminal instance id exists.
// Only CLAUDE_CODE_SESSION_ID is verified (hooks-basic.json uses it); the
// rest are best-effort.
var fallbackVars = []struct{ env, prefix string }{
	{"BLINKENKEYS_NAME", ""},
	{"CLAUDE_CODE_SESSION_ID", "claude-"},
	{"ZELLIJ_PANE_ID", "zellij-"},
	{"STY", "screen-"},
	{"WT_SESSION", "wt-"},
	{"TERM_SESSION_ID", "termsession-"},
}

// FallbackName returns a key name built from the first unique id found in
// the environment, and which variable it came from; "" if none.
func FallbackName(getenv func(string) string) (name, source string) {
	for _, v := range fallbackVars {
		if val := getenv(v.env); val != "" {
			return Sanitize(v.prefix + val), v.env
		}
	}
	return "", ""
}
