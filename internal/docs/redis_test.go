package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNovaRedisSpecFirstSlice pins the nova-redis specification's contract
// terms: the `nova-redis` binary owning the instance (serve, and spill/recall
// scratch with TTL and owner prefixes) bound to localhost and the tailnet with
// auth from nova-secrets and the AOF on. Store keys are kept and scratch keys are
// not a record (the bus's streams have no other copy, SPEC-BUS.md); a missing
// file or a section missing one of the named contract terms is a bug.
func TestNovaRedisSpecFirstSlice(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-REDIS.md")
	require.NoError(t, err, "docs/SPEC-REDIS.md: %v", err)
	content := string(body)

	for _, want := range []string{
		"# nova-redis",
		"`nova-redis`",
		"`serve`",
		"`spill`",
		"`recall`",
		"TTL",
		"owner prefix",
		"localhost",
		"tailnet",
		"nova-secrets",
		"Persistence",
		"Store keys are kept; scratch keys are not a record",
	} {
		assert.Contains(t, content, want, "docs/SPEC-REDIS.md missing %q", want)
	}
}

// TestSpecRedisVerbBlockNamesEveryVerb pins the verb block of docs/SPEC-REDIS.md
// "The verbs" to the verbs the nova-redis binary declares: every tool.Verb in
// cmd/nova-redis, plus the skeleton's version and help, must be named in the
// block, so the normative spec cannot ship a verb the block omits.
func TestSpecRedisVerbBlockNamesEveryVerb(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile("../../docs/SPEC-REDIS.md")
	require.NoError(t, err, "docs/SPEC-REDIS.md: %v", err)
	block, ok := redisVerbBlock(string(spec))
	require.True(t, ok, "docs/SPEC-REDIS.md: no fenced verb block under \"## The verbs\"")

	named := map[string]bool{}
	for _, line := range strings.Split(block, "\n") {
		rest, ok := strings.CutPrefix(line, "nova-redis ")
		if !ok {
			continue
		}
		if head, _, ok := strings.Cut(rest, " --"); ok {
			rest = head
		}
		named[strings.TrimSpace(rest)] = true
	}

	declared, err := novaRedisDeclaredVerbs()
	require.NoError(t, err)
	require.NotEmpty(t, declared, "cmd/nova-redis declares no verbs")
	declared = append(declared, "version", "help")
	for _, verb := range declared {
		assert.True(t, named[verb], "docs/SPEC-REDIS.md verb block omits %q, which the binary declares", verb)
	}
}

// redisVerbBlock returns the fenced verb block under SPEC-REDIS.md's
// "## The verbs" heading.
func redisVerbBlock(spec string) (string, bool) {
	_, rest, ok := strings.Cut(spec, "## The verbs")
	if !ok {
		return "", false
	}
	_, rest, ok = strings.Cut(rest, "```")
	if !ok {
		return "", false
	}
	block, _, ok := strings.Cut(rest, "```")
	return block, ok
}

// verbNameRe matches the Name field of a cmd/nova-redis verb literal; the
// tool's own name carries a hyphen and does not match.
var verbNameRe = regexp.MustCompile(`(?m)^\s*Name:\s+"([a-z]+(?: [a-z]+)?)"`)

// novaRedisDeclaredVerbs lists the tool.Verb names cmd/nova-redis declares,
// from the Name fields of its non-test source files.
func novaRedisDeclaredVerbs() ([]string, error) {
	files, err := filepath.Glob("../../cmd/nova-redis/*.go")
	if err != nil {
		return nil, err
	}
	var verbs []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		for _, m := range verbNameRe.FindAllStringSubmatch(string(b), -1) {
			verbs = append(verbs, m[1])
		}
	}
	return verbs, nil
}
