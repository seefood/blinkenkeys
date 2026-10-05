package client

import (
	"fmt"
	"hash/fnv"
	"math"
	"strings"

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
		return Plan{Mode: ModeNamed, Base: in.Name, Name: in.Name, Owner: in.Name}, nil
	case in.ID.Tab > 0:
		n := in.ID.Name()
		return Plan{Mode: ModeSlot, Tab: in.ID.Tab, Base: n, Owner: n}, nil
	case in.ID.InstanceID != "":
		n := in.ID.Name()
		return Plan{Mode: ModeNamed, Base: n, Name: n, Owner: n}, nil
	case in.Fallback != "":
		return Plan{Mode: ModeNamed, Base: in.Fallback, Name: in.Fallback, Owner: in.Fallback}, nil
	default:
		return Plan{}, fmt.Errorf("%w: pass -k KEY or -n NAME, or run inside a recognized terminal (checked tmux, iTerm2, WezTerm, kitty) or with one of $BLINKENKEYS_NAME, $CLAUDE_CODE_SESSION_ID, $ZELLIJ_PANE_ID, $STY, $WT_SESSION, $TERM_SESSION_ID set", ErrNoKey)
	}
}

// Qualify prefixes a derived identity with host so panes on different
// machines sharing one daemon don't collide. Explicit plans are unchanged.
func (p Plan) Qualify(host string) Plan {
	if host == "" || p.Base == "" || p.Mode == ModeExplicit {
		return p
	}
	q := host + "." + p.Base
	p.Owner = q
	if p.Mode == ModeNamed {
		p.Name = q
	}
	return p
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
