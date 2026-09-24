// Package keyaddr parses the REST API's {pos} key-address forms and resolves
// them to a VialRGB LED index against a device's capabilities. It has no
// dependency on the dispatcher, so the API, the effects engine, and pending-
// write resolution share one implementation.
package keyaddr

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kind is which {pos} form an Address was written in.
type Kind int

const (
	RowCol Kind = iota // "R,C": matrix row/column
	LED                // "led:N": raw VialRGB LED index (firmware order)
	Idx                // "idx:N": reading-order index over keyed LEDs
	Name               // anything else: a key name (reserved for Phase 5)
)

// Address is one parsed {pos}. Only the fields for its Kind are set.
type Address struct {
	Kind     Kind
	Row, Col uint8
	N        uint16
	Name     string
}

// ErrInvalid is returned for a malformed {pos}.
var ErrInvalid = errors.New("keyaddr: invalid key address")

// noKey is VialRGB's row/col for an LED with no matrix key (e.g. underglow).
const noKey = 0xFF

// Parse parses s, tried in order: "led:N", "idx:N", "R,C" (any string
// containing a comma must be a valid R,C), else a Name.
func Parse(s string) (Address, error) {
	switch {
	case s == "":
		return Address{}, fmt.Errorf("%w: empty", ErrInvalid)
	case strings.HasPrefix(s, "led:"):
		n, err := strconv.ParseUint(s[len("led:"):], 10, 16)
		if err != nil {
			return Address{}, fmt.Errorf("%w: %q", ErrInvalid, s)
		}
		return Address{Kind: LED, N: uint16(n)}, nil
	case strings.HasPrefix(s, "idx:"):
		n, err := strconv.ParseUint(s[len("idx:"):], 10, 16)
		if err != nil {
			return Address{}, fmt.Errorf("%w: %q", ErrInvalid, s)
		}
		return Address{Kind: Idx, N: uint16(n)}, nil
	case strings.Contains(s, ","):
		parts := strings.Split(s, ",")
		if len(parts) != 2 {
			return Address{}, fmt.Errorf("%w: %q: want row,col", ErrInvalid, s)
		}
		r, err1 := strconv.ParseUint(parts[0], 10, 8)
		c, err2 := strconv.ParseUint(parts[1], 10, 8)
		if err1 != nil || err2 != nil {
			return Address{}, fmt.Errorf("%w: %q: want row,col", ErrInvalid, s)
		}
		return Address{Kind: RowCol, Row: uint8(r), Col: uint8(c)}, nil
	default:
		return Address{Kind: Name, Name: s}, nil
	}
}

// String renders a in the same form Parse accepts.
func (a Address) String() string {
	switch a.Kind {
	case RowCol:
		return fmt.Sprintf("%d,%d", a.Row, a.Col)
	case LED:
		return fmt.Sprintf("led:%d", a.N)
	case Idx:
		return fmt.Sprintf("idx:%d", a.N)
	default:
		return a.Name
	}
}

// Position is one LED's matrix location, as reported by VialRGB's
// VIALRGB_GET_LED_INFO.
type Position struct {
	Index uint16 `json:"index"`
	Row   uint8  `json:"row"`
	Col   uint8  `json:"col"`
}

// Resolve maps a to an LED index given a device's LED count and positions.
// LEDs with no matrix key (row and col 0xFF) never match R,C and are
// excluded from idx: numbering. Name addresses never resolve.
func Resolve(a Address, ledCount int, positions []Position) (uint16, bool) {
	switch a.Kind {
	case LED:
		if int(a.N) < ledCount {
			return a.N, true
		}
		return 0, false
	case RowCol:
		if a.Row == noKey && a.Col == noKey {
			return 0, false
		}
		for _, p := range positions {
			if p.Row == a.Row && p.Col == a.Col {
				return p.Index, true
			}
		}
		return 0, false
	case Idx:
		keyed := make([]Position, 0, len(positions))
		for _, p := range positions {
			if p.Row != noKey || p.Col != noKey {
				keyed = append(keyed, p)
			}
		}
		sort.Slice(keyed, func(i, j int) bool {
			if keyed[i].Row != keyed[j].Row {
				return keyed[i].Row < keyed[j].Row
			}
			return keyed[i].Col < keyed[j].Col
		})
		if int(a.N) >= len(keyed) {
			return 0, false
		}
		return keyed[a.N].Index, true
	default:
		return 0, false
	}
}
