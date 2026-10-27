package dnsprofiles

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/libdns/libdns"

	"github.com/photonring/marineris-server/internal/secrets"
)

func TestEveryProviderHasCaddyModuleShape(t *testing.T) {
	want := map[string]bool{"ovh": true, "cloudflare": true, "gandi": true, "infomaniak": true, "route53": true}
	for _, p := range Providers {
		if !want[p.ID] {
			t.Errorf("unexpected provider %s", p.ID)
		}
		delete(want, p.ID)
		if p.Guide == "" {
			t.Errorf("%s: missing least-privilege guide", p.ID)
		}
		secret := false
		for _, f := range p.Fields {
			secret = secret || f.Secret
		}
		if !secret {
			t.Errorf("%s: no field marked secret", p.ID)
		}
		if _, err := NewClient(p.ID, Config{}); err != nil {
			t.Errorf("%s: no libdns client: %v", p.ID, err)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing providers: %v", want)
	}
}

func TestNormalize(t *testing.T) {
	cfg, err := Normalize("ovh", Config{"application_key": "ak", "application_secret": "as", "consumer_key": "ck"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["endpoint"] != "ovh-eu" {
		t.Fatal("default not applied")
	}
	bad := []Config{
		{"application_key": "ak", "application_secret": "as"},                                                       // missing
		{"application_key": "ak", "application_secret": "as", "consumer_key": "ck", "extra": "x"},                   // unknown key
		{"application_key": "a k", "application_secret": "as", "consumer_key": "ck"},                                // whitespace
		{"application_key": "ak\n", "application_secret": "as\"}", "consumer_key": "c\x00k"},                        // control chars
		{"endpoint": "evil.example.com", "application_key": "ak", "application_secret": "as", "consumer_key": "ck"}, // not an option
		{"application_key": strings.Repeat("a", 2000), "application_secret": "as", "consumer_key": "ck"},
	}
	for i, c := range bad {
		if _, err := Normalize("ovh", c, nil); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if _, err := Normalize("nope", Config{}, nil); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

func TestNormalizeKeepsSecretsOnEdit(t *testing.T) {
	prev := Config{"api_token": "old-secret-token"}
	cfg, err := Normalize("cloudflare", Config{"api_token": ""}, prev)
	if err != nil || cfg["api_token"] != "old-secret-token" {
		t.Fatalf("%v %v", cfg, err)
	}
	cfg, _ = Normalize("cloudflare", Config{"api_token": "new-token"}, prev)
	if cfg["api_token"] != "new-token" {
		t.Fatal("secret not replaced")
	}
}

func TestRedactedNeverLeaksSecrets(t *testing.T) {
	cfg := Config{"endpoint": "ovh-eu", "application_key": "ak", "application_secret": "TOPSECRET1", "consumer_key": "TOPSECRET2"}
	r := Redacted("ovh", cfg)
	s := strings.Join(func() []string {
		var out []string
		for k, v := range r {
			out = append(out, k, strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(fmtAny(v), "\n", ""), " ", "")))
		}
		return out
	}(), ",")
	if strings.Contains(s, "TOPSECRET") {
		t.Fatalf("secret leaked: %s", s)
	}
	if r["endpoint"] != "ovh-eu" {
		t.Fatal("non-secret field missing")
	}
}

func fmtAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]bool:
		if x["set"] {
			return "set"
		}
		return "unset"
	}
	return ""
}

func TestCaddyProvider(t *testing.T) {
	m, err := CaddyProvider("cloudflare", Config{"api_token": "t"})
	if err != nil || m["name"] != "cloudflare" || m["api_token"] != "t" {
		t.Fatalf("%v %v", m, err)
	}
}

func TestSealBoundToProfile(t *testing.T) {
	box, _ := secrets.NewBox(bytes.Repeat([]byte{9}, 32))
	sealed, err := Seal(box, 1, Config{"api_token": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(box, 2, sealed); err == nil {
		t.Fatal("config of profile 1 opened as profile 2")
	}
	cfg, err := Open(box, 1, sealed)
	if err != nil || cfg["api_token"] != "x" {
		t.Fatal(err)
	}
}

type fakeDNS struct {
	mu          sync.Mutex
	records     map[string][]libdns.Record
	failAppend  error
	failDelete  error
	hideRecords bool
	deletes     int
}

func (f *fakeDNS) AppendRecords(_ context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	if f.failAppend != nil {
		return nil, f.failAppend
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[zone] = append(f.records[zone], recs...)
	return recs, nil
}

func (f *fakeDNS) DeleteRecords(_ context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	f.deletes++
	if f.failDelete != nil {
		return nil, f.failDelete
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[zone] = nil
	return recs, nil
}

func (f *fakeDNS) GetRecords(_ context.Context, zone string) ([]libdns.Record, error) {
	if f.hideRecords {
		return nil, nil
	}
	return f.records[zone], nil
}

func withFake(t *testing.T, f *fakeDNS) {
	old := NewClient
	NewClient = func(string, Config) (Client, error) { return f, nil }
	t.Cleanup(func() { NewClient = old })
}

func TestTestSuccess(t *testing.T) {
	f := &fakeDNS{records: map[string][]libdns.Record{}}
	withFake(t, f)
	res := Test(context.Background(), "ovh", Config{}, "example.com")
	if !res.OK || len(res.Steps) != 3 {
		t.Fatalf("%+v", res)
	}
	if len(f.records["example.com."]) != 0 || f.deletes != 1 {
		t.Fatal("test record not cleaned up")
	}
}

func TestTestFailuresCleanUpAndRedact(t *testing.T) {
	f := &fakeDNS{records: map[string][]libdns.Record{}, failAppend: errors.New("403 forbidden for key SUPERSECRETKEY")}
	withFake(t, f)
	res := Test(context.Background(), "ovh", Config{"application_secret": "SUPERSECRETKEY"}, "example.com")
	if res.OK || strings.Contains(res.Error, "SUPERSECRETKEY") || !strings.Contains(res.Error, "[redacted]") {
		t.Fatalf("%+v", res)
	}
	f2 := &fakeDNS{records: map[string][]libdns.Record{}, hideRecords: true}
	withFake(t, f2)
	res = Test(context.Background(), "ovh", Config{}, "example.com")
	if res.OK || f2.deletes != 1 {
		t.Fatalf("record must be cleaned up after a failed read-back: %+v deletes=%d", res, f2.deletes)
	}
}
