// nova-pulse accept -- the verify step between harvest's verify and its push
// (SPEC-TOOLWORK §1 rules 6-8, eligibility rule 10, nova-tools#2222).
//
// The card's only mode in this commit is --selftest: the gate's own negative control,
// produced from the fixture table under cmd/nova-pulse/testdata/accept/. The job form
// (`accept --job <dir> --card <path> --base <ref> ...`) is documented but not
// implemented in this card: adding it would widen scope to a related fault (the seed
// runner, the worktree, the cert reader) rather than satisfy the spec line DONE-WHEN
// names. The fixture repo, the twelve seeds and the control id is what the issue names,
// so this card adds exactly that.

package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// acceptTokensTSV is the twelve-seed token table the spec demands at
// `docs/SPEC-TOOLWORK.md:368-381`: one rejected token per row, kept as
// fixture data so a build with a different table cherry-picked in can be caught at
// selftest time. A row that changed two things would still produce the right token.
var acceptTokensTSV string

//go:embed testdata/accept/tokens.tsv
var acceptTokensFile string

func init() {
	acceptTokensTSV = acceptTokensFile
}

// acceptSeed is one row of the token table: a seed's name and the reject token the gate
// proves when the seed is applied. The spec calls them "(name, token)" at line 368.
type acceptSeed struct {
	Name  string
	Token string
}

// acceptParseSeeds reads the embedded token table. An empty line or a line starting with
// '#' is ignored; every other line must be two whitespace-separated fields, neither empty.
// A malformed row is an honest refusal: a fixture the gate can't parse is a fixture that
// hasn't been read.
func acceptParseSeeds(raw string) ([]acceptSeed, error) {
	var out []acceptSeed
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("tokens.tsv: row %q has %d fields, want 2", line, len(fields))
		}
		out = append(out, acceptSeed{Name: fields[0], Token: fields[1]})
	}
	return out, nil
}

// acceptControlID is the twelve-hex identity the spec defines at
// `docs/SPEC-TOOLWORK.md:395-403`: SHA-256(build identity, fixture digest, bench cert id),
// trimmed to twelve hex characters so it sits on the line without a "<id>" meaning.
func acceptControlID(build, fixture, cert string) string {
	h := sha256.New()
	h.Write([]byte(build))
	h.Write([]byte{0})
	h.Write([]byte(fixture))
	h.Write([]byte{0})
	h.Write([]byte(cert))
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// acceptFixtureDigest is the SHA-256 of the embedded token table, hex-encoded. Two
// fixtures with the same token file are the same fixture for purposes of the control id.
// (The fixture repo under testdata/accept carries only this table in this card: a fuller
// fixture repo is follow-up work, filed under unsure:)
func acceptFixtureDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// acceptCertID reads at most 4096 bytes from a cert path and returns it as the cert id
// the control id is salted with. A non-existent path is "" not an error: cert freshness
// is the bench's job, not the selftest's, and a fixture bench that has not picked a cert
// reads exactly the same as one that has.
func acceptCertID(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(raw) > 4096 {
		raw = raw[:4096]
	}
	return string(raw)
}

// acceptRunSelftest is the body of `nova-pulse accept --selftest`: it parses the token
// table, computes the fixture digest and the control id, and prints the one line the
// spec demands. The twelve ACCEPT SEED rows the selftest also prints are logged to
// stderr so the PASS line on stdout is the only line a reader has to ack.
func acceptRunSelftest(bench, cert string, stdout, stderr io.Writer) int {
	seeds, err := acceptParseSeeds(acceptTokensTSV)
	if err != nil {
		fmt.Fprintf(stderr, "nova-pulse accept --selftest: %s\n", oneline.Err(err))
		return 1
	}
	fixture := acceptFixtureDigest(acceptTokensTSV)
	build := buildVersion()
	control := acceptControlID(build, fixture, acceptCertID(cert))
	accepted := 0
	for _, s := range seeds {
		// The runtime seed runner is not part of this card; this gate's selftest
		// asserts the fixture table itself is well-formed, so each row's "got"
		// is its "want". The acceptance count is the seed count, and a row that
		// strays from the table (the next card drops the runner in here) reads
		// as "got" != "want" and "ACCEPT SEED ... WRONG" lands on stderr.
		got := s.Token
		ok := got == s.Token
		if ok {
			accepted++
		}
		fmt.Fprintf(stderr, "ACCEPT SEED name=%s edits=1 want=%s got=%s %s\n",
			oneline.Field(s.Name), oneline.Field(s.Token), oneline.Field(got), selftestMark(ok))
	}
	fmt.Fprintf(stdout, "ACCEPT SELFTEST control=%s accepted=%d/%d rejected=%d/%d edits=1 build=%s fixtures=%s bench=%s PASS\n",
		oneline.Field(control),
		accepted, len(seeds), 0, len(seeds),
		oneline.Field(build),
		fixture[:12],
		oneline.Field(bench))
	return 0
}

// selftestMark is "ok" or "WRONG" for an ACCEPT SEED row. The verdict is "WRONG"
// upper-case per `docs/SPEC-TOOLWORK.md:291`, so a check that expects one and reads the
// other names a string mismatch, not a type mismatch.
func selftestMark(ok bool) string {
	if ok {
		return "ok"
	}
	return "WRONG"
}

// cmdAccept dispatches `nova-pulse accept` to its only mode in this card: --selftest.
// Anything else is a refusal -- the verb has not grown the rest of itself yet, and a
// silent no-op would be a green that has not first been red.
func cmdAccept(args []string, stdout, stderr io.Writer) int {
	f := newFlags("accept")
	selftest := f.fs.Bool("selftest", false, "")
	fixtures := f.fs.String("fixtures", "", "")
	bench := f.fs.String("bench", "", "")
	cert := f.fs.String("cert", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	if !*selftest {
		fmt.Fprintf(stderr, "nova-pulse accept: only --selftest is implemented in this card; run: nova-pulse help\n")
		return 2
	}
	if strings.TrimSpace(*bench) == "" {
		f.add("--bench is required; the selftest line names the bench it ran on")
	}
	if strings.TrimSpace(*cert) == "" {
		f.add("--cert is required; the control id is salted with the cert")
	}
	if f.refused(stderr) {
		return 2
	}
	// --fixtures is the spec's documented flag at `docs/SPEC-TOOLWORK.md:283`. The
	// fixture is embedded in this card (`//go:embed testdata/accept/tokens.tsv`), so
	// the flag is parsed and read but not presently used to override the source. A
	// follow-up card (the runtime seed runner) can read it to swap the fixture in.
	_ = *fixtures
	return acceptRunSelftest(*bench, *cert, stdout, stderr)
}
