package client

import (
	"fmt"
	"hash/fnv"
	"math"
	"strings"

	"github.com/seefood/blinkenkeys/internal/keyaddr"
	"github.com/seefood/blinkenkeys/internal/termid"
)

// ErrNoKey means no key could be derived: no -k/-n and no unique id in the
// environment. It is a usage error (exit 64).
var ErrNoKey = fmt.Errorf("%w: cannot work out which key to use", ErrUsage)

// Mode is how a key was chosen.
type Mode int

const (
	ModeExplicit Mode = iota // -k / $BLINKENKEYS_KEY, used verbatim
	ModeSlot                 // direct write to the tab's slot key
	ModeNamed                // named claim (pool), shared slot on 409
)

// KeyInput is everything PlanKey needs; none of it requires the network.
type KeyInput struct {
	Key      string // -k or $BLINKENKEYS_KEY
	Name     string // -n
	ID       termid.Identity
	Fallback string // termid.FallbackName result
}

// Plan is the chosen key strategy. Base is the unqualified identity; Name is
// the claim name (ModeNamed); Owner is the tag sent with writes and clears
// ("" for explicit -k, which clears unconditionally).
type Plan struct {
	Mode  Mode
	Key   string
	Tab   int
	Base  string
	Name  string
	Owner string
	Given bool // Name came from -n verbatim (never host-qualified)
}

// PlanKey picks the key strategy: -k, then -n, then the terminal's tab slot,
// then a name from the terminal's instance id, then the environment
// fallback; otherwise ErrNoKey.
func PlanKey(in KeyInput) (Plan, error) {
	switch {
	case in.Key != "":
		return Plan{Mode: ModeExplicit, Key: in.Key}, nil
	case in.Name != "":
		if strings.ContainsAny(in.Name, ",/") {
			return Plan{}, fmt.Errorf("%w: key names must not contain ',' or '/'", ErrUsage)
		}
		// The daemon would read led:N / idx:N as a direct address (a write,
		// not a claim), and "." / ".." are not usable URL path segments.
		if a, err := keyaddr.Parse(in.Name); err != nil || a.Kind != keyaddr.Name || in.Name == "." || in.Name == ".." {
			return Plan{}, fmt.Errorf("%w: %q is not a usable key name (no led:/idx: prefix, not . or ..); use -k for a direct address", ErrUsage, in.Name)
		}
		if len(in.Name) > maxName {
			return Plan{}, fmt.Errorf("%w: key names are limited to %d bytes", ErrUsage, maxName)
		}
		return Plan{Mode: ModeNamed, Base: in.Name, Name: in.Name, Owner: in.Name, Given: true}, nil
	case in.ID.Tab > 0:
		n := in.ID.Name()
		return Plan{Mode: ModeSlot, Tab: in.ID.Tab, Base: n, Owner: fitName(n)}, nil
	case in.ID.InstanceID != "":
		n := in.ID.Name()
		return Plan{Mode: ModeNamed, Base: n, Name: fitName(n), Owner: fitName(n)}, nil
	case in.Fallback != "":
		n := in.Fallback
		return Plan{Mode: ModeNamed, Base: n, Name: fitName(n), Owner: fitName(n)}, nil
	default:
		return Plan{}, fmt.Errorf("%w: pass -k KEY or -n NAME, or run inside a recognized terminal (checked tmux, iTerm2, WezTerm, kitty) or with one of $BLINKENKEYS_NAME, $CLAUDE_CODE_SESSION_ID, $ZELLIJ_PANE_ID, $STY, $WT_SESSION, $TERM_SESSION_ID set", ErrNoKey)
	}
}

// Qualify prefixes a derived identity with host so panes on different
// machines sharing one daemon don't collide. Explicit -k plans and -n names
// are unchanged, so hosts can share a name on purpose.
func (p Plan) Qualify(host string) Plan {
	if host == "" || p.Base == "" || p.Mode == ModeExplicit || p.Given {
		return p
	}
	q := fitName(host + "." + p.Base)
	p.Owner = q
	if p.Mode == ModeNamed {
		p.Name = q
	}
	return p
}

// maxName is the daemon's limit on claim names and owner tags (1-128 bytes).
const maxName = 128

// fitName shortens a derived name over maxName bytes to a readable prefix
// plus "-" and a 16-hex-digit FNV-1a hash of the whole name: deterministic,
// and distinct long names stay distinct. Derived names are ASCII (termid
// sanitizes them), so the byte cut never splits a rune.
func fitName(s string) string {
	if len(s) <= maxName {
		return s
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	sum := fmt.Sprintf("%016x", h.Sum64())
	return s[:maxName-len(sum)-1] + "-" + sum
}

// DefaultTabs is idx 0..n-1.
func DefaultTabs(n int) []uint16 {
	out := make([]uint16, n)
	for i := range out {
		out[i] = uint16(i)
	}
	return out
}

// SlotKey maps a 1-based tab number onto tabs, wrapping.
func SlotKey(tab int, tabs []uint16) string {
	return fmt.Sprintf("idx:%d", tabs[(tab-1)%len(tabs)])
}

// SharedKey places a name on a tab slot by hash (FNV-1a), for when the pool
// is empty or exhausted: the same name always lands on the same key, with no
// daemon state.
func SharedKey(name string, tabs []uint16) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("idx:%d", tabs[int(h.Sum32()&math.MaxInt32)%len(tabs)])
}
