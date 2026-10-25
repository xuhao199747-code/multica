package selfupdate

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func externalHealthClient(base *http.Client, externalURL, resolveIP string) (*http.Client, error) {
	if resolveIP == "" {
		return base, nil
	}
	ip, err := netip.ParseAddr(resolveIP)
	if err != nil {
		return nil, fmt.Errorf("external resolver override must be an IP address")
	}
	target, err := url.Parse(externalURL)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return nil, fmt.Errorf("external resolver requires a credential-free HTTPS URL")
	}
	host := target.Hostname()
	if _, err := netip.ParseAddr(host); err == nil || strings.EqualFold(host, "localhost") {
		return nil, fmt.Errorf("external resolver requires an external hostname")
	}
	if base == nil {
		base = &http.Client{Timeout: 15 * time.Second}
	}
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	httpTransport, ok := transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("external resolver requires an HTTP transport")
	}
	external := httpTransport.Clone()
	// A proxy or custom TLS dialer could bypass the pinned socket destination.
	external.Proxy = nil
	external.DialTLS = nil
	external.DialTLSContext = nil
	if external.TLSClientConfig == nil {
		external.TLSClientConfig = &tls.Config{}
	}
	external.TLSClientConfig.ServerName = host
	external.TLSClientConfig.InsecureSkipVerify = false
	address := healthAddress(target)
	_, port, _ := net.SplitHostPort(address)
	dial := external.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 15 * time.Second}).DialContext
	}
	external.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != address {
			return nil, fmt.Errorf("unexpected external health address")
		}
		return dial(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	client := *base
	client.Transport = &externalHealthTransport{base: transport, external: external, host: host, address: address}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client, nil
}

func healthAddress(target *url.URL) string {
	port := target.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(target.Hostname(), port)
}

type externalHealthTransport struct {
	base     http.RoundTripper
	external *http.Transport
	host     string
	address  string
}

func (t *externalHealthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || healthAddress(req.URL) != t.address {
		return nil, fmt.Errorf("unexpected external health URL")
	}
	return t.external.RoundTrip(req)
}

func VerifyDeployment(ctx context.Context, client *http.Client, e DeployEvidence) error {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	// Redirects must not make a failing deployment look healthy at another URL.
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if bounded.Timeout == 0 {
		bounded.Timeout = 15 * time.Second
	}
	local := bounded
	if transport, ok := bounded.Transport.(*externalHealthTransport); ok {
		local.Transport = transport.base
		defer transport.external.CloseIdleConnections()
		for _, rawURL := range []string{e.ReadyURL, e.LocalURL} {
			u, err := url.Parse(rawURL)
			if err != nil || strings.EqualFold(u.Hostname(), transport.host) {
				return fmt.Errorf("external resolver cannot be used for local health URLs")
			}
		}
	}
	for _, target := range []struct {
		url    string
		ready  bool
		client *http.Client
	}{{e.ReadyURL, true, &local}, {e.LocalURL, false, &local}, {e.ExternalURL, false, &bounded}} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.url, nil)
		if err != nil {
			return fmt.Errorf("invalid deployment health URL")
		}
		response, err := target.client.Do(req)
		if err != nil {
			return fmt.Errorf("deployment health request failed")
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return fmt.Errorf("deployment health returned non-200")
		}
		if target.ready {
			var body struct {
				Checks struct {
					DB         string `json:"db"`
					Migrations string `json:"migrations"`
				} `json:"checks"`
			}
			dec := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
			err = dec.Decode(&body)
			var extra any
			if err != nil || dec.Decode(&extra) != io.EOF || body.Checks.DB != "ok" || body.Checks.Migrations != "ok" {
				response.Body.Close()
				return fmt.Errorf("deployment database and migrations are not ready")
			}
		}
		response.Body.Close()
	}
	return nil
}
