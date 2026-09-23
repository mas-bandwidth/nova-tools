package main

// runHook is the `nova-post hook` listener of nova-tools #2657: GitHub webhook
// deliveries in, one XADD per carried event on ev:github out
// (internal/post/hook, internal/ghevent).
//
// NEITHER SECRET IS EVER A FLAG. The webhook secret and the Redis password are
// read from the environment `nova-secrets exec --only` puts them in, named by
// --secret-env and --password-env, so neither reaches an argv a `ps` can read
// and nothing here prints either. With no webhook secret the verb refuses to
// start: an unsigned receiver is not a mode.
//
// The listen address has no default and may not be a wildcard. The receiver
// binds loopback (Tailscale Funnel or `tailscale serve` fronts it) or a
// tailnet address; every delivery is still signature-checked.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/post/hook"
	"github.com/redis/go-redis/v9"
)

// defaultHookSecretEnv holds the GitHub webhook secret nova-secrets delivers.
const defaultHookSecretEnv = "NOVA_GITHUB_WEBHOOK_SECRET"

// defaultHookPasswordEnv is the fleet store's password, as the other verbs read it.
const defaultHookPasswordEnv = "NOVA_REDIS_BENCH_PASSWORD"

func runHook(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-post hook", flag.ContinueOnError)
	addr := fs.String("addr", "", "listen host:port; loopback or a tailnet address, never a wildcard")
	store := fs.String("redis", "", "the fleet Redis host:port")
	user := fs.String("user", "", "the Redis ACL user")
	passwordEnv := fs.String("password-env", defaultHookPasswordEnv, "the variable holding the Redis password")
	secretEnv := fs.String("secret-env", defaultHookSecretEnv, "the variable holding the webhook secret")
	path := fs.String("path", "/webhook", "the URL path GitHub posts to")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuseLine(stderr, "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes), 2)
	}
	if fs.NArg() > 0 {
		return refuseLine(stderr, "bad-flags", fmt.Sprintf("unexpected argument %q", oneline.Field(fs.Arg(0))), 2)
	}
	if *addr == "" {
		return refuseLine(stderr, "missing-flag", "--addr is required: 127.0.0.1:<port> behind tailscale funnel/serve, or a tailnet address; refusing to guess", 2)
	}
	if err := hookAddrOK(*addr); err != nil {
		return refuseLine(stderr, "wildcard-addr", err.Error(), 2)
	}
	if *store == "" {
		return refuseLine(stderr, "missing-flag", "--redis is required: the fleet Redis host:port; refusing to guess", 2)
	}
	if !strings.HasPrefix(*path, "/") {
		return refuseLine(stderr, "bad-flags", "--path wants an absolute URL path such as /webhook", 2)
	}
	secret := os.Getenv(*secretEnv)
	if secret == "" {
		return refuseLine(stderr, "missing-secret", fmt.Sprintf("the webhook secret is empty; run under nova-secrets exec --only %s", oneline.Field(*secretEnv)), 2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	rdb := redis.NewClient(&redis.Options{Addr: *store, Username: *user, Password: os.Getenv(*passwordEnv)})
	defer rdb.Close()
	pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
	err := rdb.Ping(pctx).Err()
	pcancel()
	if err != nil {
		// go-redis names the address, never the password.
		return refuseLine(stderr, "redis-unreachable", oneline.Err(err), 2)
	}
	h, err := hook.NewHandler(rdb, []byte(secret))
	if err != nil {
		return refuseLine(stderr, "bad-config", oneline.Err(err), 2)
	}

	mux := http.NewServeMux()
	mux.Handle(*path, h)
	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return refuseLine(stderr, "listen-failed", oneline.Err(err), 2)
	}
	fmt.Fprintf(stdout, "HOOK LISTEN addr=%s path=%s stream=%s redis=%s\n",
		oneline.Field(ln.Addr().String()), oneline.Field(*path), ghevent.Stream, oneline.Field(*store))

	go func() {
		<-ctx.Done()
		sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer scancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return refuseLine(stderr, "serve-failed", oneline.Err(err), 1)
	}
	fmt.Fprintf(stdout, "HOOK STOP addr=%s\n", oneline.Field(ln.Addr().String()))
	return 0
}

// hookAddrOK refuses a listen address whose host is empty or unspecified
// (":8080", "0.0.0.0:8080", "[::]:8080"): those bind every interface.
func hookAddrOK(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--addr wants host:port, got %q", addr)
	}
	if host == "" {
		return fmt.Errorf("--addr %q binds every interface; name 127.0.0.1 or a tailnet address", addr)
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return fmt.Errorf("--addr %q binds every interface; name 127.0.0.1 or a tailnet address", addr)
	}
	return nil
}
