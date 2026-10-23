package guard

import (
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// urlRe finds explicit http(s) URLs. The host is parsed with net/url so userinfo
// and ports are not treated as part of the name.
var urlRe = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)

// ipRe finds IPv4 literals. IPv6 is only taken from URLs, where brackets make
// the address unambiguous.
var ipRe = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

// domainRe finds a bare hostname that is its own argument. A slash before the
// name (a file path) does not match.
var domainRe = regexp.MustCompile(`(?:^|[\s"'=])((?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,})(?::\d+)?(?:$|[\s"'/])`)

// NetworkTargets returns the remote hosts a tool call would contact.
// Local file tools and commands with no host return nil, and the scope gate
// leaves those calls alone. Results are lowercase, without port, and de-duplicated.
func NetworkTargets(tool string, input json.RawMessage) []string {
	switch tool {
	case "Bash", "shell_open":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) != nil {
			return nil
		}
		return hostsInText(in.Command)
	case "shell_send":
		var in struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(input, &in) != nil {
			return nil
		}
		return hostsInText(in.Text)
	case "WebFetch":
		var in struct {
			URL string `json:"url"`
			URI string `json:"uri"`
		}
		if json.Unmarshal(input, &in) != nil {
			return nil
		}
		raw := in.URL
		if raw == "" {
			raw = in.URI
		}
		if h := hostOfURL(raw); h != "" {
			return []string{h}
		}
		return hostsInText(raw)
	default:
		return nil
	}
}

func hostsInText(s string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = normalizeTarget(h)
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, m := range urlRe.FindAllString(s, -1) {
		add(hostOfURL(m))
	}
	for _, m := range ipRe.FindAllString(s, -1) {
		if ip := net.ParseIP(m); ip != nil {
			add(ip.String())
		}
	}
	for _, m := range domainRe.FindAllStringSubmatch(s, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	return out
}

func hostOfURL(raw string) string {
	raw = strings.TrimRight(raw, ".,);]")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

func normalizeTarget(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.Trim(host, "[]")
	host = strings.TrimSuffix(host, ".")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host
}
