package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/guard"
)

func cmdGuard(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "set" {
		return cmdGuardSet(args[1:], stdout, stderr)
	}

	fs := flag.NewFlagSet("guard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bench := fs.String("bench", "", "")
	loopSec := fs.Int("loop", 0, "")
	dryRun := fs.Bool("dry-run", false, "")
	redisAddr := fs.String("redis", "", "")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	in := guard.GuardInput{
		Bench:     *bench,
		DryRun:    *dryRun,
		RedisAddr: *redisAddr,
		Stdout:    stdout,
		Stderr:    stderr,
	}

	if *loopSec > 0 {
		ticker := time.NewTicker(time.Duration(*loopSec) * time.Second)
		defer ticker.Stop()
		for {
			code := guard.Pass(in)
			if code != 0 {
				fmt.Fprintf(stderr, "GUARD LOOP NOTE pass failed with exit %d\n", code)
			}
			<-ticker.C
		}
	}

	return guard.Pass(in)
}

func cmdGuardSet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("guard set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bench := fs.String("bench", "", "")
	maxAge := fs.Int("max-age", 300, "")
	names := fs.String("names", "rg,find,grep,ugrep", "")
	spare := fs.String("spare", "harvest,HARVEST,SECRETS,runner-nova-tools,Runner.Worker", "")
	redisAddr := fs.String("redis", "", "")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*bench) == "" {
		fmt.Fprintln(stderr, "GUARD SET REFUSED: --bench is required")
		return 2
	}

	cfg := guard.Config{
		MaxAge: time.Duration(*maxAge) * time.Second,
		Names:  strings.Split(*names, ","),
		Spare:  strings.Split(*spare, ","),
	}

	if err := guard.SetConfig(*redisAddr, *bench, cfg); err != nil {
		fmt.Fprintf(stderr, "GUARD SET REFUSED: could not write config: %s\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "GUARD SET bench=%s max_age=%d names=%s spare=%s\n", *bench, *maxAge, *names, *spare)
	return 0
}
