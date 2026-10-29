package validate

import (
	"strings"
	"testing"
	"unicode"
)

func TestHostname(t *testing.T) {
	good := []string{"example.com", "app.example.com", "a-b.c-d.example.org", "xn--mrs-qla.example.com", "x1.io"}
	bad := []string{
		"", "localhost", "Example.com", "-a.example.com", "a-.example.com", "a..b.com",
		"*.example.com", "example.com.", "1.2.3.4", "::1", "app.example.123",
		"a b.com", "a_b.example.com", "app.example.com/x", "app.example.com:443",
		"app.example.com\n", "\"quoted\".com", strings.Repeat("a", 64) + ".com",
		strings.Repeat("abcdefghi.", 26) + "com",
	}
	for _, h := range good {
		if err := Hostname(h); err != nil {
			t.Errorf("%q rejected: %v", h, err)
		}
	}
	for _, h := range bad {
		if err := Hostname(h); err == nil {
			t.Errorf("%q accepted", h)
		}
	}
}

func TestHostInZone(t *testing.T) {
	if !HostInZone("a.example.com", "example.com") || !HostInZone("example.com", "example.com") {
		t.Fatal("valid host rejected")
	}
	if HostInZone("evilexample.com", "example.com") || HostInZone("example.com.evil.org", "example.com") {
		t.Fatal("suffix confusion")
	}
	if !IsDirectChild("a.example.com", "example.com") || IsDirectChild("a.b.example.com", "example.com") || IsDirectChild("example.com", "example.com") {
		t.Fatal("IsDirectChild wrong")
	}
}

func TestPathPrefix(t *testing.T) {
	for _, p := range []string{"", "/api", "/a/b-c/d_e.f~"} {
		if err := PathPrefix(p); err != nil {
			t.Errorf("%q rejected: %v", p, err)
		}
	}
	for _, p := range []string{"/", "api", "/a/../b", "/a/./b", "//a", "/a/", "/a*", "/a b", "/a%2e%2e", "/a\"}", "/{http.request.uri}"} {
		if err := PathPrefix(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestNames(t *testing.T) {
	if ContainerName("my-app_1.web") != nil || ContainerName("/web") != nil {
		t.Fatal("valid container rejected")
	}
	for _, n := range []string{"", "-x", "a b", "a/b", "a;b", "a\nb"} {
		if ContainerName(n) == nil {
			t.Errorf("container %q accepted", n)
		}
	}
	if StackName("media") != nil || StackName("Media") == nil || StackName("a.b") == nil {
		t.Fatal("stack name rules")
	}
	for _, f := range []string{"..", ".", "a/b", "../etc", "a..b", ".hidden"} {
		if FolderName(f) == nil {
			t.Errorf("folder %q accepted", f)
		}
	}
	if FolderName("site") != nil || FolderName("my-site.v2") != nil {
		t.Fatal("valid folder rejected")
	}
	if DisplayName("OVH account (main)") != nil || DisplayName("Économie 2") != nil {
		t.Fatal("valid display name rejected")
	}
	for _, n := range []string{"", " lead", "trail ", "<script>", "a\"b", "O'Brien", "a\nb", strings.Repeat("x", 65)} {
		if DisplayName(n) == nil {
			t.Errorf("display name %q accepted", n)
		}
	}
	for _, n := range []string{"O'Brien", "D’Artagnan", "Théo", "Jean-Luc (home)"} {
		if err := UserDisplayName(n); err != nil {
			t.Errorf("User display name %q rejected: %v", n, err)
		}
	}
	for _, n := range []string{"", "'lead", " lead", "trail ", "<script>", "a\"b", "a\nb", strings.Repeat("x", 65)} {
		if UserDisplayName(n) == nil {
			t.Errorf("User display name %q accepted", n)
		}
	}
	for _, g := range []string{"family", "f2", "team_a-b", "1st", strings.Repeat("a", 32)} {
		if err := GroupName(g); err != nil {
			t.Errorf("group %q rejected: %v", g, err)
		}
	}
	for _, g := range []string{"", "a", "Family", "a b", "a.b", "-x", "_x", "été", "a\n", strings.Repeat("a", 33), "admins", "proxy-admins"} {
		if GroupName(g) == nil {
			t.Errorf("group %q accepted", g)
		}
	}
	for _, u := range []string{"dave", "jo", "j.doe", "a_b-c", "1st", strings.Repeat("a", 32)} {
		if err := Username(u); err != nil {
			t.Errorf("username %q rejected: %v", u, err)
		}
	}
	for _, u := range []string{"", "a", "Dave", "a b", ".x", "-x", "_x", "été", "a\n", "a:b", "a@b", strings.Repeat("a", 33)} {
		if Username(u) == nil {
			t.Errorf("username %q accepted", u)
		}
	}
}

func TestEmail(t *testing.T) {
	for _, e := range []string{"dave@example.com", "j.doe+home@mail.example.org", "A_B-c@Example.COM"} {
		if err := Email(e); err != nil {
			t.Errorf("e-mail %q rejected: %v", e, err)
		}
	}
	for _, e := range []string{"", "dave", "@example.com", "dave@", "dave@localhost", "a b@x.com", "a..b@x.com", ".a@x.com",
		"a.@x.com", "a@b@x.com", `"a"@x.com`, "a'b@x.com", "a{b}@x.com", "a@x.com\n", "Dave <dave@x.com>", "a@1.2.3.4",
		strings.Repeat("a", 65) + "@x.com", "a@" + strings.Repeat("b", 250) + ".com"} {
		if Email(e) == nil {
			t.Errorf("e-mail %q accepted", e)
		}
	}
}

func TestUpstreamURL(t *testing.T) {
	cases := []struct {
		in   string
		dial string
		tls  bool
	}{
		{"http://192.168.1.10:8080", "192.168.1.10:8080", false},
		{"https://nas.lan", "nas.lan:443", true},
		{"http://nas", "nas:80", false},
		{"https://[fd00::1]:8443/", "[fd00::1]:8443", true},
	}
	for _, c := range cases {
		dial, tls, err := UpstreamURL(c.in)
		if err != nil || dial != c.dial || tls != c.tls {
			t.Errorf("%s: got %s %v %v", c.in, dial, tls, err)
		}
	}
	for _, bad := range []string{
		"ftp://x", "http://user:pw@host", "http://127.0.0.1", "http://[::1]", "http://localhost:80",
		"http://a.localhost", "http://169.254.169.254", "http://0.0.0.0", "http://224.0.0.1",
		"http://host/path", "http://host?q=1", "http://host:0", "http://host:99999", "http://[fe80::1%25eth0]",
		"http://[::ffff:127.0.0.1]", "javascript:alert(1)", "http://HOST", "http://ho st",
	} {
		if _, _, err := UpstreamURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDuration(t *testing.T) {
	for _, d := range []string{"1m", "30s", "1h", "500ms"} {
		if Duration(d) != nil {
			t.Errorf("%q rejected", d)
		}
	}
	for _, d := range []string{"", "0s", "-1m", "1d", "1m30s", "m", "999999s"} {
		if Duration(d) == nil {
			t.Errorf("%q accepted", d)
		}
	}
}

// Anything accepted by the validators must be free of characters that could
// alter the structure of JSON, YAML or Caddy placeholders.
func assertInert(t *testing.T, s string) {
	t.Helper()
	if strings.ContainsAny(s, "\"'{}$\\\n\r\t` :#&*!|>%@[],?") {
		t.Fatalf("validated value contains structural character: %q", s)
	}
}

func FuzzHostname(f *testing.F) {
	for _, s := range []string{"example.com", "a.b", "*.x.y", "a..b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if Hostname(s) == nil {
			assertInert(t, s)
		}
	})
}

func FuzzPathPrefix(f *testing.F) {
	f.Add("/api")
	f.Add("/a/../b")
	f.Fuzz(func(t *testing.T, s string) {
		if PathPrefix(s) == nil && s != "" {
			assertInert(t, strings.ReplaceAll(s, "/", ""))
			if strings.Contains(s, "/../") || strings.HasSuffix(s, "/..") {
				t.Fatalf("traversal accepted: %q", s)
			}
		}
	})
}

func FuzzFolderName(f *testing.F) {
	f.Add("site")
	f.Add("..")
	f.Fuzz(func(t *testing.T, s string) {
		if FolderName(s) == nil {
			assertInert(t, s)
			if strings.Contains(s, "/") || strings.Contains(s, "..") {
				t.Fatalf("unsafe folder accepted: %q", s)
			}
		}
	})
}

func FuzzGroupName(f *testing.F) {
	f.Add("family")
	f.Add("admins")
	f.Fuzz(func(t *testing.T, s string) {
		if GroupName(s) == nil {
			assertInert(t, s)
			if s == "admins" || s == "proxy-admins" {
				t.Fatalf("built-in Group accepted: %q", s)
			}
		}
	})
}

func FuzzUsername(f *testing.F) {
	f.Add("dave")
	f.Add("j.doe")
	f.Add("a\nb")
	f.Fuzz(func(t *testing.T, s string) {
		if Username(s) == nil {
			assertInert(t, s)
			if s != strings.ToLower(s) {
				t.Fatalf("uppercase Username accepted: %q", s)
			}
		}
	})
}

func FuzzUserDisplayName(f *testing.F) {
	f.Add("O'Brien")
	f.Add("D’Artagnan")
	f.Add("a\"b")
	f.Fuzz(func(t *testing.T, s string) {
		if UserDisplayName(s) == nil {
			// Apostrophes are the one addition to DisplayName's characters.
			assertInert(t, strings.NewReplacer(" ", "", "'", "").Replace(s))
			if s != strings.TrimSpace(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
				t.Fatalf("unsafe User display name accepted: %q", s)
			}
		}
	})
}

func FuzzDisplayName(f *testing.F) {
	f.Add("Dave Doe")
	f.Add("Économie (2)")
	f.Add("a\"b")
	f.Fuzz(func(t *testing.T, s string) {
		if DisplayName(s) == nil {
			// Spaces are allowed inside, never around.
			assertInert(t, strings.ReplaceAll(s, " ", ""))
			if s != strings.TrimSpace(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
				t.Fatalf("unsafe display name accepted: %q", s)
			}
		}
	})
}

func FuzzEmail(f *testing.F) {
	f.Add("dave@example.com")
	f.Add(`"a b"@x.com`)
	f.Add("Dave <dave@x.com>")
	f.Fuzz(func(t *testing.T, s string) {
		if Email(s) == nil {
			if strings.Count(s, "@") != 1 {
				t.Fatalf("e-mail with %d '@' accepted: %q", strings.Count(s, "@"), s)
			}
			assertInert(t, strings.Replace(s, "@", "", 1))
		}
	})
}

func FuzzUpstreamURL(f *testing.F) {
	f.Add("http://192.168.1.1:80")
	f.Add("http://127.0.0.1")
	f.Fuzz(func(t *testing.T, s string) {
		dial, _, err := UpstreamURL(s)
		if err == nil && strings.ContainsAny(dial, "\"'{}$\\\n\r/ @") {
			t.Fatalf("unsafe dial address %q from %q", dial, s)
		}
	})
}
