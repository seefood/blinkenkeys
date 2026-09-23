package dispatcher

import (
	"reflect"
	"testing"

	"github.com/seefood/blinkenkeys/internal/hid"
)

func TestCacheUpdateAndSnapshot(t *testing.T) {
	c := NewCache()
	if got := c.Snapshot("a"); got != nil {
		t.Fatalf("Snapshot before any Update = %v, want nil", got)
	}

	c.Update("a", []hid.KeyColor{{Index: 2, H: 1, S: 2, V: 3}})
	c.Update("a", []hid.KeyColor{{Index: 0, H: 4, S: 5, V: 6}})
	c.Update("a", []hid.KeyColor{{Index: 2, H: 9, S: 9, V: 9}}) // overwrite index 2

	got := c.Snapshot("a")
	want := []hid.KeyColor{{Index: 0, H: 4, S: 5, V: 6}, {Index: 2, H: 9, S: 9, V: 9}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot = %+v, want %+v", got, want)
	}
}

func TestCacheSnapshotOtherDeviceUnaffected(t *testing.T) {
	c := NewCache()
	c.Update("a", []hid.KeyColor{{Index: 0}})
	if got := c.Snapshot("b"); got != nil {
		t.Errorf("Snapshot(b) = %v, want nil", got)
	}
}

func TestCacheForget(t *testing.T) {
	c := NewCache()
	c.Update("a", []hid.KeyColor{{Index: 0}})
	c.Forget("a")
	if got := c.Snapshot("a"); got != nil {
		t.Errorf("Snapshot after Forget = %v, want nil", got)
	}
}
