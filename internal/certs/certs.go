// Package certs reports the certificates Caddy actually serves.
//
// Marineris has no access to Caddy's private keys: it inspects certificates
// by performing a TLS handshake against Caddy with the right SNI, exactly as
// a browser would.
package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status describes the certificate served for a host.
type Status struct {
	Host      string    `json:"host"`
	OK        bool      `json:"ok"`
	Issuer    string    `json:"issuer,omitempty"`
	Subject   string    `json:"subject,omitempty"`
	DNSNames  []string  `json:"dns_names,omitempty"`
	NotBefore time.Time `json:"not_before,omitempty"`
	NotAfter  time.Time `json:"not_after,omitempty"`
	DaysLeft  int       `json:"days_left"`
	Staging   bool      `json:"staging"`
	Wildcard  bool      `json:"wildcard"`
	// Trusted is true when the chain verifies against the system roots.
	Trusted bool   `json:"trusted"`
	Error   string `json:"error,omitempty"`
}

// Checker inspects certificates through a TLS endpoint.
type Checker struct {
	// Addr is Caddy's TLS listener (host:port).
	Addr string
	// Roots verifies chains; nil means system roots.
	Roots *x509.CertPool
	Now   func() time.Time
	// Dial is overridable for tests.
	Dial func(ctx context.Context, addr string, cfg *tls.Config) (*tls.Conn, error)
}

// NewChecker returns a checker for Caddy's TLS address.
func NewChecker(addr string) *Checker {
	return &Checker{Addr: addr, Now: time.Now, Dial: dialTLS}
}

func dialTLS(ctx context.Context, addr string, cfg *tls.Config) (*tls.Conn, error) {
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: cfg}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return c.(*tls.Conn), nil
}

// Check inspects one host.
func (c *Checker) Check(ctx context.Context, host string) Status {
	st := Status{Host: host}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// Verification is done manually below so that untrusted (e.g. staging)
	// certificates can still be inspected and reported.
	conn, err := c.Dial(ctx, c.Addr, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, //nolint:gosec // inspection only; the chain is verified explicitly below
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		st.Error = "no certificate served: " + shortErr(err)
		return st
	}
	defer conn.Close()
	chain := conn.ConnectionState().PeerCertificates
	if len(chain) == 0 {
		st.Error = "no certificate served"
		return st
	}
	leaf := chain[0]
	st.Issuer = leaf.Issuer.CommonName
	if len(leaf.Issuer.Organization) > 0 {
		st.Issuer = strings.TrimSpace(leaf.Issuer.Organization[0] + " " + leaf.Issuer.CommonName)
	}
	st.Subject = leaf.Subject.CommonName
	st.DNSNames = leaf.DNSNames
	st.NotBefore, st.NotAfter = leaf.NotBefore, leaf.NotAfter
	now := c.Now()
	st.DaysLeft = int(leaf.NotAfter.Sub(now).Hours() / 24)
	st.Staging = strings.Contains(strings.ToUpper(st.Issuer), "STAGING") || strings.Contains(strings.ToUpper(st.Issuer), "FAKE")
	for _, n := range leaf.DNSNames {
		if strings.HasPrefix(n, "*.") {
			st.Wildcard = true
		}
	}
	inter := x509.NewCertPool()
	for _, ic := range chain[1:] {
		inter.AddCert(ic)
	}
	_, verr := leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: c.Roots, Intermediates: inter, CurrentTime: now})
	st.Trusted = verr == nil
	switch {
	case leaf.VerifyHostname(host) != nil:
		st.Error = "certificate does not cover this host"
	case now.After(leaf.NotAfter):
		st.Error = "certificate expired"
	case !st.Trusted && !st.Staging:
		st.Error = "certificate is not trusted: " + shortErr(verr)
	default:
		st.OK = true
	}
	return st
}

// CheckAll inspects hosts concurrently and returns results sorted by host.
func (c *Checker) CheckAll(ctx context.Context, hosts []string) []Status {
	uniq := map[string]bool{}
	for _, h := range hosts {
		uniq[h] = true
	}
	out := make([]Status, 0, len(uniq))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for h := range uniq {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s := c.Check(ctx, h)
			mu.Lock()
			out = append(out, s)
			mu.Unlock()
		}(h)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

func shortErr(err error) string {
	if err == nil {
		return ""
	}
	var ue x509.UnknownAuthorityError
	if errors.As(err, &ue) {
		return "issued by an unknown authority"
	}
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
