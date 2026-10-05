package config

import (
	"fmt"
	"strconv"
	"strings"
)

// KeyList is an ordered list of idx: (reading-order) key indexes. In YAML it
// is either a list whose items are integers or ranges ("[0-4, 6, 8-10]") or
// one string ("0-4,6,8-10"). Order is preserved as written. A key present
// but empty decodes to a non-nil empty KeyList; an omitted key stays nil, so
// callers can tell "no keys" from "unset".
type KeyList []uint16

// UnmarshalYAML implements goccy/go-yaml's InterfaceUnmarshaler.
func (k *KeyList) UnmarshalYAML(unmarshal func(any) error) error {
	var raw any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	list, err := parseKeyList(raw)
	if err != nil {
		return err
	}
	*k = list
	return nil
}

func parseKeyList(raw any) (KeyList, error) {
	out := KeyList{}
	switch v := raw.(type) {
	case nil:
		return out, nil
	case string:
		return parseKeyString(v)
	case []any:
		for _, el := range v {
			switch e := el.(type) {
			case string:
				part, err := parseKeyString(e)
				if err != nil {
					return nil, err
				}
				out = append(out, part...)
			case uint64:
				if e > 0xFFFF {
					return nil, fmt.Errorf("config: key index %d out of range", e)
				}
				out = append(out, uint16(e))
			case int64:
				if e < 0 || e > 0xFFFF {
					return nil, fmt.Errorf("config: key index %d out of range", e)
				}
				out = append(out, uint16(e))
			default:
				return nil, fmt.Errorf("config: key list element %v (%T): want an index or a range like 0-4", el, el)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("config: key list must be a list or a string like \"0-4,6\", got %T", raw)
	}
}

func parseKeyString(s string) (KeyList, error) {
	out := KeyList{}
	if strings.TrimSpace(s) == "" {
		return out, nil
	}
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
		a, err := strconv.ParseUint(strings.TrimSpace(lo), 10, 16)
		if err != nil {
			return nil, fmt.Errorf("config: key list %q: bad index %q", s, lo)
		}
		b := a
		if isRange {
			if b, err = strconv.ParseUint(strings.TrimSpace(hi), 10, 16); err != nil {
				return nil, fmt.Errorf("config: key list %q: bad index %q", s, hi)
			}
			if b < a {
				return nil, fmt.Errorf("config: key list %q: range %d-%d runs backwards", s, a, b)
			}
		}
		for n := a; n <= b; n++ {
			out = append(out, uint16(n))
		}
	}
	return out, nil
}
