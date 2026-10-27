// Command gencerts creates the throw-away PKI used by the integration
// tests: a root CA and a TLS certificate for the Pebble ACME server.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: gencerts <dir>")
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Marineris integration test root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(7 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		log.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "pebble"},
		DNSNames: []string{"pebble", "localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(7 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		log.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	write := func(name, typ string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o644); err != nil {
			log.Fatal(err)
		}
	}
	write("root.pem", "CERTIFICATE", caDER)
	write("pebble.pem", "CERTIFICATE", der)
	write("pebble.key", "EC PRIVATE KEY", keyDER)
	cfg := `{
  "pebble": {
    "listenAddress": "0.0.0.0:14000",
    "managementListenAddress": "0.0.0.0:15000",
    "certificate": "/certs/pebble.pem",
    "privateKey": "/certs/pebble.key",
    "httpPort": 80,
    "tlsPort": 443,
    "ocspResponderURL": "",
    "externalAccountBindingRequired": false,
    "retryAfter": {"authz": 1, "order": 1}
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "pebble.json"), []byte(cfg), 0o644); err != nil {
		log.Fatal(err)
	}
}
