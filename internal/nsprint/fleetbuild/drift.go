package fleetbuild

// The deploy duty (#4050): a landing into dev writes fleet:release version
// and commit (land merge), and the reconciler runs `fleet build` for the
// benches whose beat still names another version, so a landing deploys itself
// within one tick and nobody runs a deploy chain by hand.
//
// One pass: the facts (ReadFacts, two round trips), convergence of builder,
// self and platform:<b> (one HSET when anything changed), then per beating
// bench its beat version (field two of the nova-sprint version line) against
// fleet:release version. A bench with no live beat is no evidence and is
// never drift (nova-update report --store reads the beats the same way). With
// drift, one SET NX PX on DeployKey claims the deploy of that version, and the
// Start seam runs `fleet build --bench <drifting benches>` in its own session;
// the claim holds until the build's own bound passes, so a failed deploy is
// retried after it and a running one is never started twice.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// DeployKey is the claim on one deploy: its value is the version being
// deployed, its TTL DeployClaim.
const DeployKey = "fleet:release:deploy"

// DeployClaim bounds one deploy: the build's bound plus the installs'.
const DeployClaim = BuildTimeout + 5*time.Minute

// BenchDrift is one beating bench against the release.
type BenchDrift struct {
	Bench, Beat string // Beat is the version identity its beat names ("" when the beat has no build)
}

// Drift is one pass's reading.
type Drift struct {
	Version, Commit string
	Converged       map[string]string // fields written to fleet:release this pass
	Current         []string          // beating benches on the release
	Drifting        []BenchDrift      // beating benches on another version
	Quiet           []string          // registered benches with no live beat
	Claimed         string            // the version a running deploy holds, when not ours
	// Outcome is what the pass did: START (a deploy started), WOULD (dry
	// run with drift), HELD (a deploy already claimed), CURRENT (no
	// beating bench drifts) or NOPLAN (fleet:release has no valid version
	// and commit yet).
	Outcome string
}

// Duty is the deploy duty.
type Duty struct {
	Client   *redis.Client
	Machines []Machine // the registry builder and self converge from; nil keeps them
	// Start runs the deploy of version on benches; nil or DryRun starts
	// nothing and the pass prints WOULD INSTALL lines.
	Start  func(ctx context.Context, version string, benches []string) error
	DryRun bool
	Out    io.Writer
	// last is the receipt printed last, so an idle loop prints nothing twice.
	last string
}

func (d *Duty) printf(format string, a ...any) {
	if d.Out != nil {
		fmt.Fprintf(d.Out, format, a...)
	}
}

// Read converges fleet:release and reads the fleet's beats against it; it
// writes only the converged fields.
func (d *Duty) Read(ctx context.Context) (Drift, error) {
	f, err := ReadFacts(ctx, d.Client)
	if err != nil {
		return Drift{}, err
	}
	f.Machines = d.Machines
	var r Drift
	if !d.DryRun {
		r.Converged = Converge(f)
		if err := WriteConverged(ctx, d.Client, r.Converged); err != nil {
			return Drift{}, err
		}
	}
	r.Version, r.Commit = f.Release["version"], f.Release["commit"]
	for _, b := range f.Benches {
		line, beating := f.Beat[b]
		if !beating {
			r.Quiet = append(r.Quiet, b)
			continue
		}
		v := strings.TrimSpace(line)
		if bf, ok := buildinfo.Parse(line); ok {
			v = bf.Version
		}
		if v == r.Version {
			r.Current = append(r.Current, b)
			continue
		}
		r.Drifting = append(r.Drifting, BenchDrift{Bench: b, Beat: v})
	}
	return r, nil
}

// Pass is one duty pass; the Drift's Outcome says what it did. A release
// that is not a valid plan (no version or commit yet) is no work: the landing
// writes them.
func (d *Duty) Pass(ctx context.Context) (Drift, error) {
	r, err := d.Read(ctx)
	if err != nil {
		return r, err
	}
	if len(r.Converged) > 0 {
		d.printf("FLEET DEPLOY CONVERGED %s\n", Fields(r.Converged))
	}
	sm := versionRe.FindStringSubmatch(r.Version)
	if sm == nil || !commitRe.MatchString(r.Commit) || !strings.HasPrefix(r.Commit, sm[1]) {
		d.last, r.Outcome = "", "NOPLAN"
		return r, nil
	}
	if len(r.Drifting) == 0 {
		d.last, r.Outcome = "", "CURRENT"
		return r, nil
	}
	benches := make([]string, len(r.Drifting))
	for i, b := range r.Drifting {
		benches[i] = b.Bench
	}
	sort.Strings(benches)
	if d.DryRun || d.Start == nil {
		for _, b := range r.Drifting {
			d.printf("FLEET DEPLOY WOULD INSTALL %s beat=%s want=%s\n", b.Bench, orNone(b.Beat), r.Version)
		}
		d.printf("FLEET DEPLOY DRY-RUN version=%s commit=%s drift=%d current=%d quiet=%d\n",
			r.Version, r.Commit[:12], len(r.Drifting), len(r.Current), len(r.Quiet))
		r.Outcome = "WOULD"
		return r, nil
	}
	ok, err := d.Client.SetNX(ctx, DeployKey, r.Version, DeployClaim).Result()
	if err != nil {
		return r, fmt.Errorf("claim %s: %w", DeployKey, err)
	}
	if !ok {
		held, err := d.Client.Get(ctx, DeployKey).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return r, err
		}
		r.Claimed, r.Outcome = held, "HELD"
		line := fmt.Sprintf("FLEET DEPLOY HELD version=%s by=%s drift=%d\n", r.Version, orNone(held), len(r.Drifting))
		if line != d.last {
			d.printf("%s", line)
			d.last = line
		}
		return r, nil
	}
	if err := d.Start(ctx, r.Version, benches); err != nil {
		// Give the claim back so the next pass tries again.
		d.Client.Del(ctx, DeployKey)
		return r, fmt.Errorf("start fleet build %s: %w", r.Version, err)
	}
	d.last, r.Outcome = "", "START"
	d.printf("FLEET DEPLOY START version=%s commit=%s benches=%s\n", r.Version, r.Commit[:12], strings.Join(benches, ","))
	return r, nil
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
