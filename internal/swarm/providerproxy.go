package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// THE BODY-READ DEADLINE LIVES ON A CLIENT THIS PROCESS OWNS.
//
// The card does not issue the provider request. OpenCode does. OpenCode 1.18.20
// was measured to reach one POST /v1/responses with headerTimeout and
// chunkTimeout set to 2000 in the job config and to still be running at 50.5s
// with no timeout line. Writing those options is not this deadline.
//
// native points the harness baseURL at a proxy bound to 127.0.0.1. The proxy
// forwards one upstream request. After response headers arrive, readWithinSilence
// waits for body bytes. A gap of ProviderBodySilence with no bytes ends the
// request. The run records UNKNOWN and does not launch the card again, and the
// proxy does not open a second upstream request. A body that arrives inside the
// gap is success and is not unknown. Whole-card silence is not this signal;
// that stays the 300s idle watch.
//
// THE HEADER WAIT IS OWNED TOO (stella 5782441006). The body timer starts only
// when headers arrive, so an upstream that accepts the POST and never answers
// with headers had no deadline of ours. The proxy's own transport waits
// ProviderHeaderTimeout for response headers after the request is fully
// written. That expiry is the same UNKNOWN as a silent body: the request was
// sent and may have been accepted, so the run records unknown and the proxy
// opens no second upstream request. A timeout before the request was written,
// such as a dial that never connected, sent no work and stays a 502. Both
// gaps are per request and are not the card's runtime.

// errBodySilence is the gap readWithinSilence reports. It is not a launch
// failure and it is not written into the harness log for the classifier to
// find: the proxy signals the run directly.
var errBodySilence = errors.New("provider body silence")

// ProviderProxyConfig is one proxy. Silence zero means ProviderBodySilence.
// After nil means a time.Timer inside readWithinSilence. A test passes After
// so the production gap is an event it can fire. HeaderWait zero means
// ProviderHeaderTimeout; a test passes a shorter wait so the suite does not
// wait 45s.
type ProviderProxyConfig struct {
	Upstream   string
	Silence    time.Duration
	After      func(time.Duration) <-chan time.Time
	HeaderWait time.Duration
}

// ProviderProxy is the localhost client the harness dials instead of the
// provider. HarnessURL is the baseURL written into the job config.
type ProviderProxy struct {
	harness  string
	upstream *url.URL
	silence  time.Duration
	after    func(time.Duration) <-chan time.Time
	headerWt time.Duration
	client   *http.Client
	srv      *http.Server
	stall    chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc

	mu        sync.Mutex
	dead      bool
	requests  int64
	headersAt time.Time
	stalledAt time.Time
	sentAt    time.Time
	noHeaders bool

	once      sync.Once
	closeOnce sync.Once
}

// ListenProviderProxy listens on 127.0.0.1 and forwards to upstream.
// A base URL that is not http or https is refused so the caller can leave
// it unchanged instead of sending the harness somewhere else.
func ListenProviderProxy(cfg ProviderProxyConfig) (*ProviderProxy, error) {
	if !ProviderProxyEligible(cfg.Upstream) {
		return nil, fmt.Errorf("not an http provider base url")
	}
	u, err := url.Parse(cfg.Upstream)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	silence := cfg.Silence
	if silence <= 0 {
		silence = ProviderBodySilence
	}
	headerWait := cfg.HeaderWait
	if headerWait <= 0 {
		headerWait = ProviderHeaderTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &ProviderProxy{
		upstream: u,
		silence:  silence,
		after:    cfg.After,
		headerWt: headerWait,
		stall:    make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:                 nil,
				DisableCompression:    true,
				ResponseHeaderTimeout: headerWait,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	harness := *u
	harness.Scheme = "http"
	harness.Host = ln.Addr().String()
	harness.User = nil
	p.harness = harness.String()
	p.srv = &http.Server{Handler: p}
	go func() {
		if err := p.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// The run ends the card; a failed accept is a dial error, not a
			// silent body.
		}
	}()
	return p, nil
}

// HarnessURL is the baseURL the harness dials.
func (p *ProviderProxy) HarnessURL() string { return p.harness }

// Silence is the gap the body timer is armed with.
func (p *ProviderProxy) Silence() time.Duration { return p.silence }

// HeaderWait is how long the proxy waits for response headers after the
// request is written.
func (p *ProviderProxy) HeaderWait() time.Duration { return p.headerWt }

// Requests is how many upstream requests were started. A request refused
// after a silent body is not counted.
func (p *ProviderProxy) Requests() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests
}

// Stalled is closed once, when a body gap or an expired header wait ends the
// request.
func (p *ProviderProxy) Stalled() <-chan struct{} { return p.stall }

// Lost reports that the body gap or the header wait already fired. A closed
// channel stays lost.
func (p *ProviderProxy) Lost() bool {
	select {
	case <-p.stall:
		return true
	default:
		return false
	}
}

// SilenceWall is the measured gap from response headers to the body deadline.
// Zero means the deadline has not fired, or headers were never seen.
func (p *ProviderProxy) SilenceWall() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.headersAt.IsZero() || p.stalledAt.IsZero() {
		return 0
	}
	return p.stalledAt.Sub(p.headersAt)
}

// HeaderWall is the measured gap from the written request to the expired
// header wait. Zero means the header wait did not end the request.
func (p *ProviderProxy) HeaderWall() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.noHeaders || p.sentAt.IsZero() || p.stalledAt.IsZero() {
		return 0
	}
	return p.stalledAt.Sub(p.sentAt)
}

// Close stops the listener and unblocks a body read still in progress.
// A read cancelled here is not UNKNOWN: the run is already over.
func (p *ProviderProxy) Close() error {
	var err error
	p.closeOnce.Do(func() {
		p.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		err = p.srv.Shutdown(ctx)
	})
	return err
}

// ProviderProxyEligible reports whether baseURL is an http(s) endpoint the
// proxy can stand in front of. Anything else is left for the harness as given.
func ProviderProxyEligible(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// ProviderBaseURL is the model's options.baseURL, or empty when the config
// has none. The proxy is only installed when this is an http endpoint.
func ProviderBaseURL(raw []byte, provider string) string {
	opts := providerOptions(raw, provider)
	if opts == nil {
		return ""
	}
	base, _ := opts["baseURL"].(string)
	return strings.TrimSpace(base)
}

// PointProviderAtProxy returns the config with that provider's baseURL set
// to the proxy. The other options, including an apiKey, stay. ok is false
// when there is no baseURL to replace: the caller must not claim the harness
// was pointed at the proxy.
func PointProviderAtProxy(raw []byte, provider, proxyURL string) ([]byte, bool) {
	if provider == "" || proxyURL == "" {
		return raw, false
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return raw, false
	}
	providers, _ := cfg["provider"].(map[string]any)
	if providers == nil {
		return raw, false
	}
	entry, _ := providers[provider].(map[string]any)
	if entry == nil {
		return raw, false
	}
	opts, _ := entry["options"].(map[string]any)
	if opts == nil {
		return raw, false
	}
	base, _ := opts["baseURL"].(string)
	if strings.TrimSpace(base) == "" {
		return raw, false
	}
	opts["baseURL"] = proxyURL
	entry["options"] = opts
	providers[provider] = entry
	cfg["provider"] = providers
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return raw, false
	}
	return append(out, '\n'), true
}

func providerOptions(raw []byte, provider string) map[string]any {
	var cfg map[string]any
	if json.Unmarshal(raw, &cfg) != nil {
		return nil
	}
	providers, _ := cfg["provider"].(map[string]any)
	entry, _ := providers[provider].(map[string]any)
	if entry == nil {
		return nil
	}
	opts, _ := entry["options"].(map[string]any)
	return opts
}

func (p *ProviderProxy) begin() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead {
		return false
	}
	p.requests++
	return true
}

func (p *ProviderProxy) sawHeaders() {
	p.mu.Lock()
	if p.headersAt.IsZero() {
		p.headersAt = time.Now()
	}
	p.mu.Unlock()
}

func (p *ProviderProxy) sent() {
	p.mu.Lock()
	if p.sentAt.IsZero() {
		p.sentAt = time.Now()
	}
	p.mu.Unlock()
}

func (p *ProviderProxy) markLost() { p.lose(false) }

func (p *ProviderProxy) lose(noHeaders bool) {
	p.once.Do(func() {
		p.mu.Lock()
		p.dead = true
		p.noHeaders = noHeaders
		p.stalledAt = time.Now()
		p.mu.Unlock()
		close(p.stall)
	})
}

// headerWaitExpired reports that a written request got no response headers
// within the transport's header wait. A cancelled run is not this, and
// neither is a timeout before the request was written: a dial that never
// connected sent no work.
func headerWaitExpired(ctx context.Context, err error, sent bool) bool {
	if err == nil || ctx.Err() != nil || !sent {
		return false
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (p *ProviderProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()

	target := url.URL{
		Scheme:   p.upstream.Scheme,
		Host:     p.upstream.Host,
		User:     p.upstream.User,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}
	if target.Path == "" {
		target.Path = "/"
	}
	var wrote atomic.Bool
	traced := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				wrote.Store(true)
				p.sent()
			}
		},
	})
	out, err := http.NewRequestWithContext(traced, r.Method, target.String(), r.Body)
	if err != nil {
		http.Error(w, "upstream", http.StatusBadGateway)
		return
	}
	out.Header = r.Header.Clone()
	dropHop(out.Header)
	// Count only a request that is about to be forwarded. A request after the
	// body gap is refused here and does not touch the upstream.
	if !p.begin() {
		http.Error(w, "lost", http.StatusBadGateway)
		return
	}
	resp, err := p.client.Do(out)
	if err != nil {
		if headerWaitExpired(ctx, err, wrote.Load()) {
			// The request was written and no headers came back inside the
			// owned wait. It may have been accepted: the same UNKNOWN as a
			// silent body, signalled before the abort, and no second
			// upstream request after it.
			p.lose(true)
			panic(http.ErrAbortHandler)
		}
		http.Error(w, "upstream", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	p.sawHeaders()
	copyProxyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if err := copyWithinSilence(ctx, w, resp.Body, p.silence, p.after); errors.Is(err, errBodySilence) {
		// A normal return would finish a chunked body and the harness would
		// read a clean EOF. Abort the connection so the stall is a broken
		// read, and signal the run. The signal is sent before the panic so
		// it is not lost when the server recovers ErrAbortHandler.
		p.markLost()
		panic(http.ErrAbortHandler)
	}
}

// readWithinSilence reads one chunk or returns errBodySilence when no bytes
// arrive within silence. after nil arms a time.Timer; a test's after is the
// production gap as an event. A read that wins stops the timer. Cancelling
// ctx is not silence: the run is ending for some other reason.
func readWithinSilence(ctx context.Context, src io.Reader, buf []byte, silence time.Duration, after func(time.Duration) <-chan time.Time) (int, error) {
	type got struct {
		n   int
		err error
	}
	ch := make(chan got, 1)
	go func() {
		n, err := src.Read(buf)
		ch <- got{n, err}
	}()
	stop := func() {}
	var timer <-chan time.Time
	if after != nil {
		timer = after(silence)
	} else {
		t := time.NewTimer(silence)
		timer = t.C
		stop = func() { t.Stop() }
	}
	defer stop()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case g := <-ch:
		return g.n, g.err
	case <-timer:
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
			return 0, errBodySilence
		}
	}
}

func copyWithinSilence(ctx context.Context, dst io.Writer, src io.Reader, silence time.Duration, after func(time.Duration) <-chan time.Time) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := readWithinSilence(ctx, src, buf, silence, after)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			if f, ok := dst.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

var hopHeader = map[string]bool{
	"Connection":          true,
	"Proxy-Connection":    true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

func dropHop(h http.Header) {
	for k := range h {
		if hopHeader[http.CanonicalHeaderKey(k)] {
			h.Del(k)
		}
	}
}

func copyProxyHeader(dst, src http.Header) {
	for k, vv := range src {
		ck := http.CanonicalHeaderKey(k)
		if hopHeader[ck] || ck == "Content-Length" {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
