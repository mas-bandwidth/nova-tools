// Package metrics is the one Prometheus surface the queue verbs share
// (card nx-g61-promhttp-in-verbs; recut of nova-tools #2720).
//
// Three families, each labelled by the component that wrote it:
//
//	nova_queue_depth{component}                 gauge: work waiting to be dealt (fill: ready cards;
//	                                            dealer: pooled cards over every open sprint;
//	                                            lander: members of the batch being gated)
//	nova_leases_held{component}                 gauge: work out on a lease right now (fill: launched
//	                                            cards; dealer: leased slots over every bench;
//	                                            lander: members admitted to the gate)
//	nova_provider_latency_seconds{component,provider}
//	                                            histogram: one observation per call that leaves the
//	                                            process (fill: the launcher per bench; dealer: the
//	                                            ssh session per bench; lander: forge and gate)
//
// A verb holds a *Set; a nil *Set records nothing, so a verb run without
// metrics behaves exactly as before. Handler is a real promhttp handler over
// the set's own registry, and Register puts it on a mux at Path.
package metrics

import (
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Path is where Register serves the exposition.
const Path = "/metrics"

// The component label values the three verbs write.
const (
	Fill   = "fill"
	Dealer = "dealer"
	Lander = "lander"
)

// Set is one registry and the three families on it.
type Set struct {
	reg     *prometheus.Registry
	queue   *prometheus.GaugeVec
	leases  *prometheus.GaugeVec
	latency *prometheus.HistogramVec
}

// New returns a set on a fresh registry, so two sets never share a series.
func New() *Set {
	s := &Set{
		reg: prometheus.NewRegistry(),
		queue: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "nova_queue_depth",
			Help: "Work waiting to be dealt, as the component last counted it.",
		}, []string{"component"}),
		leases: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "nova_leases_held",
			Help: "Work out on a lease right now, as the component last counted it.",
		}, []string{"component"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "nova_provider_latency_seconds",
			Help:    "Wall time of one call that leaves the process (launcher, ssh session, forge, gate).",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 1800},
		}, []string{"component", "provider"}),
	}
	s.reg.MustRegister(s.queue, s.leases, s.latency)
	return s
}

// Default is the process's set; the verbs' command lines hand it to the
// library and serve it with --metrics-addr.
var Default = New()

// QueueDepth sets component's queue depth to n.
func (s *Set) QueueDepth(component string, n int) {
	if s == nil {
		return
	}
	s.queue.WithLabelValues(component).Set(float64(n))
}

// LeasesHeld sets component's held lease count to n.
func (s *Set) LeasesHeld(component string, n int) {
	if s == nil {
		return
	}
	s.leases.WithLabelValues(component).Set(float64(n))
}

// ProviderLatency records one call of d against provider.
func (s *Set) ProviderLatency(component, provider string, d time.Duration) {
	if s == nil {
		return
	}
	s.latency.WithLabelValues(component, provider).Observe(d.Seconds())
}

// Handler is the promhttp handler over this set's registry.
func (s *Set) Handler() http.Handler {
	return promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{Registry: s.reg})
}

// Register serves the set at Path on mux.
func (s *Set) Register(mux *http.ServeMux) {
	mux.Handle(Path, s.Handler())
}

// Serve serves the set at Path on ln in the background and returns the
// server so the caller can Close it. The caller owns the listener: the
// command line opens it from --metrics-addr, a test from httptest.
func (s *Set) Serve(ln net.Listener) *http.Server {
	mux := http.NewServeMux()
	s.Register(mux)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return srv
}

// Server is the set served on an address a verb's --metrics-addr named.
type Server struct {
	addr string
	srv  *http.Server
}

// BeforeClose, when set, is called with a Server's bound address just before
// it stops serving. It is the test seam of the one-shot verbs (fill --once,
// reconcile --once, land run): the test scrapes the verb's own live endpoint
// after the verb's work and before its exit. Production leaves it nil.
var BeforeClose func(addr string)

// Listen opens addr ("host:port"; port 0 picks a free one) and serves the set
// at Path on it until Close. Addr is the address actually bound.
func (s *Set) Listen(addr string) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Server{addr: ln.Addr().String(), srv: s.Serve(ln)}, nil
}

// Addr is the bound host:port.
func (v *Server) Addr() string { return v.addr }

// URL is the scrape URL, http://<addr>/metrics.
func (v *Server) URL() string { return "http://" + v.addr + Path }

// Close runs BeforeClose, then stops serving.
func (v *Server) Close() error {
	if BeforeClose != nil {
		BeforeClose(v.addr)
	}
	return v.srv.Close()
}
