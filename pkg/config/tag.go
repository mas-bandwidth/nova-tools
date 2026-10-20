package config

import (
	"fmt"
	"regexp"
)

// A tag is a word in nova-config's registry (KindTag): a machine declares it
// on its row or the member proves it on its heartbeat by running the tag's
// probe, and a card names the tags it needs on its TAGS: line (GATE-NEEDS is
// the compatibility alias). The registry is data: a new tag is one row and an
// apply, never a build (docs/SPEC-CONFIG.md, "The tag registry"; tla/Tags.tla).
//
// The sprint server keeps no copy of a tag: it reads the registry and the
// machine rows from Postgres on the tick's inventory read and the declared
// tags with the live beat. The machine's tags and gates_only (its exclusive
// tags) are Local fields: Postgres holds them and Redis never does.

// tagSeedNames are the tags migration 0048 seeds, the compiled facts
// machineFacts measures today (cmd/nova-swarm/facts.go). They are Kind.Seed so
// migrate's rows are never removed by the tag verbs, and SeedRows carry each
// row's fields.
var tagSeedNames = []string{
	"amd64", "arm64", "avx2", "avx512f", "darwin", "dotnet", "go",
	"java", "kvm", "landlock", "linux", "tla", "windows", "xdp",
}

// tagSeedRows are the seeded registry rows: today's compiled facts and
// toolchains, so adoption changes nothing. linux, darwin, windows, amd64,
// arm64, landlock and kvm are builtins the member measures itself; tla is a
// path; go, dotnet and java are commands; avx2, avx512f and xdp are declared
// because machineFacts does not measure them today.
var tagSeedRows = []Row{
	{Name: "linux", Fields: map[string]string{"description": "the member runs Linux", "source": "probed", "probe": "builtin", "builtin": "os"}},
	{Name: "darwin", Fields: map[string]string{"description": "the member runs macOS", "source": "probed", "probe": "builtin", "builtin": "os"}},
	{Name: "windows", Fields: map[string]string{"description": "the member runs Windows", "source": "probed", "probe": "builtin", "builtin": "os"}},
	{Name: "amd64", Fields: map[string]string{"description": "the member's ISA is amd64", "source": "probed", "probe": "builtin", "builtin": "isa"}},
	{Name: "arm64", Fields: map[string]string{"description": "the member's ISA is arm64", "source": "probed", "probe": "builtin", "builtin": "isa"}},
	{Name: "landlock", Fields: map[string]string{"description": "the Linux kernel runs landlock", "source": "probed", "probe": "builtin", "builtin": "landlock"}},
	{Name: "kvm", Fields: map[string]string{"description": "the Linux kernel's KVM answers the member's user", "source": "probed", "probe": "builtin", "builtin": "kvm"}},
	{Name: "tla", Fields: map[string]string{"description": "the pinned TLA+ tools are installed", "source": "probed", "probe": "path", "probe_path": "/opt/tla/tla2tools.jar"}},
	{Name: "go", Fields: map[string]string{"description": "the Go toolchain is on PATH", "source": "probed", "probe": "command", "probe_argv": "go version"}},
	{Name: "dotnet", Fields: map[string]string{"description": "the .NET SDK is on PATH", "source": "probed", "probe": "command", "probe_argv": "dotnet --version"}},
	{Name: "java", Fields: map[string]string{"description": "the Java runtime is on PATH", "source": "probed", "probe": "command", "probe_argv": "java -version"}},
	{Name: "avx2", Fields: map[string]string{"description": "the machine declares AVX2", "source": "declared"}},
	{Name: "avx512f", Fields: map[string]string{"description": "the machine declares AVX-512F", "source": "declared"}},
	{Name: "xdp", Fields: map[string]string{"description": "the machine declares XDP support", "source": "declared"}},
}

// probeFields are the fields only a probed tag may carry.
var probeFields = []string{"probe", "probe_os", "probe_path", "probe_argv", "probe_match", "probe_min", "probe_limit", "builtin"}

// dottedNumbers is a probe_min: dotted decimal numbers, no sign or exponent.
var dottedNumbers = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)

// checkTag is the tag kind's Check (docs/SPEC-CONFIG.md, "The tag registry"):
// a declared tag carries no probe field; a probed tag names exactly one probe
// shape, carries only that shape's fields, and its match compiles with at most
// one group and its minimum is dotted numbers. Every problem is named.
func checkTag(r Row) error {
	source := r.Fields["source"]
	if source != "declared" && source != "probed" {
		return nil
	}
	shape := r.Fields["probe"]
	if source == "declared" {
		for _, f := range probeFields {
			if r.Fields[f] != "" {
				return fmt.Errorf("tag %s is declared and carries --%s; a declared tag has no probe fields (make it --source probed, or clear --%s)", r.Name, f, f)
			}
		}
		return nil
	}
	if shape == "" {
		return fmt.Errorf("tag %s is probed and names no probe; want --probe one of path, command, limit, builtin", r.Name)
	}
	allowed := map[string]bool{"probe": true, "probe_os": true}
	switch shape {
	case "path":
		allowed["probe_path"] = true
		if r.Fields["probe_path"] == "" {
			return fmt.Errorf("tag %s has --probe path and no --probe_path; want --probe_path <a file or directory, ~ is the member user's home>", r.Name)
		}
	case "command":
		allowed["probe_argv"], allowed["probe_match"], allowed["probe_min"] = true, true, true
		if r.Fields["probe_argv"] == "" {
			return fmt.Errorf("tag %s has --probe command and no --probe_argv; want --probe_argv <argv, space separated>", r.Name)
		}
		if match := r.Fields["probe_match"]; match != "" {
			re, err := regexp.Compile(match)
			if err != nil {
				return fmt.Errorf("tag %s has --probe_match %q: %v", r.Name, match, err)
			}
			if re.NumSubexp() > 1 {
				return fmt.Errorf("tag %s has --probe_match %q with %d groups; want at most one, the version", r.Name, match, re.NumSubexp())
			}
		}
		if min := r.Fields["probe_min"]; min != "" && !dottedNumbers.MatchString(min) {
			return fmt.Errorf("tag %s has --probe_min %q; want dotted numbers, like 1.6", r.Name, min)
		}
	case "limit":
		allowed["probe_limit"], allowed["probe_min"] = true, true
		if r.Fields["probe_limit"] == "" {
			return fmt.Errorf("tag %s has --probe limit and no --probe_limit; want --probe_limit nofile", r.Name)
		}
		if min := r.Fields["probe_min"]; min != "" && !dottedNumbers.MatchString(min) {
			return fmt.Errorf("tag %s has --probe_min %q; want a whole number, like 65536", r.Name, min)
		}
	case "builtin":
		allowed["builtin"] = true
		if r.Fields["builtin"] == "" {
			return fmt.Errorf("tag %s has --probe builtin and no --builtin; want --builtin one of os, isa, landlock, kvm", r.Name)
		}
	default:
		return fmt.Errorf("tag %s has --probe %q; want one of path, command, limit, builtin", r.Name, shape)
	}
	for _, f := range probeFields {
		if !allowed[f] && r.Fields[f] != "" {
			return fmt.Errorf("tag %s has --%s, which does not belong to --probe %s", r.Name, f, shape)
		}
	}
	return nil
}
