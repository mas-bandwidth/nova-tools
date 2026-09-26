package main

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func init() {
	register(Verb{
		Name:    "drain",
		Summary: "put parked work back: resume named benches/friends, release waiting cards; --control <id> tears a control run down",
		Run:     runDrain,
	})
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// runDrain is `nova-sprint drain --sprint <S> [--resume bench:<b>|friend:<f>]...` (#3035). Every item prints one DRAIN OK or DRAIN
// REFUSED line on stdout; exit 2 when anything was refused.
// `nova-sprint drain --control control-<id>` (#3442) instead removes every
// key that control run left in the store, in one call, and prints one
// DRAIN DONE control=<id> line.
func runDrain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("drain")
	sprint := fs.String("sprint", "", "")
	control := fs.String("control", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	var resume multiFlag
	fs.Var(&resume, "resume", "")
	if err := fs.Parse(args); err != nil || (*sprint == "" && *control == "") || *addr == "" || fs.NArg() > 0 {
		return refuse(stderr, "drain", "needs (--sprint <name> | --control <id>) and --redis <addr>; --resume bench:<b>|friend:<f> repeats")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	client, code := openCardRedis(ctx, *addr, stderr)
	if code != 0 {
		return code
	}
	defer client.Close()
	res := card.Drain(ctx, client, *sprint, card.DrainOptions{Resume: resume, Control: *control})
	if _, err := io.WriteString(stdout, res.Stdout); err != nil {
		return refuse(stderr, "drain", err.Error())
	}
	if res.Code != 0 && res.Stderr != "" {
		return writeCardResult(stdout, stderr, res)
	}
	return res.Code
}
