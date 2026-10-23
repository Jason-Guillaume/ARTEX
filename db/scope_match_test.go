package db

import "testing"

func TestHostMatchesTaskScope(t *testing.T) {
	scopes := []TaskScope{
		{Kind: "root_domain", Domain: "example.com"},
		{Kind: "ip", Net: "203.0.113.10/32"},
		{Kind: "cidr", Net: "198.51.100.0/24"},
	}
	ok := []string{"example.com", "WWW.Example.com", "a.b.example.com", "203.0.113.10", "198.51.100.8", "127.0.0.1", "localhost"}
	for _, h := range ok {
		if !hostMatchesScopes(h, scopes) {
			t.Errorf("host %q should be inside scope", h)
		}
	}
	blocked := []string{"example.org", "notexample.com", "example.com.evil.test", "203.0.113.11", "198.51.100.256", "10.1.1.1"}
	for _, h := range blocked {
		if hostMatchesScopes(h, scopes) {
			t.Errorf("host %q should be outside scope", h)
		}
	}
	if scopeHasHostBoundary([]TaskScope{{Kind: "keyword", Value: "acme"}}) {
		t.Fatal("keyword scope is not a host boundary")
	}
	if !scopeHasHostBoundary(scopes) {
		t.Fatal("domain and cidr rows are a host boundary")
	}
}
