package main

import (
	"bufio"
	"net"
	"os"
	"strings"
)

// Policy decides whether a destination host is blocked.
// It is built once at startup and never modified afterwards, so many
// goroutines can read it at the same time without any locking.
type Policy struct {
	exact    map[string]bool // "example.com"
	suffixes []string        // ".example.com", from the rule "*.example.com"
}

// NewPolicy builds a Policy from a list of rule lines.
func NewPolicy(rules []string) *Policy {
	p := &Policy{exact: make(map[string]bool)}
	for _, rule := range rules {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == "" || strings.HasPrefix(rule, "#") {
			continue // skip blank lines and comments
		}
		if strings.HasPrefix(rule, "*.") {
			p.suffixes = append(p.suffixes, rule[1:]) // "*.ads.com" -> ".ads.com"
		} else {
			p.exact[rule] = true
		}
	}
	return p
}

// LoadPolicy reads a blocklist file (one rule per line).
func LoadPolicy(path string) (*Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return NewPolicy(lines), nil
}

// Count returns the number of active rules.
func (p *Policy) Count() int {
	return len(p.exact) + len(p.suffixes)
}

// IsBlocked reports whether the host (with or without a port) is blocked.
func (p *Policy) IsBlocked(hostport string) bool {
	host := normalizeHost(hostport)
	if p.exact[host] {
		return true
	}
	for _, suffix := range p.suffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// normalizeHost removes the port, lower-cases the name and drops a trailing dot,
// so "Example.COM:443" and "example.com." both become "example.com".
func normalizeHost(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
