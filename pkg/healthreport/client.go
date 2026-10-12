package healthreport

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"
)

var tailnet = netip.MustParsePrefix("100.64.0.0/10")

// Client reports immutable events to its enrolled TLS audience. It owns no
// source queue, retry loop, acceptance ledger or alarm-delivery state.
type Client struct {
	capability *Capability
	http       *http.Client
}

// NewClient uses normal certificate verification and explicit roots when given.
// All connections resolve and dial only loopback/tailnet addresses, with the
// checked IP itself dialed to prevent a second DNS lookup changing the boundary.
// A caller cannot substitute a transport that logs headers or disables TLS.
func NewClient(c *Capability, roots *x509.CertPool) (*Client, error) {
	if c == nil {
		return nil, errors.New("health capability is required")
	}
	if roots != nil {
		roots = roots.Clone()
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            privateDial,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 8192,
	}
	return &Client{capability: c, http: &http.Client{
		Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("health report redirect refused") },
	}}, nil
}

// Close releases idle TLS connections; it never changes accepted/source state.
func (c *Client) Close() {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
}

// Report validates the event against its grant before dialing. It returns only
// a matching acceptance receipt. Errors never quote response bodies, URLs,
// credentials or transport diagnostics that could repeat an Authorization value.
func (c *Client) Report(ctx context.Context, raw []byte) (Receipt, error) {
	if c == nil || c.capability == nil {
		return Receipt{}, errors.New("health client is not configured")
	}
	v, err := ParseEvent(raw, c.capability.enrollment)
	if err != nil {
		return Receipt{}, err
	}
	want := ReceiptFor(v, raw)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.capability.enrollment.Audience, bytes.NewReader(raw))
	if err != nil {
		return Receipt{}, errors.New("health report request refused")
	}
	req.Header.Set("Content-Type", "application/json")
	c.capability.headers(req.Header)
	resp, err := c.http.Do(req)
	if err != nil {
		return Receipt{}, errors.New("health receiver did not answer over verified TLS")
	}
	defer func() { _ = resp.Body.Close() }() // ignored: receipt is read or refused; closing cannot change acceptance
	if resp.StatusCode != http.StatusOK {
		return Receipt{}, errors.New("health receiver refused the report")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxReceipt+1))
	if err != nil {
		return Receipt{}, errors.New("health acceptance receipt was cut")
	}
	return parseReceipt(body, want)
}

func privateDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, errors.New("invalid health receiver address")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("health receiver does not resolve")
	}
	if !privateIPs(ips) {
		return nil, errors.New("health receiver is outside loopback and tailnet")
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	for _, ip := range ips {
		var conn net.Conn
		conn, err = d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("health receiver connection failed")
}

// privateIPs checks every resolved address before any dial, including mixed
// public/private answers. Dialing the checked IP prevents DNS rebinding.
func privateIPs(ips []netip.Addr) bool {
	if len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		ip = ip.Unmap()
		if !ip.IsLoopback() && !tailnet.Contains(ip) {
			return false
		}
	}
	return true
}
