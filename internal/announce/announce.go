// Package announce tells the home network Box Office is here, with DNS-SD
// over multicast DNS, so Matinee can fill in its address on its own.
// Nothing leaves the local network.
package announce

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/libp2p/zeroconf/v2"
)

// Service is the DNS-SD service type Matinee browses for.
const Service = "_boxoffice._tcp"

// Announcement is what Matinee sees.
type Announcement struct {
	Port    int
	ID      string // the instance id, as at GET /api/v1/status
	Version string
}

// TXT is the record Matinee reads.
func (a Announcement) TXT() []string {
	txt := []string{"product=boxoffice", "version=" + a.Version}
	if a.ID != "" {
		txt = append(txt, "id="+a.ID)
	}
	return txt
}

// Port is the port in a listen address such as ":7272" or "0.0.0.0:7272".
func Port(addr string) (int, error) {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(p)
}

// Run announces a until ctx ends.
func Run(ctx context.Context, a Announcement, log *slog.Logger) error {
	server, err := zeroconf.Register("Box Office", Service, "local.", a.Port, a.TXT(), homeInterfaces())
	if err != nil {
		return fmt.Errorf("announce on the local network: %w", err)
	}
	log.Info("announcing on the local network", "service", Service, "port", a.Port)
	<-ctx.Done()
	server.Shutdown()
	return nil
}

// virtualPrefixes name the interfaces of containers, Kubernetes networks and
// VPNs, whose addresses the home network can't reach.
var virtualPrefixes = []string{
	"cni", "flannel", "docker", "veth", "br-", "cali", "cilium", "vxlan", "kube",
	"tunl", "tun", "tap", "wg", "tailscale", "zt", "lxc", "lxd", "virbr", "vmnet", "podman", "weave", "genev",
}

func virtual(name string) bool {
	name = strings.ToLower(name)
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// homeInterfaces lists the interfaces facing the home network; nil, when
// there's none it can tell, announces on all of them.
func homeInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	return pickHome(all)
}

func pickHome(all []net.Interface) []net.Interface {
	var out []net.Interface
	for _, i := range all {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagMulticast == 0 || virtual(i.Name) {
			continue
		}
		out = append(out, i)
	}
	return out
}
