package util

import (
	"net/http"
	"net/url"
	"strings"
	"sync"

	tls "github.com/refraction-networking/utls"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

type uTLSRoundTripper struct {
	mu          sync.Mutex
	connections map[string]*http2.ClientConn
	pending     map[string]*sync.Cond
	dialer      proxy.Dialer
}

func newUTLSRoundTripper(cfg *config.SDKConfig) *uTLSRoundTripper {
	var dialer proxy.Dialer = proxy.Direct
	if cfg != nil && strings.TrimSpace(cfg.ProxyURL) != "" {
		proxyURL, err := url.Parse(strings.TrimSpace(cfg.ProxyURL))
		if err != nil {
			log.Errorf("failed to parse proxy URL %q: %v", cfg.ProxyURL, err)
		} else {
			proxyDialer, errProxy := proxy.FromURL(proxyURL, proxy.Direct)
			if errProxy != nil {
				log.Errorf("failed to create proxy dialer for %q: %v", cfg.ProxyURL, errProxy)
			} else {
				dialer = proxyDialer
			}
		}
	}

	return &uTLSRoundTripper{
		connections: make(map[string]*http2.ClientConn),
		pending:     make(map[string]*sync.Cond),
		dialer:      dialer,
	}
}

func (t *uTLSRoundTripper) getOrCreateConnection(host, addr string) (*http2.ClientConn, error) {
	t.mu.Lock()

	if h2Conn, ok := t.connections[host]; ok && h2Conn.CanTakeNewRequest() {
		t.mu.Unlock()
		return h2Conn, nil
	}

	if cond, ok := t.pending[host]; ok {
		cond.Wait()
		if h2Conn, ok := t.connections[host]; ok && h2Conn.CanTakeNewRequest() {
			t.mu.Unlock()
			return h2Conn, nil
		}
	}

	cond := sync.NewCond(&t.mu)
	t.pending[host] = cond
	t.mu.Unlock()

	h2Conn, err := t.createConnection(host, addr)

	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.pending, host)
	cond.Broadcast()

	if err != nil {
		return nil, err
	}

	t.connections[host] = h2Conn
	return h2Conn, nil
}

func (t *uTLSRoundTripper) createConnection(host, addr string) (*http2.ClientConn, error) {
	conn, err := t.dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	tlsConn := tls.UClient(conn, &tls.Config{ServerName: host}, tls.HelloFirefox_Auto)
	if errHandshake := tlsConn.Handshake(); errHandshake != nil {
		_ = conn.Close()
		return nil, errHandshake
	}

	h2Transport := &http2.Transport{}
	h2Conn, err := h2Transport.NewClientConn(tlsConn)
	if err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	return h2Conn, nil
}

func (t *uTLSRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return http.DefaultTransport.RoundTrip(req)
	}

	host := req.URL.Hostname()
	addr := req.URL.Host
	if !strings.Contains(addr, ":") {
		addr += ":443"
	}

	h2Conn, err := t.getOrCreateConnection(host, addr)
	if err != nil {
		return nil, err
	}

	resp, err := h2Conn.RoundTrip(req)
	if err != nil {
		t.mu.Lock()
		if cached, ok := t.connections[host]; ok && cached == h2Conn {
			delete(t.connections, host)
		}
		t.mu.Unlock()
		return nil, err
	}
	return resp, nil
}

// NewUTLSHTTPClient returns an HTTP client with a Firefox-like TLS fingerprint.
func NewUTLSHTTPClient(cfg *config.SDKConfig) *http.Client {
	return &http.Client{Transport: newUTLSRoundTripper(cfg)}
}
