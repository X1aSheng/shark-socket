package plugin

import (
	"fmt"
	"net"
	"sync/atomic"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// blacklistSet is the immutable block list a Blacklist swaps in atomically.
// Two fields in one struct so a reload cannot publish the new exact hosts and
// the old CIDRs at the same time: OnAccept reads a single pointer.
type blacklistSet struct {
	exact map[string]struct{}
	nets  []*net.IPNet
}

type Blacklist struct {
	core.BasePlugin
	set atomic.Pointer[blacklistSet]
}

// NewBlacklist builds a block list, skipping entries that are neither an IP nor
// a CIDR.
//
// Skipping rather than failing keeps the constructor usable in code (where the
// literals are reviewed anyway). Replace, which takes operator input from a
// config file, is strict: a typo'd entry that is silently dropped would look
// exactly like a block that does not work.
func NewBlacklist(entries ...string) *Blacklist {
	p := &Blacklist{}
	set, err := parseBlacklist(entries)
	if err != nil {
		// Keep every entry that did parse: a partly valid list still blocks
		// what it can, and the caller that cares uses Replace.
		set, _ = parseBlacklistBestEffort(entries)
	}
	p.set.Store(set)
	return p
}

// Replace swaps the whole block list. Entries are validated first, so a
// malformed list leaves the previous one in force (RFC of the reload path:
// never apply a configuration that failed to parse).
func (p *Blacklist) Replace(entries []string) error {
	set, err := parseBlacklist(entries)
	if err != nil {
		return err
	}
	p.set.Store(set)
	return nil
}

// ValidateBlacklist reports whether every entry is an IP literal or a CIDR.
func ValidateBlacklist(entries []string) error {
	_, err := parseBlacklist(entries)
	return err
}

func parseBlacklist(entries []string) (*blacklistSet, error) {
	set := &blacklistSet{exact: make(map[string]struct{}, len(entries))}
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			set.nets = append(set.nets, cidr)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("blacklist entry %q is neither an IP nor a CIDR", entry)
		}
		set.exact[normalizeIP(entry)] = struct{}{}
	}
	return set, nil
}

func parseBlacklistBestEffort(entries []string) (*blacklistSet, error) {
	set := &blacklistSet{exact: make(map[string]struct{}, len(entries))}
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			set.nets = append(set.nets, cidr)
			continue
		}
		if net.ParseIP(entry) != nil {
			set.exact[normalizeIP(entry)] = struct{}{}
		}
	}
	return set, nil
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
	set := p.set.Load()
	if set == nil {
		return nil
	}
	if _, ok := set.exact[host]; ok {
		return core.ErrPluginBlock
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	for _, cidr := range set.nets {
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
