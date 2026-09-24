package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func runLandWorker(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("land worker")
	bench := fs.String("bench", "", "bench name (required)")
	redisAddr := fs.String("redis", "", "redis address")
	slots := fs.Int("slots", 1, "number of slot goroutines")
	mirrorDir := fs.String("mirror", "", "mirror directory")
	once := fs.Bool("once", false, "run one pass then exit")

	var repos []string
	var passArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--repo" && i+1 < len(args) {
			repos = append(repos, args[i+1])
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--repo=") {
			repos = append(repos, strings.TrimPrefix(args[i], "--repo="))
			continue
		}
		passArgs = append(passArgs, args[i])
	}

	if err := fs.Parse(passArgs); err != nil {
		return refuse(errOut, "land worker", err.Error())
	}

	if *bench == "" {
		return refuse(errOut, "land worker", "needs --bench <b>")
	}

	addr := *redisAddr
	if addr == "" {
		addr = os.Getenv("NOVA_REDIS_ADDR")
		if addr == "" {
			addr = "127.0.0.1:6379"
		}
	}

	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint land worker: %v\n", err)
		return 6
	}
	defer st.Close()

	if len(repos) == 0 {
		repos = []string{"nova-tools"}
	}

	w := land.NewWorker(land.WorkerConfig{
		Client:    st.Client(),
		Store:     st,
		Bench:     *bench,
		Repos:     repos,
		Slots:     *slots,
		MirrorDir: *mirrorDir,
	})

	if *once {
		_, err := w.RunOnce(ctx, "slot-1")
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint land worker: %v\n", err)
			return 2
		}
		return 0
	}

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := w.Run(sigCtx); err != nil && err != context.Canceled {
		fmt.Fprintf(errOut, "nova-sprint land worker: %v\n", err)
		return 2
	}
	return 0
}
