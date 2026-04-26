package utils

import (
	"net"
	"strings"
)

// ParseIP extracts the pure IP address from an address string (strips port).
func ParseIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// ParseCIDR parses a CIDR string and returns the network.
func ParseCIDR(s string) (*net.IPNet, error) {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return nil, err
	}
	return ipnet, nil
}

// IPToKey normalizes an IP for use as a map key.
func IPToKey(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.To16().String()
}

// IsPrivateIP checks if an IP address is in a private range.
func IsPrivateIP(ip net.IP) bool {
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// ExtractIPFromAddr extracts a net.IP from a net.Addr.
func ExtractIPFromAddr(addr net.Addr) net.IP {
	switch a := addr.(type) {
	case *net.TCPAddr:
		return a.IP
	case *net.UDPAddr:
		return a.IP
	default:
		host := ParseIP(addr.String())
		return net.ParseIP(host)
	}
}

// NormalizeIP normalizes an IP string for consistent map lookups.
func NormalizeIP(ipStr string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return strings.TrimSpace(ipStr)
	}
	return IPToKey(ip)
}
