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
		p.exact[entry] = struct{}{}
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
