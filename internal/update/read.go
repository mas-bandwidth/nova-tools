package update

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
)

const ChildCap = 64 * 1024

var dotted = regexp.MustCompile(`[0-9]\.[0-9]`)
var digit = regexp.MustCompile(`[0-9]`)
var bareCommit = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
var release = regexp.MustCompile(`^[0-9]+(\.[0-9]+)+$`)
var digest = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

type Read struct{ Raw, Version, Path, Reason, Remedy, Source string }

func (r Read) Known() bool      { return r.Reason == "" }
func firstLine(s string) string { s, _, _ = strings.Cut(s, "\n"); return strings.TrimSuffix(s, "\r") }
func opaque(line string) bool {
	f := strings.Fields(line)
	return len(f) > 1 && (f[1] == "devel" || bareCommit.MatchString(f[1]))
}
func versionKey(line string) (string, error) {
	if opaque(line) {
		return "", fmt.Errorf("no_release_identity")
	}
	for _, t := range strings.Fields(line) {
		if dotted.MatchString(t) {
			return t[digit.FindStringIndex(t)[0]:], nil
		}
	}
	return "", fmt.Errorf("no_version")
}
func identity(e Entry, raw string, report bool) Read {
	r := Read{Raw: firstLine(raw), Remedy: "wrap it in a script that prints the version alone"}
	if e.Kind == "pin" {
		f := strings.Fields(r.Raw)
		if len(f) < 2 {
			r.Reason = "version line has fewer than two tokens"
		} else {
			r.Version = f[1]
		}
		return r
	}
	if e.Kind == "model" {
		for _, line := range strings.Split(raw, "\n") {
			f := strings.Fields(line)
			if len(f) > 1 && f[0] == e.Name && digest.MatchString(strings.ToLower(f[1])) {
				r.Raw = strings.TrimSuffix(line, "\r")
				r.Version = strings.ToLower(f[1])[:12]
				return r
			}
		}
		r.Reason = "model_not_found"
		r.Remedy = "owner: ollama pull " + e.Name + "; nova-local status --list"
		return r
	}
	if report && opaque(r.Raw) {
		return r
	}
	v, err := versionKey(r.Raw)
	if err != nil {
		r.Reason = err.Error()
		if r.Reason == "no_release_identity" {
			r.Remedy = "install a stamped build, or read it with report"
		}
	} else {
		r.Version = v
	}
	return r
}

type ProcessResult struct{ Stdout, Stderr, Path, Reason string }

func process(ctx context.Context, args []string, input io.Reader, cap int) ProcessResult {
	r := ProcessResult{}
	if ctx.Err() != nil {
		r.Reason = "budget"
		return r
	}
	if len(args) == 0 {
		r.Reason = "empty argv"
		return r
	}
	path, err := exec.LookPath(args[0])
	if err != nil {
		r.Reason = "not_found"
		return r
	}
	r.Path = path
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	out, errs := bounded.NewCapture(cap, cancel), bounded.NewCapture(cap, cancel)
	cmd := exec.CommandContext(child, path, args[1:]...)
	cmd.Stdin = input
	cmd.Stdout = out
	cmd.Stderr = errs
	cmd.WaitDelay = 10 * time.Millisecond
	configureProcess(cmd)
	err = cmd.Run()
	r.Stdout = string(out.Bytes())
	r.Stderr = string(errs.Bytes())
	switch {
	case out.Hit() || errs.Hit():
		r.Reason = "output"
	case ctx.Err() != nil:
		r.Reason = "timeout"
	case err != nil:
		if e, ok := err.(*exec.ExitError); ok {
			r.Reason = fmt.Sprintf("exit %d", e.ExitCode())
		} else {
			r.Reason = "execution failed"
		}
	}
	return r
}
func Installed(ctx context.Context, e Entry, timeout time.Duration, report bool) Read {
	if ctx.Err() != nil {
		return Read{Reason: "budget", Remedy: "increase --budget"}
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p := process(child, e.Installed, nil, ChildCap)
	raw := p.Stdout
	if raw == "" {
		raw = p.Stderr
	}
	r := identity(e, raw, report)
	r.Path = p.Path
	if p.Reason != "" {
		r.Reason = p.Reason
		r.Remedy = "wrap it in a script that prints the version alone"
		if p.Reason == "not_found" {
			r.Remedy = "install " + e.Installed[0] + " or supply its executable path; searched PATH=" + os.Getenv("PATH")
		}
		if p.Reason == "timeout" {
			r.Remedy = "increase --timeout or repair the version command"
		}
	}
	if ctx.Err() != nil {
		r.Reason = "budget"
		r.Remedy = "increase --budget"
	}
	return r
}

// Compare orders only equally long, plain numeric release tags. Arbitrary-size
// components avoid both floating-point loss and machine integer overflow.
func Compare(a, b string) string {
	if a == b {
		return "EQUAL"
	}
	if !release.MatchString(a) || !release.MatchString(b) {
		return "DIFFERENT"
	}
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	if len(aa) != len(bb) {
		return "DIFFERENT"
	}
	for i := range aa {
		x, _ := new(big.Int).SetString(aa[i], 10)
		y, _ := new(big.Int).SetString(bb[i], 10)
		switch x.Cmp(y) {
		case -1:
			return "OLDER"
		case 1:
			return "NEWER"
		}
	}
	return "DIFFERENT"
}
