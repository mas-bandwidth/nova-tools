package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/events"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/post/hook"
	"github.com/redis/go-redis/v9"
)

func runHook(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-post hook", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "listen address")
	redisAddr := fs.String("redis", "", "redis address (required)")
	redisUser := fs.String("redis-user", "", "redis username")
	redisPass := fs.String("redis-pass", "", "redis password")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuseLine(stderr, "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes), 2)
	}
	if fs.NArg() > 0 {
		return refuseLine(stderr, "bad-flags", fmt.Sprintf("unexpected argument %q", oneline.Field(fs.Arg(0))), 2)
	}
	if *redisAddr == "" {
		return refuseLine(stderr, "missing-flag", "--redis is required; refusing to guess", 2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	rdb := redis.NewClient(&redis.Options{
		Addr:     *redisAddr,
		Username: *redisUser,
		Password: *redisPass,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return refuseLine(stderr, "redis-unreachable", oneline.Err(err), 2)
	}

	emitter := hook.NewRedisEmitter(rdb)
	rec := hook.NewReceiver(emitter)

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", rec.ServeHTTP)
	mux.HandleFunc("/webhook/", rec.ServeHTTP)

	_, _ = fmt.Fprintf(stderr, "HOOK listening on %s redis=%s\n", *addr, *redisAddr)

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return refuseLine(stderr, "listen-failed", oneline.Err(err), 2)
	}
	return 0
}

var _ events.Event = events.Event{}
