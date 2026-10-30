package instances

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Normalise turns operator-supplied input into the canonical form used as an
// instance's identity.
//
// It is not cosmetic. The index is keyed on this string, so two spellings of one
// instance are two entries, and a lookup by a hand-typed URL with a trailing
// slash would fail to find the entry it was meant to find.
//
// The rules, in order:
//
//   - an empty or whitespace-only value is rejected rather than normalised,
//     because "" does not parse as a URL and the resulting error would be about
//     parsing rather than about the input being missing
//   - a bare host is given an https scheme: an instance is served over TLS, and
//     defaulting to http would invite a downgrade that strips the credentials
//     clients send
//   - the path, query and fragment are removed, because an instance's base URL
//     is its origin; "/api" belongs in the deployment's configuration, and
//     carrying it in the index would make the probe path ambiguous
//   - the trailing slash on the remaining path is dropped, again so one
//     instance is one string
//   - the host is lowercased and the default port is removed, because
//     "https://Alt.Example:443/" and "https://alt.example" are the same
//     instance
//
// Userinfo is rejected rather than stripped. "https://user:pass@host" in an
// index would publish a credential, and a URL that needs one is not something
// this feature can verify.
func Normalise(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("instance URL is required")
	}

	// A bare host such as "alt.example" or "alt.example:8080" has no scheme, so
	// url.Parse reads the whole thing as a path.
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("instance URL is not a valid URL: %w", err)
	}

	if parsed.User != nil {
		return "", fmt.Errorf("instance URL must not contain credentials")
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("instance URL must include a host")
	}

	// An omitted port means the scheme's default and is the common case, so it
	// is not an error; an explicit default port is dropped so the two spellings
	// are one identity.
	port := parsed.Port()

	// url.Parse accepts a port that is not a port. Storing one would put a URL
	// in the index that nothing can dial, and the probe would fail every day
	// with a DNS error that looks like the operator's problem rather than the
	// input's.
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("instance URL has an invalid port %q", port)
		}
	}
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}

	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("instance URL must be http or https, not %q", parsed.Scheme)
	}

	// http is permitted only because a local instance on a LAN is a real thing
	// to register and refusing it would push someone to publish a private
	// service over the internet to get a badge. It is not the default: the
	// scheme is filled in as https above when none was given.
	return parsed.Scheme + "://" + host, nil
}

// ResolveBlockedIP reports whether an address must not be dialled.
//
// Every instance URL is fetched by the backend on behalf of whoever registered
// it, which means an admin-gated registration can still be pointed at the
// machine's own metadata service or its private network. The block list is
// therefore checked on the resolved address, not on the hostname: a name that
// resolves to a private address is exactly the case a string check misses.
//
// This is a guard on top of admin-only registration, not a replacement for it.
// The realistic threat is social engineering an operator into pasting a URL, and
// a block list is the cheapest thing that stops the obvious version of that.
func ResolveBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() {
		return true
	}

	// Carrier-grade NAT, 100.64.0.0/10. Not private and not link-local, but
	// unreachable from the internet either, and it is where Alibaba puts its
	// instance metadata service — so this one range is both.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return true
	}

	return ip.IsPrivate()
}

// BlockedHostError describes why a URL was refused before any request was made.
type BlockedHostError struct {
	Host   string
	Reason string
}

func (e BlockedHostError) Error() string {
	return fmt.Sprintf("refusing to fetch %s: %s", e.Host, e.Reason)
}
