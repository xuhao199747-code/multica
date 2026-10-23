package agent

import "strings"

// Codex's system proxy discovery can send local gateways through a proxy even
// when the OS bypass list contains wildcard loopback entries. Explicit hosts
// keep local gateways direct without changing remote provider proxy settings.
func codexProxyEnv(env []string) []string {
	out := make([]string, 0, len(env)+2)
	bypass := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if key == "NO_PROXY" || key == "no_proxy" {
			bypass[key] = value
			continue
		}
		out = append(out, entry)
	}
	var hosts []string
	seen := map[string]bool{}
	for _, value := range []string{bypass["NO_PROXY"], bypass["no_proxy"], "localhost,127.0.0.1,::1"} {
		for _, host := range strings.Split(value, ",") {
			host = strings.TrimSpace(host)
			if host != "" && !seen[host] {
				seen[host] = true
				hosts = append(hosts, host)
			}
		}
	}
	value := strings.Join(hosts, ",")
	return append(out, "NO_PROXY="+value, "no_proxy="+value)
}
