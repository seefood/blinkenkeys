package hid

import "testing"

func TestFilterVial(t *testing.T) {
	infos := []Info{
		{Path: "/dev/hidraw0", UsagePage: 0x0001, Usage: 0x0006}, // a keyboard interface, not ours
		{Path: "/dev/hidraw1", UsagePage: UsagePageVial, Usage: UsageVial},
	}
	got := filterVial(infos)
	if len(got) != 1 || got[0].Path != "/dev/hidraw1" {
		t.Errorf("filterVial = %+v, want only /dev/hidraw1", got)
	}
}

func TestBaseName(t *testing.T) {
	got := BaseName(Identity{HasUID: true, UID: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}})
	if got != "uid-0102030405060708" {
		t.Errorf("BaseName(uid) = %q", got)
	}
	got = BaseName(Identity{VendorID: 0x5754, ProductID: 0xC401, Path: "/dev/hidraw3"})
	if got != "5754-c401-/dev/hidraw3" {
		t.Errorf("BaseName(vid/pid+path) = %q", got)
	}
	got = BaseName(Identity{VendorID: 0x5754, ProductID: 0xC401})
	if got != "5754-c401" {
		t.Errorf("BaseName(vid/pid only) = %q", got)
	}
}
