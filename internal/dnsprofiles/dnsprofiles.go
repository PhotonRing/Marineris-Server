// Package dnsprofiles describes the supported DNS providers, validates their
// credentials, renders them for Caddy's DNS-01 solver and tests them against
// the real provider API.
//
// Every provider here is backed by the same libdns implementation that the
// corresponding caddy-dns module uses, so a successful test exercises
// exactly the code path Caddy will use to solve challenges.
package dnsprofiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/libdns/cloudflare"
	"github.com/libdns/gandi"
	"github.com/libdns/infomaniak"
	"github.com/libdns/libdns"
	"github.com/libdns/ovh"
	"github.com/libdns/route53"

	"github.com/photonring/marineris-server/internal/secrets"
)

// Field describes one credential/setting of a provider.
type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Secret   bool     `json:"secret"`
	Required bool     `json:"required"`
	Default  string   `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"`
	Help     string   `json:"help,omitempty"`
}

// Provider describes a supported DNS provider.
type Provider struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
	// Guide explains how to create least-privilege credentials.
	Guide string `json:"guide"`
}

// Providers lists every supported provider.
var Providers = []Provider{
	{
		ID:   "ovh",
		Name: "OVHcloud",
		Fields: []Field{
			{Key: "endpoint", Label: "API endpoint", Required: true, Default: "ovh-eu",
				Options: []string{"ovh-eu", "ovh-ca", "ovh-us", "kimsufi-eu", "kimsufi-ca", "soyoustart-eu", "soyoustart-ca"}},
			{Key: "application_key", Label: "Application key", Required: true},
			{Key: "application_secret", Label: "Application secret", Required: true, Secret: true},
			{Key: "consumer_key", Label: "Consumer key", Required: true, Secret: true},
		},
		Guide: "Create the token at https://<endpoint>/createToken/ (e.g. https://eu.api.ovh.com/createToken/) and grant ONLY these rights, " +
			"repeated for every zone this profile manages (replace <zone>):\n" +
			"  GET    /domain/zone/<zone>/*\n" +
			"  POST   /domain/zone/<zone>/*\n" +
			"  PUT    /domain/zone/<zone>/*\n" +
			"  DELETE /domain/zone/<zone>/*\n" +
			"No other paths are needed. Use one profile per OVH account; a profile can manage several zones of the same account.",
	},
	{
		ID:   "cloudflare",
		Name: "Cloudflare",
		Fields: []Field{
			{Key: "api_token", Label: "API token", Required: true, Secret: true,
				Help: "Zone.DNS:Edit permission, restricted to the zones this profile manages."},
			{Key: "zone_token", Label: "Zone read token (optional)", Secret: true,
				Help: "Only needed when the API token is limited to specific zones and cannot list them: a token with Zone.Zone:Read on all zones."},
		},
		Guide: "Create an API token (not the global API key) with permission Zone → DNS → Edit, " +
			"and under Zone Resources include only the zones this profile manages.",
	},
	{
		ID:   "gandi",
		Name: "Gandi",
		Fields: []Field{
			{Key: "bearer_token", Label: "Personal access token", Required: true, Secret: true},
		},
		Guide: "Create a personal access token restricted to the organisation and domains this profile manages, " +
			"with only the 'Manage domain name technical configurations' permission.",
	},
	{
		ID:   "infomaniak",
		Name: "Infomaniak",
		Fields: []Field{
			{Key: "api_token", Label: "API token", Required: true, Secret: true},
		},
		Guide: "Create an API token in the Infomaniak manager with only the 'Domain' scope.",
	},
	{
		ID:   "route53",
		Name: "Amazon Route 53",
		Fields: []Field{
			{Key: "access_key_id", Label: "Access key ID", Required: true},
			{Key: "secret_access_key", Label: "Secret access key", Required: true, Secret: true},
			{Key: "region", Label: "Region", Default: "us-east-1"},
			{Key: "hosted_zone_id", Label: "Hosted zone ID (optional)",
				Help: "Set it when the IAM policy does not allow route53:ListHostedZonesByName."},
		},
		Guide: "Create an IAM user whose policy allows route53:ChangeResourceRecordSets and route53:ListResourceRecordSets " +
			"on arn:aws:route53:::hostedzone/<zone-id> only, plus route53:ListHostedZonesByName and route53:GetChange.",
	},
}

// Lookup returns the provider with the given ID.
func Lookup(id string) (*Provider, bool) {
	for i := range Providers {
		if Providers[i].ID == id {
			return &Providers[i], true
		}
	}
	return nil, false
}

// Config holds a profile's provider settings and credentials.
type Config map[string]string

// Normalize validates cfg against the provider and fills defaults. When
// previous is given, empty secret fields keep their previous value, so the
// UI never has to display or resend secrets.
func Normalize(providerID string, cfg, previous Config) (Config, error) {
	p, ok := Lookup(providerID)
	if !ok {
		return nil, fmt.Errorf("unknown DNS provider %q", providerID)
	}
	allowed := map[string]Field{}
	for _, f := range p.Fields {
		allowed[f.Key] = f
	}
	out := Config{}
	for k := range cfg {
		if _, ok := allowed[k]; !ok {
			return nil, fmt.Errorf("unknown field %q for provider %s", k, p.Name)
		}
	}
	for _, f := range p.Fields {
		v := strings.TrimSpace(cfg[f.Key])
		if v == "" && f.Secret && previous != nil {
			v = previous[f.Key]
		}
		if v == "" {
			v = f.Default
		}
		if v == "" {
			if f.Required {
				return nil, fmt.Errorf("%s is required", f.Label)
			}
			continue
		}
		if len(v) > 1024 {
			return nil, fmt.Errorf("%s is too long", f.Label)
		}
		for _, r := range v {
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				return nil, fmt.Errorf("%s contains whitespace or control characters", f.Label)
			}
		}
		if len(f.Options) > 0 && !contains(f.Options, v) {
			return nil, fmt.Errorf("%s must be one of %s", f.Label, strings.Join(f.Options, ", "))
		}
		out[f.Key] = v
	}
	return out, nil
}

// Redacted returns the config with secret values replaced by a marker, safe
// to send to the browser.
func Redacted(providerID string, cfg Config) map[string]any {
	p, ok := Lookup(providerID)
	out := map[string]any{}
	if !ok {
		return out
	}
	for _, f := range p.Fields {
		v, set := cfg[f.Key]
		if f.Secret {
			out[f.Key] = map[string]bool{"set": set && v != ""}
		} else if set {
			out[f.Key] = v
		}
	}
	return out
}

// CaddyProvider renders the "provider" object of Caddy's DNS challenge
// configuration (module dns.providers.<id>).
func CaddyProvider(providerID string, cfg Config) (map[string]any, error) {
	if _, ok := Lookup(providerID); !ok {
		return nil, fmt.Errorf("unknown DNS provider %q", providerID)
	}
	out := map[string]any{"name": providerID}
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = cfg[k]
	}
	return out, nil
}

// Seal encrypts a config for storage in the profile row id.
func Seal(box *secrets.Box, id int64, cfg Config) (string, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return box.Seal(b, SealContext(id))
}

// Open decrypts a stored config.
func Open(box *secrets.Box, id int64, sealed string) (Config, error) {
	b, err := box.Open(sealed, SealContext(id))
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// SealContext is the encryption context of a profile's config.
func SealContext(id int64) string { return fmt.Sprintf("dns-profile:%d", id) }

// Client is the subset of libdns used to test a profile.
type Client interface {
	libdns.RecordAppender
	libdns.RecordDeleter
	libdns.RecordGetter
}

// NewClient builds the libdns client for a profile.
var NewClient = func(providerID string, cfg Config) (Client, error) {
	switch providerID {
	case "ovh":
		return &ovh.Provider{Endpoint: cfg["endpoint"], ApplicationKey: cfg["application_key"], ApplicationSecret: cfg["application_secret"], ConsumerKey: cfg["consumer_key"]}, nil
	case "cloudflare":
		return &cloudflare.Provider{APIToken: cfg["api_token"], ZoneToken: cfg["zone_token"]}, nil
	case "gandi":
		return &gandi.Provider{BearerToken: cfg["bearer_token"]}, nil
	case "infomaniak":
		return &infomaniak.Provider{APIToken: cfg["api_token"]}, nil
	case "route53":
		return &route53.Provider{AccessKeyId: cfg["access_key_id"], SecretAccessKey: cfg["secret_access_key"], Region: cfg["region"], HostedZoneID: cfg["hosted_zone_id"]}, nil
	}
	return nil, fmt.Errorf("unknown DNS provider %q", providerID)
}

// TestRecordName is the relative name of the TXT record created by Test.
const TestRecordName = "_marineris-check"

// TestResult reports the steps of a profile test.
type TestResult struct {
	OK    bool     `json:"ok"`
	Steps []string `json:"steps"`
	Error string   `json:"error,omitempty"`
}

// Test proves that the credentials can create and delete a TXT record in
// zone - the exact operations a DNS-01 challenge needs.
func Test(ctx context.Context, providerID string, cfg Config, zone string) TestResult {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res := TestResult{}
	fail := func(step string, err error) TestResult {
		res.Steps = append(res.Steps, "✗ "+step)
		res.Error = sanitize(err, cfg)
		return res
	}
	client, err := NewClient(providerID, cfg)
	if err != nil {
		return fail("build client", err)
	}
	fqdnZone := strings.TrimSuffix(zone, ".") + "."
	value := "marineris-" + secrets.HexToken(12)
	rec := libdns.TXT{Name: TestRecordName, TTL: 60 * time.Second, Text: value}

	created, err := client.AppendRecords(ctx, fqdnZone, []libdns.Record{rec})
	if err != nil {
		return fail("create TXT "+TestRecordName+"."+zone, err)
	}
	if len(created) == 0 {
		return fail("create TXT "+TestRecordName+"."+zone, errors.New("provider reported no record created"))
	}
	res.Steps = append(res.Steps, "✓ created TXT "+TestRecordName+"."+zone)

	// Always try to clean up, even if the read-back fails.
	defer func() {
		if len(created) == 0 {
			return
		}
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		if _, derr := client.DeleteRecords(cctx, fqdnZone, created); derr != nil && res.OK {
			res.OK = false
			res.Steps = append(res.Steps, "✗ delete test record")
			res.Error = sanitize(derr, cfg)
		}
	}()

	recs, err := client.GetRecords(ctx, fqdnZone)
	if err != nil {
		return fail("read records back", err)
	}
	found := false
	for _, r := range recs {
		rr := r.RR()
		if rr.Type == "TXT" && rr.Name == TestRecordName && strings.Trim(rr.Data, `"`) == value {
			found = true
			break
		}
	}
	if !found {
		return fail("read records back", errors.New("test record not visible in zone"))
	}
	res.Steps = append(res.Steps, "✓ record visible in zone")
	if _, err := client.DeleteRecords(ctx, fqdnZone, created); err != nil {
		return fail("delete test record", err)
	}
	created = nil
	res.Steps = append(res.Steps, "✓ deleted test record")
	res.OK = true
	return res
}

// sanitize removes credential values from provider error messages.
func sanitize(err error, cfg Config) string {
	msg := err.Error()
	for _, v := range cfg {
		if len(v) >= 6 {
			msg = strings.ReplaceAll(msg, v, "[redacted]")
		}
	}
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	return msg
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
