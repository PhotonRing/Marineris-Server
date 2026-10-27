// Package icons validates and fetches the images shown on hub tiles.
//
// Icons are stored in the database and served from Marineris itself, so the
// UI never loads third-party images (the CSP only allows 'self' and data:).
// Remote icons are fetched once, server-side, with SSRF protection: HTTPS
// only, and every connection (including redirects) must go to a public IP.
package icons

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// MaxSize bounds an icon.
const MaxSize = 512 << 10

// DashboardIconsURL is the CDN of the open-source dashboard-icons set
// (https://github.com/homarr-labs/dashboard-icons), used for icon names.
const DashboardIconsURL = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/png/%s.png"

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ErrInvalid is returned for data that is not an accepted image.
var ErrInvalid = errors.New("icons: not a PNG, JPEG, WebP, GIF, ICO or SVG image (max 512 KB)")

// Detect returns the content type of an accepted image, sniffed from the
// data (never trusted from the client).
func Detect(data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaxSize {
		return "", ErrInvalid
	}
	switch ct := http.DetectContentType(data); ct {
	case "image/png", "image/jpeg", "image/webp", "image/gif", "image/x-icon", "image/vnd.microsoft.icon":
		return ct, nil
	}
	if isSVG(data) {
		return "image/svg+xml", nil
	}
	return "", ErrInvalid
}

func isSVG(data []byte) bool {
	head := bytes.ToLower(data[:min(len(data), 4096)])
	head = bytes.TrimSpace(bytes.TrimPrefix(head, []byte("\xef\xbb\xbf")))
	starts := bytes.HasPrefix(head, []byte("<svg")) || bytes.HasPrefix(head, []byte("<?xml")) || bytes.HasPrefix(head, []byte("<!--"))
	return starts && bytes.Contains(head, []byte("<svg"))
}

// SlugURL returns the dashboard-icons URL of an icon name ("jellyfin").
func SlugURL(slug string) (string, error) {
	if !slugRE.MatchString(slug) {
		return "", errors.New("icons: an icon name uses lowercase letters, digits and '-'")
	}
	return fmt.Sprintf(DashboardIconsURL, slug), nil
}

// SuggestSlug guesses an icon name from a container image
// ("lscr.io/linuxserver/jellyfin:latest" → "jellyfin").
func SuggestSlug(image string) string {
	if i := strings.LastIndex(image, "/"); i >= 0 {
		image = image[i+1:]
	}
	image, _, _ = strings.Cut(image, "@")
	image, _, _ = strings.Cut(image, ":")
	image = strings.ToLower(image)
	if slugRE.MatchString(image) {
		return image
	}
	return ""
}

// Fetcher downloads icons.
type Fetcher struct {
	Client *http.Client
}

// publicIP reports whether ip may be contacted.
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("240.0.0.0/4"),
	} {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// NewFetcher returns a fetcher whose connections may only reach public IP
// addresses (checked after DNS resolution, so DNS rebinding does not help).
func NewFetcher() *Fetcher {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !publicIP(ip) {
				return fmt.Errorf("icons: refusing to connect to non-public address %s", host)
			}
			return nil
		},
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          2,
	}
	return &Fetcher{Client: &http.Client{
		Transport: tr,
		Timeout:   15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("icons: too many redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("icons: redirect to a non-HTTPS URL")
			}
			return nil
		},
	}}
}

// Fetch downloads and validates an icon from an https URL.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (string, []byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", nil, errors.New("icons: the icon URL must be an https:// URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "image/*")
	resp, err := f.Client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("icons: download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("icons: download failed: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxSize+1))
	if err != nil {
		return "", nil, err
	}
	ct, err := Detect(data)
	if err != nil {
		return "", nil, err
	}
	return ct, data, nil
}
