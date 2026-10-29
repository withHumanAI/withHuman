// Package webhookdelivery is the one outbound HTTP client the product uses
// on an organization's behalf: it enforces the egress policy, signs every
// delivery, bounds the response, and parses the closed answer shape. The
// same client fetches the public documents a request names, such as an
// OAuth client's metadata document, under the same policy.
package webhookdelivery

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrDestinationBlocked means the URL or the address it resolves to is
// outside the egress policy.
var ErrDestinationBlocked = errors.New("webhook destination is not allowed by the egress policy")

// Policy is the egress policy. The defaults are public HTTPS only; the
// two flags exist for development and tests.
type Policy struct {
	AllowInsecure        bool
	AllowPrivateNetworks bool
}

// ValidateURL checks the static shape of a destination: absolute, a host,
// https unless insecure is allowed, no userinfo, no fragment, and an
// IP-literal host inside the policy. Resolved addresses are checked again
// at dial time.
func (policy Policy) ValidateURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return nil, fmt.Errorf("webhook URL must be absolute with a host: %w", ErrDestinationBlocked)
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !policy.AllowInsecure {
			return nil, fmt.Errorf("webhook URL must use https: %w", ErrDestinationBlocked)
		}
	default:
		return nil, fmt.Errorf("webhook URL scheme %q is not allowed: %w", parsed.Scheme, ErrDestinationBlocked)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("webhook URL must not carry credentials: %w", ErrDestinationBlocked)
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("webhook URL must not carry a fragment: %w", ErrDestinationBlocked)
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !policy.AllowIP(ip) {
		return nil, fmt.Errorf("webhook URL points at a blocked address: %w", ErrDestinationBlocked)
	}
	return parsed, nil
}

// AllowIP reports whether the policy permits connecting to ip: no
// loopback, unspecified, private, link-local (which includes the cloud
// metadata address), multicast, or reserved ranges unless private
// networks are allowed.
func (policy Policy) AllowIP(ip net.IP) bool {
	if policy.AllowPrivateNetworks {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, network := range blockedNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

// blockedNetworks are reserved ranges the standard predicates miss:
// "this network", carrier-grade NAT, IETF protocol assignments,
// documentation, benchmarking, class E, NAT64, and IPv6 documentation.
var blockedNetworks = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "2001:db8::/32",
	}
	networks := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		networks = append(networks, network)
	}
	return networks
}()
