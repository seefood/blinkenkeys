package config

import (
	"reflect"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestKeyListDecodes(t *testing.T) {
	tests := []struct {
		name, src string
		want      KeyList
	}{
		{"flow list with ranges", "k: [0-4, 6, 8-10]", KeyList{0, 1, 2, 3, 4, 6, 8, 9, 10}},
		{"single range in list", "k: [0-5]", KeyList{0, 1, 2, 3, 4, 5}},
		{"plain ints", "k: [3, 1, 2]", KeyList{3, 1, 2}},
		{"string form", `k: "0-4,6,8-10"`, KeyList{0, 1, 2, 3, 4, 6, 8, 9, 10}},
		{"order preserved", `k: "5,1-2"`, KeyList{5, 1, 2}},
		{"explicit empty list", "k: []", KeyList{}},
		{"empty string", `k: ""`, KeyList{}},
	}
	for _, tt := range tests {
		var got struct {
			K KeyList `yaml:"k"`
		}
		if err := yaml.Unmarshal([]byte(tt.src), &got); err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if got.K == nil || !reflect.DeepEqual(got.K, tt.want) {
			t.Errorf("%s: got %#v, want %#v (non-nil)", tt.name, got.K, tt.want)
		}
	}
}

func TestKeyListOmittedStaysNil(t *testing.T) {
	var got struct {
		K KeyList `yaml:"k"`
	}
	if err := yaml.Unmarshal([]byte("other: 1"), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.K != nil {
		t.Errorf("omitted key decoded to %#v, want nil", got.K)
	}
}

// A YAML null (`pool: ~`, `pool: null`, bare `pool:`) means "unset", the same
// as omitting the key: for pool that is the default pool, not an empty one.
func TestKeyListNullIsUnset(t *testing.T) {
	for _, src := range []string{"k: ~", "k: null", "k:"} {
		var got struct {
			K KeyList `yaml:"k"`
		}
		if err := yaml.Unmarshal([]byte(src), &got); err != nil {
			t.Fatalf("%q: unmarshal: %v", src, err)
		}
		if got.K != nil {
			t.Errorf("%q decoded to %#v, want nil (unset)", src, got.K)
		}
	}
	// goccy/go-yaml skips the unmarshaler for a null today; if a decoder ever
	// passes one through, UnmarshalYAML itself must still say "unset".
	k := KeyList{1}
	if err := k.UnmarshalYAML(func(any) error { return nil }); err != nil || k != nil {
		t.Errorf("UnmarshalYAML(null) = %#v, %v; want nil", k, err)
	}
	cfg, err := Load(writeConfig(t, "devices:\n  - id: a\n    keys:\n      tabs: [0-1]\n      pool: ~\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Devices[0].Keys.Pool != nil {
		t.Errorf("pool: ~ must mean the default pool (nil), got %#v", cfg.Devices[0].Keys.Pool)
	}
}

func TestKeyListRejects(t *testing.T) {
	for _, src := range []string{`k: "5-2"`, `k: "x"`, `k: "-1"`, `k: [a]`, `k: "70000"`, "k: {a: 1}", "k: [1.5]"} {
		var got struct {
			K KeyList `yaml:"k"`
		}
		if err := yaml.Unmarshal([]byte(src), &got); err == nil {
			t.Errorf("%s: want error, got %#v", src, got.K)
		}
	}
}

func TestLoadKeyLayout(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
devices:
  - id: macropad
    keys:
      tabs: [0-5]
      pool: [6-11]
  - id: other
    keys:
      tabs: [0-1]
      pool: []
  - id: plain
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := cfg.Devices
	if !reflect.DeepEqual(d[0].Keys.Tabs, KeyList{0, 1, 2, 3, 4, 5}) || !reflect.DeepEqual(d[0].Keys.Pool, KeyList{6, 7, 8, 9, 10, 11}) {
		t.Errorf("macropad keys = %+v", d[0].Keys)
	}
	if d[1].Keys.Pool == nil || len(d[1].Keys.Pool) != 0 {
		t.Errorf("pool: [] must decode non-nil empty, got %#v", d[1].Keys.Pool)
	}
	if d[2].Keys != nil {
		t.Errorf("device without keys: must have nil Keys, got %+v", d[2].Keys)
	}
	cfg2, err := Load(writeConfig(t, "devices:\n  - id: a\n    keys:\n      tabs: [0-1]\n"))
	if err != nil || cfg2.Devices[0].Keys.Pool != nil {
		t.Errorf("omitted pool must stay nil (default pool): %+v, %v", cfg2.Devices[0].Keys, err)
	}
}

func TestLoadKeyLayoutCollision(t *testing.T) {
	cfg, err := Load(writeConfig(t, "devices:\n  - id: a\n    keys:\n      collision: displace\n"))
	if err != nil || cfg.Devices[0].Keys.Collision != "displace" {
		t.Fatalf("displace: %+v, %v", cfg, err)
	}
	cfg, err = Load(writeConfig(t, "devices:\n  - id: a\n    keys:\n      tabs: [0]\n"))
	if err != nil || cfg.Devices[0].Keys.Collision != "" {
		t.Errorf("omitted collision must stay empty (last-wins): %+v, %v", cfg, err)
	}
	if _, err := Load(writeConfig(t, "devices:\n  - id: a\n    keys:\n      collision: nope\n")); err == nil {
		t.Error("unknown collision policy must be rejected")
	}
}

func TestLoadKeyLayoutRejectsOverlap(t *testing.T) {
	for _, body := range []string{
		"tabs: [0-5]\n      pool: [5-6]",
		"tabs: [1, 1]",
		"pool: [2-3, 3]",
	} {
		if _, err := Load(writeConfig(t, "devices:\n  - id: a\n    keys:\n      "+body+"\n")); err == nil {
			t.Errorf("%q: want error", body)
		}
	}
}
