package api

import (
	"testing"

	"github.com/yeixio/boxoffice/internal/monitor"
)

// Shares of one server read a little differently from one moment to the
// next while files are written; they're still one disk.
func TestSameDisk(t *testing.T) {
	const tb = 1 << 40
	array := monitor.Disk{Total: 27 * tb, Free: 2600 << 30}
	for _, c := range []struct {
		b    monitor.Disk
		want bool
	}{
		{monitor.Disk{Total: 27 * tb, Free: 2600 << 30}, true},
		{monitor.Disk{Total: 27 * tb, Free: 2598 << 30}, true},
		{monitor.Disk{Total: 27 * tb, Free: 1200 << 30}, false},
		{monitor.Disk{Total: 4 * tb, Free: 2600 << 30}, false},
	} {
		if got := sameDisk(array, c.b); got != c.want {
			t.Errorf("%+v: %v", c.b, got)
		}
	}
}
