package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

type pki struct {
	root    *x509.Certificate
	rootKey *ecdsa.PrivateKey
	pool    *x509.CertPool
}

func newPKI(t *testing.T, cn string) *pki {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn, Organization: []string{"Test"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return &pki{root: root, rootKey: key, pool: pool}
}

func (p *pki) leaf(t *testing.T, names []string, notAfter time.Time) tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: names[0]},
		DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.root, &key.PublicKey, p.rootKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func serve(t *testing.T, cert tls.Certificate) string {
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { _ = c.(*tls.Conn).Handshake(); c.Close() }(c)
		}
	}()
	return ln.Addr().String()
}

func TestTrustedWildcard(t *testing.T) {
	p := newPKI(t, "Root")
	addr := serve(t, p.leaf(t, []string{"*.example.com", "example.com"}, time.Now().Add(60*24*time.Hour)))
	c := NewChecker(addr)
	c.Roots = p.pool
	st := c.Check(context.Background(), "app.example.com")
	if !st.OK || !st.Trusted || !st.Wildcard || st.Staging || st.DaysLeft < 58 {
		t.Fatalf("%+v", st)
	}
}

func TestStagingIsReportedButAccepted(t *testing.T) {
	p := newPKI(t, "(STAGING) Pretend Pear X1")
	addr := serve(t, p.leaf(t, []string{"app.example.com"}, time.Now().Add(24*time.Hour)))
	st := NewChecker(addr).Check(context.Background(), "app.example.com") // system roots: untrusted
	if !st.OK || st.Trusted || !st.Staging {
		t.Fatalf("%+v", st)
	}
}

func TestWrongHostAndUntrusted(t *testing.T) {
	p := newPKI(t, "Root")
	addr := serve(t, p.leaf(t, []string{"other.example.com"}, time.Now().Add(24*time.Hour)))
	st := NewChecker(addr).Check(context.Background(), "app.example.com")
	if st.OK || st.Error == "" {
		t.Fatalf("%+v", st)
	}
}

func TestUnreachable(t *testing.T) {
	st := NewChecker("127.0.0.1:1").Check(context.Background(), "app.example.com")
	if st.OK || st.Error == "" {
		t.Fatalf("%+v", st)
	}
}

func TestCheckAllDedupesAndSorts(t *testing.T) {
	p := newPKI(t, "Root")
	addr := serve(t, p.leaf(t, []string{"*.example.com"}, time.Now().Add(24*time.Hour)))
	c := NewChecker(addr)
	c.Roots = p.pool
	out := c.CheckAll(context.Background(), []string{"b.example.com", "a.example.com", "b.example.com"})
	if len(out) != 2 || out[0].Host != "a.example.com" {
		t.Fatalf("%+v", out)
	}
}
