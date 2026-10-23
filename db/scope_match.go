package db

import (
	"net"
	"strings"
)

// scopeHasHostBoundary reports whether these rows can decide if a network host
// is inside the task. Keyword and ICP rows cannot; company rows can once their
// assets have been expanded into host rows by HostInTaskScope.
func scopeHasHostBoundary(scopes []TaskScope) bool {
	for _, sc := range scopes {
		switch sc.Kind {
		case "root_domain", "subdomain", "ip", "cidr":
			if sc.Domain != "" || sc.Net != "" {
				return true
			}
		}
	}
	return false
}

func normalizeScopeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.Trim(host, "[]")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// hostMatchesScopes reports whether host is inside a host-shaped scope row.
// Loopback is always inside: it is the local machine, not a remote target.
// An empty scope list does not match; callers decide whether that means
// "not enforced" or "denied".
func hostMatchesScopes(host string, scopes []TaskScope) bool {
	host = normalizeScopeHost(host)
	if host == "" || isLoopbackHost(host) {
		return true
	}
	ip := net.ParseIP(host)
	for _, sc := range scopes {
		switch sc.Kind {
		case "root_domain", "subdomain":
			d := DomainKey(sc.Domain)
			if d == "" {
				continue
			}
			if host == d || strings.HasSuffix(host, "."+d) {
				return true
			}
		case "ip", "cidr":
			if ip == nil || sc.Net == "" {
				continue
			}
			_, n, err := net.ParseCIDR(sc.Net)
			if err == nil && n.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// HostInTaskScope reports whether a task's scope constrains host and, when it
// does, whether host is allowed. enforce=false means the task has no host
// boundary yet (no domain, IP, CIDR, or company asset), so the caller should
// let the call through. Loopback is always allowed.
func (s *AssetStore) HostInTaskScope(taskID int64, host string) (enforce, allowed bool, err error) {
	host = normalizeScopeHost(host)
	if host == "" || isLoopbackHost(host) {
		return false, true, nil
	}
	if taskID <= 0 || s == nil || s.db == nil {
		return false, true, nil
	}
	scopes, err := s.ListTaskScope(taskID)
	if err != nil {
		return false, false, err
	}
	extra, err := s.companyHostScopes(scopes)
	if err != nil {
		return false, false, err
	}
	scopes = append(scopes, extra...)
	if !scopeHasHostBoundary(scopes) {
		return false, true, nil
	}
	return true, hostMatchesScopes(host, scopes), nil
}

// companyHostScopes turns company scope rows into host rows from that
// company's assets, so a company boundary can be checked the same way as a
// domain or CIDR row.
func (s *AssetStore) companyHostScopes(scopes []TaskScope) ([]TaskScope, error) {
	seen := map[int64]bool{}
	var ids []int64
	for _, sc := range scopes {
		if sc.Kind != "company" || sc.CompanyID == nil || *sc.CompanyID <= 0 || seen[*sc.CompanyID] {
			continue
		}
		seen[*sc.CompanyID] = true
		ids = append(ids, *sc.CompanyID)
	}
	if len(ids) == 0 || s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
SELECT DISTINCT COALESCE(domain,''), COALESCE(root_domain,''), COALESCE(ip,'')
FROM assets WHERE company_id = ANY($1::bigint[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskScope
	for rows.Next() {
		var domain, root, ip string
		if err := rows.Scan(&domain, &root, &ip); err != nil {
			return nil, err
		}
		if d := DomainKey(domain); d != "" && net.ParseIP(d) == nil {
			out = append(out, TaskScope{Kind: "subdomain", Domain: d})
		}
		if d := DomainKey(root); d != "" && net.ParseIP(d) == nil {
			out = append(out, TaskScope{Kind: "root_domain", Domain: d})
		}
		if c := ipToHostCIDR(ip); c != "" {
			out = append(out, TaskScope{Kind: "ip", Net: c})
		}
	}
	return out, rows.Err()
}
