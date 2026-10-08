package main

import "testing"

func TestPolicyIsBlocked(t *testing.T) {
	p := NewPolicy([]string{"# a comment", "", "Evil.com", "*.ads.example.com"})

	tests := []struct {
		host string
		want bool
	}{
		{"evil.com", true},
		{"EVIL.COM", true},               // case-insensitive
		{"evil.com:443", true},           // port is ignored
		{"evil.com.", true},              // trailing dot is ignored
		{"www.evil.com", false},          // exact rule does not cover subdomains
		{"x.ads.example.com", true},      // wildcard matches a subdomain
		{"a.b.ads.example.com:80", true}, // ...and deeper subdomains
		{"ads.example.com", false},       // wildcard does not match the bare domain
		{"notads.example.com", false},    // must match on a dot boundary
		{"example.com", false},           // unrelated host
		{"", false},                      // empty host is not blocked
	}

	for _, tc := range tests {
		if got := p.IsBlocked(tc.host); got != tc.want {
			t.Errorf("IsBlocked(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestPolicyCount(t *testing.T) {
	p := NewPolicy([]string{"# comment", "", "a.com", "*.b.com"})
	if p.Count() != 2 {
		t.Errorf("Count() = %d, want 2", p.Count())
	}
}
