package plugin

import (
	"net"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Blacklist struct {
	core.BasePlugin
	exact map[string]struct{}
	nets  []*net.IPNet
}

func NewBlacklist(entries ...string) *Blacklist {
	p := &Blacklist{exact: make(map[string]struct{})}
	for _, entry := range entries {
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			p.nets = append(p.nets, cidr)
			continue
		}
		p.exact[normalizeIP(entry)] = struct{}{}
	}
	return p
}

func (p *Blacklist) Name() string  { return "blacklist" }
func (p *Blacklist) Priority() int { return 0 }

func (p *Blacklist) OnAccept(sess core.Session) error {
	addr := sess.RemoteAddr()
	if addr == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	// Normalize both sides so an IPv4-mapped IPv6 address
	// ("::ffff:10.0.0.1" from a dual-stack socket) cannot bypass an exact
	// "10.0.0.1" entry.
	host = normalizeIP(host)
	if _, ok := p.exact[host]; ok {
		return core.ErrPluginBlock
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	for _, cidr := range p.nets {
		if cidr.Contains(ip) {
			return core.ErrPluginBlock
		}
	}
	return nil
}

// normalizeIP returns the canonical string form of an IP literal, mapping
// IPv4-mapped IPv6 addresses (::ffff:a.b.c.d) to plain IPv4 so exact matches
// are consistent across dual-stack sockets. Non-IP strings (hostnames) are
// returned unchanged.
func normalizeIP(host string) string {
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.String()
}
