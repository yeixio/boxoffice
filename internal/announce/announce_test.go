package announce

import (
	"net"
	"slices"
	"testing"
)

func TestTXT(t *testing.T) {
	got := Announcement{Port: 7272, ID: "abc", Version: "1.2.3"}.TXT()
	want := []string{"product=boxoffice", "version=1.2.3", "id=abc"}
	if !slices.Equal(got, want) {
		t.Fatalf("TXT = %v, want %v", got, want)
	}
}

func TestPort(t *testing.T) {
	for addr, want := range map[string]int{":7272": 7272, "0.0.0.0:8080": 8080, "[::]:9000": 9000} {
		if got, err := Port(addr); err != nil || got != want {
			t.Errorf("Port(%q) = %d, %v; want %d", addr, got, err, want)
		}
	}
	if _, err := Port("nope"); err == nil {
		t.Error("Port(nope) gave no error")
	}
}

func TestPickHome(t *testing.T) {
	up := net.FlagUp | net.FlagMulticast
	all := []net.Interface{
		{Name: "eth0", Flags: up},
		{Name: "lo", Flags: up | net.FlagLoopback},
		{Name: "docker0", Flags: up},
		{Name: "cni0", Flags: up},
		{Name: "wlan0", Flags: net.FlagMulticast},
	}
	got := pickHome(all)
	if len(got) != 1 || got[0].Name != "eth0" {
		t.Fatalf("pickHome = %v, want eth0 only", got)
	}
}
