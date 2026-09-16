package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// IPFilter checks incoming client IPs against configured whitelist and blacklist rules.
type IPFilter struct {
	whitelistIPs  map[string]bool
	whitelistNets []*net.IPNet
	blacklistIPs  map[string]bool
	blacklistNets []*net.IPNet
	hasWhitelist  bool
	hasBlacklist  bool
}

// NewIPFilter creates a new IPFilter from whitelist and blacklist IP/CIDR string slices.
func NewIPFilter(whitelist, blacklist []string) *IPFilter {
	f := &IPFilter{
		whitelistIPs: make(map[string]bool),
		blacklistIPs: make(map[string]bool),
	}

	for _, item := range whitelist {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			_, ipNet, err := net.ParseCIDR(item)
			if err == nil {
				f.whitelistNets = append(f.whitelistNets, ipNet)
				f.hasWhitelist = true
			}
		} else {
			if ip := net.ParseIP(item); ip != nil {
				f.whitelistIPs[ip.String()] = true
				f.hasWhitelist = true
			}
		}
	}

	for _, item := range blacklist {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			_, ipNet, err := net.ParseCIDR(item)
			if err == nil {
				f.blacklistNets = append(f.blacklistNets, ipNet)
				f.hasBlacklist = true
			}
		} else {
			if ip := net.ParseIP(item); ip != nil {
				f.blacklistIPs[ip.String()] = true
				f.hasBlacklist = true
			}
		}
	}

	return f
}

// IsAllowed returns true if the client IP is allowed, or false with a rejection reason.
func (f *IPFilter) IsAllowed(clientIP string) (bool, string) {
	if clientIP == "" {
		clientIP = "unknown"
	}

	parsed := net.ParseIP(clientIP)

	// Check blacklist first
	if f.hasBlacklist {
		if parsed != nil {
			if f.blacklistIPs[parsed.String()] {
				return false, fmt.Sprintf("IP %s is blacklisted", clientIP)
			}
			for _, n := range f.blacklistNets {
				if n.Contains(parsed) {
					return false, fmt.Sprintf("IP %s is in blacklisted CIDR %s", clientIP, n.String())
				}
			}
		} else if f.blacklistIPs[clientIP] {
			return false, fmt.Sprintf("IP %s is blacklisted", clientIP)
		}
	}

	// If whitelist is configured, client must be in whitelist
	if f.hasWhitelist {
		matched := false
		if parsed != nil {
			if f.whitelistIPs[parsed.String()] {
				matched = true
			} else {
				for _, n := range f.whitelistNets {
					if n.Contains(parsed) {
						matched = true
						break
					}
				}
			}
		} else if f.whitelistIPs[clientIP] {
			matched = true
		}

		if !matched {
			return false, fmt.Sprintf("IP %s is not in whitelist", clientIP)
		}
	}

	return true, ""
}

// GetClientIP extracts the real client IP from request headers or RemoteAddr.
func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		ip := strings.TrimSpace(xrip)
		if ip != "" {
			return ip
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
