package main

import (
	"context"
	"flag"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

func init() {
	register(Verb{
		Name:    "drain",
		Summary: "put parked work back: resume named benches/friends, import retired queue dirs once, release waiting cards",
		Run:     runDrain,
	})
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// runDrain is `nova-sprint drain --sprint <S> [--resume bench:<b>|friend:<f>]...
// [--queue-dir <dir>]...` (#3035). Every item prints one DRAIN OK or DRAIN
// REFUSED line on stdout; exit 2 when anything was refused.
func runDrain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("drain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sprint := fs.String("sprint", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	var resume, dirs multiFlag
	fs.Var(&resume, "resume", "")
	fs.Var(&dirs, "queue-dir", "")
	if err := fs.Parse(args); err != nil || *sprint == "" || *addr == "" || fs.NArg() > 0 {
		return refuse(stderr, "drain", "needs --sprint <name> and --redis <addr>; --resume bench:<b>|friend:<f> and --queue-dir <dir> repeat")
	}
	// Each queue-dir card is linted, and lint probes the card's repository
	// (30 s per probe), so the bound is the whole import, not one call.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	client, code := openCardRedis(ctx, *addr, stderr)
	if code != 0 {
		return code
	}
	defer client.Close()
	res := card.Drain(ctx, client, *sprint, card.DrainOptions{Resume: resume, QueueDirs: dirs})
	if _, err := io.WriteString(stdout, res.Stdout); err != nil {
		return refuse(stderr, "drain", err.Error())
	}
	if res.Code != 0 && res.Stderr != "" {
		return writeCardResult(stdout, stderr, res)
	}
	return res.Code
}
