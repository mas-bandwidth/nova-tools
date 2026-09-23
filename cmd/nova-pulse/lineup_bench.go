package main

// How `nova-pulse lineup` gets a bench's facts: one `ssh <target> bash -s` session per bench
// with the probe script on stdin (never a session per check, and never one per card -- the
// lineup is itself held to the rule its launcher row enforces), all benches at once, each
// under --timeout. --facts <bench>=<file> reads a captured FACT transcript instead: that is
// how the tests drive every rule, and how a lineup is replayed from a bench's own output.

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
	"github.com/mas-bandwidth/nova-tools/internal/pulse/lineup"
)

// lineupSource is one bench to ask: over ssh (target and home from the benches file) or
// from a FACT file.
type lineupSource struct {
	bench  string
	target string
	home   string
	facts  string // a FACT file; set only for --facts
}

// lineupGot is one bench's answer: its facts, or why there are none.
type lineupGot struct {
	bench string
	facts lineup.Facts
	err   string
}

// lineupSources resolves the benches to ask, in name order. A --bench the benches file does
// not carry is a refusal: a lineup that silently asks fewer benches than it was told to is
// a lineup that passes by omission.
func lineupSources(benchesPath string, names, facts []string, stderr io.Writer) ([]lineupSource, int) {
	var out []lineupSource
	seen := map[string]bool{}
	if strings.TrimSpace(benchesPath) != "" {
		benches, err := pulse.ReadFleetBenches(benchesPath)
		if err != nil {
			fmt.Fprintf(stderr, "nova-pulse lineup: --benches: %s (a benches file is name<TAB>ssh target<TAB>home<TAB>mac)\n", oneline.Err(err))
			return nil, 2
		}
		want := names
		if len(want) == 0 {
			for name := range benches {
				want = append(want, name)
			}
		}
		for _, name := range want {
			b, ok := benches[name]
			if !ok {
				fmt.Fprintf(stderr, "nova-pulse lineup: --bench %s is not in %s\n", oneline.Field(name), oneline.Field(benchesPath))
				return nil, 2
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, lineupSource{bench: b.Name, target: b.SSH, home: b.Home})
		}
	}
	for _, spec := range facts {
		name, path, ok := strings.Cut(spec, "=")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(path) == "" {
			fmt.Fprintf(stderr, "nova-pulse lineup: --facts wants <bench>=<file>, got %s\n", oneline.Field(spec))
			return nil, 2
		}
		if seen[name] {
			fmt.Fprintf(stderr, "nova-pulse lineup: bench %s is named twice\n", oneline.Field(name))
			return nil, 2
		}
		seen[name] = true
		out = append(out, lineupSource{bench: name, facts: path})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].bench < out[j].bench })
	return out, 0
}

// lineupFetch asks every source at once and returns the answers in the sources' order.
func lineupFetch(sources []lineupSource, probe lineup.ProbeOptions, sshProg string, timeout time.Duration) []lineupGot {
	got := make([]lineupGot, len(sources))
	var wg sync.WaitGroup
	for i, s := range sources {
		wg.Add(1)
		go func(i int, s lineupSource) {
			defer wg.Done()
			got[i] = lineupAsk(s, probe, sshProg, timeout)
		}(i, s)
	}
	wg.Wait()
	return got
}

func lineupAsk(s lineupSource, probe lineup.ProbeOptions, sshProg string, timeout time.Duration) lineupGot {
	var out string
	if s.facts != "" {
		raw, err := os.ReadFile(s.facts)
		if err != nil {
			return lineupGot{bench: s.bench, err: "facts file: " + oneline.Err(err)}
		}
		out = string(raw)
	} else {
		probe.Home = s.home
		if probe.Home == "-" {
			probe.Home = ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		o, err := pulse.SSHRunner{Program: sshProg}.Run(ctx, s.target, lineup.ProbeScript(probe))
		out = o
		if err != nil {
			if _, ended := lineup.ParseFacts(out); !ended {
				return lineupGot{bench: s.bench, err: "ssh " + s.target + ": " + oneline.Err(err) + " " + lastNonEmpty(out)}
			}
		}
	}
	facts, ended := lineup.ParseFacts(out)
	if !ended {
		return lineupGot{bench: s.bench, err: "the probe did not finish (no FACT end line) " + lastNonEmpty(out)}
	}
	return lineupGot{bench: s.bench, facts: facts}
}

func lastNonEmpty(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
