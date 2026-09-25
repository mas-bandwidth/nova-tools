// The expire duty (#2930 rev 5): every `nova-sprint reconcile` pass runs it
// after the refill, through registerReconcileDuty, so the reconciler loop
// the fleet already runs (nova-sprint-reconciler) runs it and rowan-tools
// bin/sprint-requeue goes. The duty gates itself to one sweep per sprint per
// s:<S>:policy expire_every_ms (internal/nsprint/reconcile/expire.go); this
// file registers it and holds its one host seam, the bench evidence session.
package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/benchsh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	registerReconcileDuty("expire", func(st *store.Store) (reconcileDuty, error) {
		return &reconcile.Expire{Client: st.Client(), Prober: expireProber()}, nil
	})
}

// expireProber is the duty's evidence seam: the system ssh to each bench. A
// test swaps in its fake (CI-NET: no host in a test).
var expireProber = func() reconcile.Prober { return sshProber{} }

// sshProber gathers a bench's reconcile-required evidence in one ssh session:
// the batch goes on stdin, one `<sprint> <label> <jobdir> <identity> <remote>
// <branch>` line per card, and the bench answers one `<sprint> <label>
// dir=<0|1> pid=<pid|-> ls=<ok|err> sha=<sha|->` line each. The branch is read
// with git ls-remote on the bench (its own git credentials), never through
// the forge's API. Any failure of the session is no evidence for the whole
// bench; a card whose line is missing or unreadable has none either.
type sshProber struct {
	Program string // default "ssh" (benchsh.Program)
	// Remote renders a card's repo (owner/name) as the git remote the bench
	// reads; default git@github.com:<repo>.git.
	Remote func(repo string) string
}

// probeScript runs on the bench under bash, never the login shell's dialect.
const probeScript = `while read -r s l dir id remote branch; do
  d=0; [ -d "$dir" ] && d=1
  p=$(pgrep -f -- "$id" 2>/dev/null | head -1); [ -n "$p" ] || p=-
  if out=$(git ls-remote "$remote" "refs/heads/$branch" 2>/dev/null); then ls=ok; else ls=err; fi
  sha=$(printf '%s\n' "$out" | awk 'NF{print $1; exit}'); [ -n "$sha" ] || sha=-
  printf '%s %s dir=%s pid=%s ls=%s sha=%s\n' "$s" "$l" "$d" "$p" "$ls" "$sha"
done`

// Probe implements reconcile.Prober. It is this file's host seam, through
// internal/benchsh (which calls testguard.RefuseHosts before the child starts).
func (p sshProber) Probe(ctx context.Context, b deal.Bench, cards []reconcile.Suspect) (map[string]reconcile.Evidence, error) {
	remote := p.Remote
	if remote == nil {
		remote = func(repo string) string { return "git@github.com:" + repo + ".git" }
	}
	var in bytes.Buffer
	for _, c := range cards {
		fields := []string{c.Sprint, c.Label, c.JobDir, c.Identity, remote(c.Repo), c.Branch}
		if hasBlank(fields) {
			continue // a field the bench cannot be asked about: no evidence
		}
		fmt.Fprintln(&in, strings.Join(fields, " "))
	}
	if in.Len() == 0 {
		return nil, nil
	}
	// benchsh: `bash -s --` reads only the line that execs bash on probeScript,
	// and probeScript reads the batch as the rest of stdin.
	t := benchsh.Target{Host: b.Target(), SSH: p.Program, ConnectTimeout: deal.DefaultConnectTimeout}
	cmd, err := benchsh.Command(ctx, t, probeScript, &in)
	if err != nil {
		return nil, fmt.Errorf("expire probe: bench %s: %w", b.Name, err)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("expire probe: bench %s: %w", b.Name, err)
	}
	return parseProbe(out, cards), nil
}

// parseProbe turns the bench's answer lines into evidence. A pid or a branch
// sha is an effect. Proven absence needs all three: no job dir, no process
// and a git ls-remote that answered with no such branch.
func parseProbe(out []byte, cards []reconcile.Suspect) map[string]reconcile.Evidence {
	byKey := map[string]reconcile.Suspect{}
	for _, c := range cards {
		byKey[c.Key()] = c
	}
	ev := map[string]reconcile.Evidence{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 6 {
			continue
		}
		c, ok := byKey[f[0]+"/"+f[1]]
		if !ok {
			continue
		}
		kv := map[string]string{}
		for _, x := range f[2:] {
			k, v, _ := strings.Cut(x, "=")
			kv[k] = v
		}
		var e reconcile.Evidence
		if kv["pid"] != "" && kv["pid"] != "-" {
			e.LivePID = kv["pid"]
		}
		if kv["ls"] == "ok" && kv["sha"] != "" && kv["sha"] != "-" {
			e.Branch, e.PushedSHA = c.Branch, kv["sha"]
		}
		if !e.Effect() && kv["dir"] == "0" && kv["pid"] == "-" && kv["ls"] == "ok" && kv["sha"] == "-" {
			e.Absent = true
		}
		if e.Effect() || e.Absent {
			ev[c.Key()] = e
		}
	}
	return ev
}

// hasBlank reports a field that is empty or would split the bench's line.
func hasBlank(fields []string) bool {
	for _, f := range fields {
		if strings.TrimSpace(f) == "" || strings.ContainsAny(f, " \t\n") {
			return true
		}
	}
	return false
}
