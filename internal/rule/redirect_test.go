package rule

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func setupRedirectChecker(t *testing.T) (*Checker, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)

	c := NewChecker(mr.Addr())
	c.enabled = true

	return c, mr
}

func TestSetAndGetRedirect(t *testing.T) {
	c, mr := setupRedirectChecker(t)
	defer c.Close()

	key := "user:settings:user1:redirects"
	mr.HSet(key, "netflix.com", "10.0.0.1")
	mr.HSet(key, "hulu.com", "10.0.0.2")

	ip, ok := c.GetRedirect("user1", "netflix.com")
	if !ok {
		t.Fatal("expected redirect for netflix.com")
	}
	if ip != "10.0.0.1" {
		t.Errorf("got IP %q, want %q", ip, "10.0.0.1")
	}

	ip, ok = c.GetRedirect("user1", "hulu.com")
	if !ok {
		t.Fatal("expected redirect for hulu.com")
	}
	if ip != "10.0.0.2" {
		t.Errorf("got IP %q, want %q", ip, "10.0.0.2")
	}
}

func TestGetRedirectNoMatch(t *testing.T) {
	c, mr := setupRedirectChecker(t)
	defer c.Close()

	mr.HSet("user:settings:user1:redirects", "netflix.com", "10.0.0.1")

	ip, ok := c.GetRedirect("user1", "google.com")
	if ok {
		t.Errorf("expected no redirect for google.com, got IP %q", ip)
	}
}

func TestGetRedirectWildcard(t *testing.T) {
	c, mr := setupRedirectChecker(t)
	defer c.Close()

	mr.HSet("user:settings:user1:redirects", "*.example.com", "10.0.0.1")
	mr.HSet("user:settings:user1:redirects", "*.net", "10.0.0.2")

	tests := []struct {
		domain string
		wantIP string
	}{
		{"sub.example.com", "10.0.0.1"},
		{"deep.sub.example.com", "10.0.0.1"},
		{"example.com", ""},
		{"test.net", "10.0.0.2"},
		{"sub.test.net", "10.0.0.2"},
		{"google.com", ""},
	}

	for _, tc := range tests {
		ip, ok := c.GetRedirect("user1", tc.domain)
		if tc.wantIP == "" {
			if ok {
				t.Errorf("domain %q: expected no redirect, got IP %q", tc.domain, ip)
			}
			continue
		}
		if !ok {
			t.Errorf("domain %q: expected redirect to %q, got none", tc.domain, tc.wantIP)
		} else if ip != tc.wantIP {
			t.Errorf("domain %q: got IP %q, want %q", tc.domain, ip, tc.wantIP)
		}
	}
}

func TestGetRedirectCaching(t *testing.T) {
	c, mr := setupRedirectChecker(t)
	defer c.Close()

	mr.HSet("user:settings:user1:redirects", "netflix.com", "10.0.0.1")

	ip, ok := c.GetRedirect("user1", "netflix.com")
	if !ok || ip != "10.0.0.1" {
		t.Fatalf("first GetRedirect failed: ok=%v, ip=%q", ok, ip)
	}

	mr.HDel("user:settings:user1:redirects", "netflix.com")

	ip, ok = c.GetRedirect("user1", "netflix.com")
	if !ok || ip != "10.0.0.1" {
		t.Errorf("cached redirect should still be returned after Redis delete: ok=%v, ip=%q", ok, ip)
	}

	c.cache.Delete("redirect:user1:netflix.com")

	ip, ok = c.GetRedirect("user1", "netflix.com")
	if ok {
		t.Errorf("after cache eviction, redirect should not be found, got IP %q", ip)
	}
}

func TestSetRedirect(t *testing.T) {
	c, _ := setupRedirectChecker(t)
	defer c.Close()

	err := c.SetRedirect("user1", "disneyplus.com", "10.0.0.5")
	if err != nil {
		t.Fatalf("SetRedirect failed: %v", err)
	}

	ip, ok := c.GetRedirect("user1", "disneyplus.com")
	if !ok {
		t.Fatal("expected redirect after SetRedirect")
	}
	if ip != "10.0.0.5" {
		t.Errorf("got IP %q, want %q", ip, "10.0.0.5")
	}
}

func TestSetRedirectDomainNormalization(t *testing.T) {
	c, _ := setupRedirectChecker(t)
	defer c.Close()

	err := c.SetRedirect("user1", "DisneyPlus.COM.", "10.0.0.5")
	if err != nil {
		t.Fatalf("SetRedirect failed: %v", err)
	}

	ip, ok := c.GetRedirect("user1", "disneyplus.com")
	if !ok || ip != "10.0.0.5" {
		t.Errorf("redirect should work with normalized domain: ok=%v, ip=%q", ok, ip)
	}
}

func TestRemoveRedirect(t *testing.T) {
	c, mr := setupRedirectChecker(t)
	defer c.Close()

	mr.HSet("user:settings:user1:redirects", "netflix.com", "10.0.0.1")

	c.GetRedirect("user1", "netflix.com")

	err := c.RemoveRedirect("user1", "netflix.com")
	if err != nil {
		t.Fatalf("RemoveRedirect failed: %v", err)
	}

	ip, ok := c.GetRedirect("user1", "netflix.com")
	if ok {
		t.Errorf("redirect should be removed, got IP %q", ip)
	}
}

func TestListRedirects(t *testing.T) {
	c, mr := setupRedirectChecker(t)
	defer c.Close()

	mr.HSet("user:settings:user1:redirects", "a.com", "10.0.0.1")
	mr.HSet("user:settings:user1:redirects", "b.com", "10.0.0.2")

	redirects, err := c.ListRedirects("user1")
	if err != nil {
		t.Fatalf("ListRedirects failed: %v", err)
	}

	if len(redirects) != 2 {
		t.Errorf("expected 2 redirects, got %d", len(redirects))
	}
	if redirects["a.com"] != "10.0.0.1" {
		t.Errorf("a.com: got %q, want %q", redirects["a.com"], "10.0.0.1")
	}
	if redirects["b.com"] != "10.0.0.2" {
		t.Errorf("b.com: got %q, want %q", redirects["b.com"], "10.0.0.2")
	}
}

func TestCheckWithRedirect(t *testing.T) {
	c, mr := setupRedirectChecker(t)
	defer c.Close()

	mr.HSet("user:settings:user1:redirects", "netflix.com", "10.0.0.1")

	ip, ok := c.GetRedirect("user1", "netflix.com")
	if !ok {
		t.Fatal("expected redirect via GetRedirect")
	}
	if ip != "10.0.0.1" {
		t.Errorf("expected RedirectIP 10.0.0.1, got %q", ip)
	}
}

func TestBlockedDomainWithRedirect(t *testing.T) {
	c, mr := setupRedirectChecker(t)
	defer c.Close()

	mr.SAdd("user:settings:user1:blocked", "netflix.com")
	mr.HSet("user:settings:user1:redirects", "netflix.com", "10.0.0.1")

	got := c.Check("user1", "netflix.com")
	if got != 1 {
		t.Errorf("blocked domain should return 1, got %d", got)
	}

	ip, ok := c.GetRedirect("user1", "netflix.com")
	if !ok {
		t.Error("redirect should still exist independently, but GetRedirect returned false")
	}
	if ip != "10.0.0.1" {
		t.Errorf("expected redirect IP 10.0.0.1, got %q", ip)
	}
}
