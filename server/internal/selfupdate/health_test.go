package selfupdate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestVerifyDeployment(t *testing.T) {
	for _, mode := range []string{"success", "db missing", "migrations missing", "bad ready", "local failed", "external failed", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/readyz":
					switch mode {
					case "db missing":
						w.Write([]byte(`{"checks":{"migrations":"ok"}}`))
					case "migrations missing":
						w.Write([]byte(`{"checks":{"db":"ok"}}`))
					case "bad ready":
						w.WriteHeader(503)
					case "malformed":
						w.Write([]byte(`{`))
					default:
						w.Write([]byte(`{"checks":{"db":"ok","migrations":"ok"}}`))
					}
				case "/local":
					if mode == "local failed" {
						w.WriteHeader(503)
					}
				case "/external":
					if mode == "external failed" {
						w.WriteHeader(503)
					}
				}
			}))
			defer s.Close()
			err := VerifyDeployment(context.Background(), s.Client(), DeployEvidence{ReadyURL: s.URL + "/readyz", LocalURL: s.URL + "/local", ExternalURL: s.URL + "/external"})
			if (err != nil) != (mode != "success") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func externalTLSFixture(t *testing.T, certificateHost string) (*httptest.Server, *http.Client) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{certificateHost},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Observed-Host", r.Host)
		if r.URL.Path == "/readyz" {
			_, _ = w.Write([]byte(`{"checks":{"db":"ok","migrations":"ok"}}`))
		}
		if r.Host == "funnel.example.test" && r.TLS.ServerName != "funnel.example.test" {
			t.Error("external TLS did not preserve the original host")
		}
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "127.0.0.1:443" {
			address = server.Listener.Addr().String()
		} else if address != server.Listener.Addr().String() {
			return nil, &net.AddrError{Err: "unexpected dial target", Addr: address}
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	t.Cleanup(transport.CloseIdleConnections)
	return server, &http.Client{Transport: transport, Timeout: time.Second}
}

func TestVerifyDeploymentExternalResolveIP(t *testing.T) {
	server, base := externalTLSFixture(t, "funnel.example.test")
	e := DeployEvidence{ReadyURL: server.URL + "/readyz", LocalURL: server.URL + "/", ExternalURL: "https://funnel.example.test/"}
	client, err := externalHealthClient(base, e.ExternalURL, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDeployment(context.Background(), client, e); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(e.ExternalURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("X-Observed-Host") != "funnel.example.test" {
		t.Fatal("external HTTP Host was rewritten")
	}
	if base.Transport.(*http.Transport).TLSClientConfig.ServerName != "" {
		t.Fatal("base TLS transport was mutated")
	}
}

func TestVerifyDeploymentExternalRejectsCertificateMismatch(t *testing.T) {
	server, base := externalTLSFixture(t, "wrong.example.test")
	base.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true
	client, err := externalHealthClient(base, "https://funnel.example.test/", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	e := DeployEvidence{ReadyURL: server.URL + "/readyz", LocalURL: server.URL + "/", ExternalURL: "https://funnel.example.test/"}
	if err := VerifyDeployment(context.Background(), client, e); err == nil {
		t.Fatal("accepted a certificate for another external host")
	}
}

func TestVerifyDeploymentExternalPreservesExplicitPort(t *testing.T) {
	server, base := externalTLSFixture(t, "funnel.example.test")
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	external := "https://funnel.example.test:" + port + "/"
	client, err := externalHealthClient(base, external, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDeployment(context.Background(), client, DeployEvidence{ReadyURL: server.URL + "/readyz", LocalURL: server.URL, ExternalURL: external}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyDeploymentExternalBypassesProxyOnlyForExternal(t *testing.T) {
	server, base := externalTLSFixture(t, "funnel.example.test")
	var proxyCalls []string
	base.Transport.(*http.Transport).Proxy = func(req *http.Request) (*url.URL, error) {
		proxyCalls = append(proxyCalls, req.URL.Hostname())
		if req.URL.Hostname() == "funnel.example.test" {
			return nil, fmt.Errorf("external proxy must not be consulted")
		}
		return nil, nil
	}
	client, err := externalHealthClient(base, "https://funnel.example.test/", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDeployment(context.Background(), client, DeployEvidence{ReadyURL: server.URL + "/readyz", LocalURL: server.URL, ExternalURL: "https://funnel.example.test/"}); err != nil {
		t.Fatal(err)
	}
	if len(proxyCalls) != 2 || proxyCalls[0] != "127.0.0.1" || proxyCalls[1] != "127.0.0.1" {
		t.Fatalf("local proxy handling changed: %v", proxyCalls)
	}
}

func TestVerifyDeploymentExternalCannotRewriteLocalURLs(t *testing.T) {
	server, base := externalTLSFixture(t, "funnel.example.test")
	client, err := externalHealthClient(base, "https://funnel.example.test/", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, local := range []string{"ready", "frontend"} {
		t.Run(local, func(t *testing.T) {
			e := DeployEvidence{ReadyURL: server.URL + "/readyz", LocalURL: server.URL + "/", ExternalURL: "https://funnel.example.test/"}
			if local == "ready" {
				e.ReadyURL = "https://funnel.example.test/readyz"
			} else {
				e.LocalURL = "https://funnel.example.test/"
			}
			if err := VerifyDeployment(context.Background(), client, e); err == nil {
				t.Fatal("resolver override rewrote a non-external health URL")
			}
		})
	}
}

func TestVerifyDeploymentExternalRejectsUnexpectedTarget(t *testing.T) {
	server, base := externalTLSFixture(t, "funnel.example.test")
	for _, target := range []string{"https://other.example.test/", "https://funnel.example.test:444/", "http://funnel.example.test/"} {
		t.Run(target, func(t *testing.T) {
			client, err := externalHealthClient(base, "https://funnel.example.test/", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			e := DeployEvidence{ReadyURL: server.URL + "/readyz", LocalURL: server.URL + "/", ExternalURL: target}
			if err := VerifyDeployment(context.Background(), client, e); err == nil {
				t.Fatal("accepted an unexpected external target")
			}
		})
	}
	for _, target := range []string{server.URL, "http://funnel.example.test/", "https://user@funnel.example.test/", "not-a-url"} {
		if _, err := externalHealthClient(base, target, "127.0.0.1"); err == nil {
			t.Fatalf("accepted unsafe external override URL %q", target)
		}
	}
	if _, err := externalHealthClient(base, "https://funnel.example.test/", "invalid"); err == nil {
		t.Fatal("accepted invalid override IP")
	}
}

func TestVerifyDeploymentExternalWithoutOverrideUsesBase(t *testing.T) {
	server, base := externalTLSFixture(t, "funnel.example.test")
	client, err := externalHealthClient(base, server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDeployment(context.Background(), client, DeployEvidence{ReadyURL: server.URL + "/readyz", LocalURL: server.URL, ExternalURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	_, err = client.Get("https://funnel.example.test/")
	if err == nil || !strings.Contains(err.Error(), "unexpected dial target") {
		t.Fatal("missing override bypassed the base DNS/dial behavior")
	}
}
