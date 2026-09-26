package fleetbuild

// Convergence (#4050): builder, self and platform:<bench> are facts the fleet
// already states, so fleet:release is filled from them and never typed.
//
//	platform:<b>  bench:<b>:desired platform, else the machines registry's
//	              os/arch for <b>, else the platform of the nova-sprint version
//	              line <b> beats (bench:<b>:beat build), else what is stored
//	builder       the one machine in the registry whose roles carry services
//	              (the stack host builds the release), else what is stored
//	self          the one machine whose roles carry coordination (the
//	              coordinator's machine), else what is stored
//	tools         the release manifest: every nova-* the build produced
//	              (Deployer, after the build), else what is stored
//
// Every source is read in two pipelined round trips (fleet:release, benches;
// then per bench its desired platform and beat build), no SCAN, and every
// changed field is written in one HSET.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// MachinesEnv names the machines registry file (name<TAB>ssh<TAB>os/arch<TAB>
// roles...) convergence reads builder and self from; unset, they keep what
// fleet:release holds.
const MachinesEnv = "NOVA_FLEET_MACHINES"

// Machine is the part of one machines-registry line convergence reads.
type Machine struct {
	Name     string
	SSH      string // the ssh column: the host to reach it by, localhost for this machine
	OSArch   string // the os/arch column as written
	Platform string // <goos>-<goarch>, "" when the line's os/arch is not one we build
	Roles    []string
}

// Host is the ssh host for m: the registry's ssh column, else its name.
func (m Machine) Host() string {
	if m.SSH != "" {
		return m.SSH
	}
	return m.Name
}

// IsLocal says whether m is this machine (its ssh column names localhost).
func (m Machine) IsLocal() bool {
	return m.SSH == "localhost" || m.SSH == "127.0.0.1" || m.SSH == "::1"
}

// HasRole says whether m carries role.
func (m Machine) HasRole(role string) bool {
	for _, r := range m.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// platformOf maps a registry os/arch (linux/x64, darwin/arm64, darwin/amd64)
// or a version line's goos/goarch to the release platform, "" when none.
func platformOf(osArch string) string {
	goos, arch, ok := strings.Cut(strings.TrimSpace(osArch), "/")
	if !ok {
		return ""
	}
	if arch == "x64" || arch == "x86_64" {
		arch = "amd64"
	}
	p := goos + "-" + arch
	if !platformRe.MatchString(p) {
		return ""
	}
	return p
}

// ParseMachines reads a machines registry: tab-separated lines, # comments
// and blank lines skipped, the name, os/arch and roles columns read. It is
// lenient about roles it does not know (convergence only asks for services
// and coordination) and strict about the shape: fewer than four columns is a
// refusal naming the line.
func ParseMachines(r io.Reader) ([]Machine, error) {
	var out []Machine
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			return nil, refused("machines line %d has %d columns, want name, ssh, os/arch, roles, ...", n, len(f))
		}
		m := Machine{Name: strings.TrimSpace(f[0]), SSH: strings.TrimSpace(f[1]), OSArch: strings.TrimSpace(f[2]), Platform: platformOf(f[2])}
		if !nameRe.MatchString(m.Name) {
			return nil, refused("machines line %d names %q, not a bench name", n, m.Name)
		}
		for _, r := range strings.Split(f[3], ",") {
			if r = strings.TrimSpace(r); r != "" {
				m.Roles = append(m.Roles, r)
			}
		}
		out = append(out, m)
	}
	return out, sc.Err()
}

// ReadMachinesFile reads the registry at path; "" reads nothing.
func ReadMachinesFile(path string) ([]Machine, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseMachines(f)
}

// Facts is everything convergence reads.
type Facts struct {
	Release  map[string]string // fleet:release as stored
	Benches  []string          // the benches set, sorted
	Desired  map[string]string // bench -> bench:<b>:desired platform
	Beat     map[string]string // bench -> bench:<b>:beat build (a version line); only benches beating
	Machines []Machine         // the machines registry; nil when none was read
}

// ReadFacts reads fleet:release, the benches set, and every bench's desired
// platform and beat build: two pipelined round trips. The self named in
// fleet:release is read with the benches.
func ReadFacts(ctx context.Context, c *redis.Client) (Facts, error) {
	pipe := c.Pipeline()
	h := pipe.HGetAll(ctx, ConfigKey)
	m := pipe.SMembers(ctx, BenchesKey)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Facts{}, err
	}
	f := Facts{Release: h.Val(), Benches: m.Val(), Desired: map[string]string{}, Beat: map[string]string{}}
	sort.Strings(f.Benches)
	names := append([]string{}, f.Benches...)
	if s := f.Release["self"]; s != "" && !contains(names, s) {
		names = append(names, s)
	}
	if len(names) == 0 {
		return f, nil
	}
	pipe = c.Pipeline()
	desired := make([]*redis.StringCmd, len(names))
	exists := make([]*redis.IntCmd, len(names))
	beat := make([]*redis.StringCmd, len(names))
	for i, b := range names {
		desired[i] = pipe.HGet(ctx, "bench:"+b+":desired", "platform")
		exists[i] = pipe.Exists(ctx, "bench:"+b+":beat")
		beat[i] = pipe.HGet(ctx, "bench:"+b+":beat", "build")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Facts{}, err
	}
	for i, b := range names {
		if v := desired[i].Val(); v != "" {
			f.Desired[b] = v
		}
		if exists[i].Val() == 1 {
			f.Beat[b] = beat[i].Val()
		}
	}
	return f, nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// onlyWithRole is the one machine carrying role, "" when none or several do.
func onlyWithRole(ms []Machine, role string) string {
	found := ""
	for _, m := range ms {
		if m.HasRole(role) {
			if found != "" {
				return ""
			}
			found = m.Name
		}
	}
	return found
}

// Converge is the fields of fleet:release the facts say differently from what
// is stored, as key -> value; empty when the release already agrees.
func Converge(f Facts) map[string]string {
	want := map[string]string{}
	if b := onlyWithRole(f.Machines, "services"); b != "" {
		want["builder"] = b
	}
	self := f.Release["self"]
	if s := onlyWithRole(f.Machines, "coordination"); s != "" {
		want["self"], self = s, s
	}
	machine := map[string]string{}
	for _, m := range f.Machines {
		machine[m.Name] = m.Platform
	}
	names := append([]string{}, f.Benches...)
	if self != "" && !contains(names, self) {
		names = append(names, self)
	}
	for _, b := range names {
		p := f.Desired[b]
		if !platformRe.MatchString(p) {
			p = machine[b]
		}
		if p == "" {
			if bf, ok := buildinfo.Parse(f.Beat[b]); ok {
				p = platformOf(bf.Platform)
			}
		}
		if p != "" {
			want["platform:"+b] = p
		}
	}
	out := map[string]string{}
	for k, v := range want {
		if f.Release[k] != v {
			out[k] = v
		}
	}
	return out
}

// Fields renders converged fields as sorted key=value words, for a receipt.
func Fields(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		keys[i] = k + "=" + m[k]
	}
	return strings.Join(keys, " ")
}

// WriteConverged writes the converged fields in one HSET; nothing when empty.
func WriteConverged(ctx context.Context, c *redis.Client, m map[string]string) error {
	if len(m) == 0 {
		return nil
	}
	args := make([]any, 0, 2*len(m))
	for k, v := range m {
		args = append(args, k, v)
	}
	if err := c.HSet(ctx, ConfigKey, args...).Err(); err != nil {
		return fmt.Errorf("converge %s: %w", ConfigKey, err)
	}
	return nil
}

// Apply is cfg with the converged fields laid over it (what a plan uses
// before, or instead of, the write).
func Apply(cfg Config, m map[string]string) Config {
	plats := map[string]string{}
	for k, v := range cfg.Platforms {
		plats[k] = v
	}
	cfg.Platforms = plats
	for k, v := range m {
		switch {
		case k == "builder":
			cfg.Builder = v
		case k == "self":
			cfg.Self = v
		case strings.HasPrefix(k, "platform:"):
			cfg.Platforms[strings.TrimPrefix(k, "platform:")] = v
		}
	}
	return cfg
}
