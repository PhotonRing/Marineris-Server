// Package validate holds strict validators for every user-supplied value that
// ends up in a generated configuration (Caddy, Authelia, compose). Values are
// allow-listed rather than escaped, so a validated value can never break out
// of the configuration it is placed in.
package validate

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	labelRE     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	containerRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	stackRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	groupRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,31}$`)
	folderRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	pathRE      = regexp.MustCompile(`^(/[A-Za-z0-9._~-]+)+$`)
	nameRE      = regexp.MustCompile(`^[\p{L}\p{N}]([\p{L}\p{N} ._()-]{0,62}[\p{L}\p{N}._()-])?$`)
	personRE    = regexp.MustCompile(`^[\p{L}\p{N}]([\p{L}\p{N} ._()'’-]{0,62}[\p{L}\p{N}._()'’-])?$`)
	usernameRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)
	localPartRE = regexp.MustCompile(`^[A-Za-z0-9_+-]+(\.[A-Za-z0-9_+-]+)*$`)
	asciiHostRE = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	durationRE  = regexp.MustCompile(`^[1-9][0-9]{0,4}(ms|s|m|h)$`)
)

// Hostname validates a fully-qualified DNS hostname in lowercase ASCII
// (internationalised names must be given in punycode). IP addresses and
// wildcards are rejected.
func Hostname(h string) error {
	if h == "" {
		return errors.New("is required")
	}
	if len(h) > 253 {
		return errors.New("is longer than 253 characters")
	}
	if h != strings.ToLower(h) {
		return errors.New("must be lowercase")
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return errors.New("must be a hostname, not an IP address")
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return errors.New("must be a fully-qualified name (e.g. app.example.com)")
	}
	for _, l := range labels {
		if !labelRE.MatchString(l) {
			return fmt.Errorf("label %q is not valid", l)
		}
	}
	tld := labels[len(labels)-1]
	if _, err := strconv.Atoi(tld); err == nil {
		return errors.New("top-level label must not be numeric")
	}
	return nil
}

// Zone validates a domain name managed by Marineris (e.g. example.com).
func Zone(z string) error { return Hostname(z) }

// HostInZone reports whether host is zone itself or a name below it.
func HostInZone(host, zone string) bool {
	return host == zone || strings.HasSuffix(host, "."+zone)
}

// IsDirectChild reports whether host is exactly one label below zone, i.e.
// covered by a *.zone wildcard certificate.
func IsDirectChild(host, zone string) bool {
	if !strings.HasSuffix(host, "."+zone) {
		return false
	}
	return !strings.Contains(strings.TrimSuffix(host, "."+zone), ".")
}

// PathPrefix validates a route path prefix. Empty means "whole host".
func PathPrefix(p string) error {
	if p == "" {
		return nil
	}
	if len(p) > 128 {
		return errors.New("is longer than 128 characters")
	}
	if !pathRE.MatchString(p) {
		return errors.New("must look like /segment[/segment…] using letters, digits, '.', '_', '~' or '-'")
	}
	for _, seg := range strings.Split(p[1:], "/") {
		if seg == "." || seg == ".." {
			return errors.New("must not contain '.' or '..' segments")
		}
	}
	return nil
}

// ContainerName validates a Docker container name.
func ContainerName(n string) error {
	if !containerRE.MatchString(strings.TrimPrefix(n, "/")) {
		return errors.New("is not a valid container name")
	}
	return nil
}

// StackName validates a compose project name.
func StackName(n string) error {
	if !stackRE.MatchString(n) {
		return errors.New("must be 1-63 characters: lowercase letters, digits, '_' or '-'")
	}
	return nil
}

// FolderName validates a single folder name below the static root.
func FolderName(n string) error {
	if !folderRE.MatchString(n) || strings.Contains(n, "..") {
		return errors.New("must be 1-64 characters: lowercase letters, digits, '.', '_' or '-'")
	}
	return nil
}

// GroupName validates the name of a custom Group: it lands in Authelia's
// users file and, later, in OIDC groups claims. The built-in names are
// reserved.
func GroupName(n string) error {
	if !groupRE.MatchString(n) {
		return errors.New("must be 2-32 characters: lowercase letters, digits, '_' or '-', starting with a letter or digit")
	}
	if n == "admins" || n == "proxy-admins" {
		return errors.New("is reserved for a built-in Group")
	}
	return nil
}

// DisplayName validates a human-readable name: a route's, a DNS profile's or
// a passkey's. Spaces only inside.
func DisplayName(n string) error {
	if !nameRE.MatchString(n) {
		return errors.New("must be 1-64 characters: letters, digits, spaces, '.', '_', '(', ')' or '-', starting with a letter or digit")
	}
	return nil
}

// UserDisplayName validates a User's display name: DisplayName's characters
// plus the apostrophe (' or ’) that people's names carry (O'Brien). Authelia's
// users file is written by a YAML encoder, which quotes it.
func UserDisplayName(n string) error {
	if !personRE.MatchString(n) {
		return errors.New("must be 1-64 characters: letters, digits, spaces, apostrophes, '.', '_', '(', ')' or '-', starting with a letter or digit")
	}
	return nil
}

// Username validates a new Username: what works as one in Authelia and in
// every app. Uniqueness (ignoring case) is the store's check.
func Username(n string) error {
	if !usernameRE.MatchString(n) {
		return errors.New("must be 2-32 characters: lowercase letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	return nil
}

// Email validates an e-mail address of the common form local@host.domain:
// the local part letters, digits, '_', '+', '-' and inner single dots; the
// domain a fully-qualified hostname in any case. Quoted local parts, IP
// literals and names around the address are refused.
func Email(e string) error {
	const msg = "must be an e-mail address like name@example.com"
	local, domain, found := strings.Cut(e, "@")
	if !found || len(e) > 254 || len(local) > 64 || !localPartRE.MatchString(local) ||
		!asciiHostRE.MatchString(domain) || Hostname(strings.ToLower(domain)) != nil {
		return errors.New(msg)
	}
	return nil
}

// Port validates a TCP port.
func Port(p int) error {
	if p < 1 || p > 65535 {
		return errors.New("must be between 1 and 65535")
	}
	return nil
}

// UpstreamURL validates the base URL of an external upstream and returns
// the dial address (host:port) and whether it uses TLS.
//
// Loopback, link-local (including cloud metadata endpoints), unspecified and
// multicast addresses are refused: proxying to them from the edge would
// expose services that are meant to be local-only.
func UpstreamURL(raw string) (dial string, tls bool, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, errors.New("is not a valid URL")
	}
	switch u.Scheme {
	case "http":
	case "https":
		tls = true
	default:
		return "", false, errors.New("scheme must be http or https")
	}
	if u.User != nil {
		return "", false, errors.New("must not contain credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return "", false, errors.New("must not contain a path, query or fragment")
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "80"
		if tls {
			port = "443"
		}
	}
	pn, err := strconv.Atoi(port)
	if err != nil || Port(pn) != nil {
		return "", false, errors.New("has an invalid port")
	}
	if ip, perr := netip.ParseAddr(host); perr == nil {
		ip = ip.Unmap()
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
			return "", false, errors.New("must not target a loopback, link-local, multicast or unspecified address")
		}
		if ip.Zone() != "" {
			return "", false, errors.New("must not contain an IPv6 zone")
		}
	} else {
		if host == "localhost" || strings.HasSuffix(host, ".localhost") {
			return "", false, errors.New("must not target localhost")
		}
		if err := upstreamHostname(host); err != nil {
			return "", false, err
		}
	}
	return net.JoinHostPort(host, port), tls, nil
}

// upstreamHostname accepts single-label names too (e.g. a LAN name "nas").
func upstreamHostname(h string) error {
	if h == "" || len(h) > 253 || h != strings.ToLower(h) {
		return errors.New("has an invalid host")
	}
	for _, l := range strings.Split(h, ".") {
		if !labelRE.MatchString(l) {
			return errors.New("has an invalid host")
		}
	}
	return nil
}

// Duration validates a short Caddy-style duration like "1m" or "30s".
func Duration(d string) error {
	if !durationRE.MatchString(d) {
		return errors.New("must look like 30s, 1m or 1h")
	}
	return nil
}
