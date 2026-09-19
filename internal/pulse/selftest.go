package pulse

// The gate's own negative control: it must be seen red before its green counts.
//
// SPEC-TOOLWORK.md §1 rules 6-8 (PR #1637), issue #1649. `accept --selftest` builds a
// repository from the shipped fixture -- one known-good fix, which must be ACCEPT OK --
// and then, one branch per seed, puts one deliberate defect on the fix and runs the gate
// again; each of the twelve must be ACCEPT REJECT with ITS token and no other. Every seed
// is one edit and the count is asserted here from what git applied, never from the
// fixture: a seed that changed nothing proves the gate red on nothing, and a seed that
// changed two things does not say which one the gate caught.
//
// The control is an id: sha12 of (the gate binary's build identity, the fixture tree's
// digest, the bench certification id). A passing run is put on file under
// <root>/accept/control/<id>, and `accept` refuses to print ACCEPT OK unless that file
// exists for the id it computed (it runs the selftest itself when none does). A new
// build, a changed fixture or a re-certified bench is a new id and a new selftest.
//
// gate-weakened is the one token this cannot prove -- it needs a second gate to weaken
// -- and accept.go's gateWeakened step, with its own red test, holds it.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	hyg "github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/review"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// SelftestInput is everything `nova-pulse accept --selftest` takes, held apart from flag
// parsing.
type SelftestInput struct {
	Fixtures fs.FS  // the fixture tree: cmd/nova-pulse/testdata/accept, embedded or --fixtures
	Root     string // the swarm root <root>/accept/control/<id> hangs under
	Bench    string
	Cert     string
	Sandbox  string
	Build    string // the gate binary's build identity; empty asks the running binary
	Timeout  time.Duration
	Max      int
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
}

// selftestSeeds is the spec's table, in its order: the twelve tokens of the common gate.
var selftestSeeds = []string{"fix-reverted", "vacuous", "no-test", "wrong-author", "stray", "wide", "secret", "broken", "vetted", "renamed", "wrong-name", "skipped"}

// controlDir is where a passing selftest is put on file, under the root.
const controlDir = "accept/control"

// ControlID is the first twelve hex of the SHA-256 over the three things that make a
// gate's green mean something: which build judged, which fixture it was seen red on,
// and which bench certification it ran under. Any of the three changing is a new id.
func ControlID(build, fixtures, cert string) string {
	sum := sha256.Sum256([]byte(build + "\n" + fixtures + "\n" + cert + "\n"))
	return hex.EncodeToString(sum[:])[:12]
}

// FixtureDigest is sha12 over every file of the fixture tree, path and bytes, in path
// order: a byte-identical copy digests the same, and one changed byte anywhere is a new
// fixture and therefore a new control.
func FixtureDigest(fsys fs.FS) (string, error) {
	if fsys == nil {
		return "", errors.New("no fixture tree")
	}
	h := sha256.New()
	var paths []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("could not walk the fixture tree: %v", err)
	}
	if len(paths) == 0 {
		return "", errors.New("the fixture tree holds no files")
	}
	sort.Strings(paths)
	for _, p := range paths {
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return "", fmt.Errorf("could not read fixture %s: %v", p, err)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(raw))
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// controlOnFile says whether a passing selftest for this id is under the root.
func controlOnFile(root, id string) bool {
	if root == "" || id == "" || id == "-" {
		return false
	}
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(controlDir), id))
	return err == nil
}

func writeControl(root, id, line string) error {
	dir := filepath.Join(root, filepath.FromSlash(controlDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id), []byte(line+"\n"), 0o644)
}

// seedSpec is one seeds/<name>/seed.txt: what kind of edit, what token it must draw, and
// the kind's own fields.
type seedSpec struct {
	name, kind, want  string
	file, text, after string // file: the file added (file) or edited (insert, hunk)
	author, line      string // author: the re-authoring identity; card: the header line
	patch             []byte // patch: the unified diff
	fields            map[string]string
}

type selftest struct {
	in       SelftestInput
	start    time.Time
	build    string
	digest   string
	certID   string
	control  string
	run      string // the selftest's own directory under <root>/accept
	job      string // the fixture repository: <run>/slot/jobs/FIXTURE
	base     string // the fixture base commit's full sha: the gate takes a sha, never a ref
	card     string // <run>/card.md
	identity hyg.Identity
	cardText string
	seeds    []seedSpec
	accepted int
	rejected int
	rows     []string
	notes    []string
}

// Selftest runs the gate over its fixture and prints one ACCEPT SEED line per seed and
// one ACCEPT SELFTEST line. Exit 0 is PASS (and the control is on file), 1 is FAIL, 2 is
// REFUSED.
func Selftest(in SelftestInput) int {
	if in.Now == nil {
		in.Now = time.Now
	}
	if in.Timeout <= 0 {
		in.Timeout = 30 * time.Minute
	}
	s := &selftest{in: in, start: in.Now(), build: in.Build}
	if s.build == "" {
		s.build = buildinfo.Version("")
	}
	code := s.run0()
	return code
}

func (s *selftest) refused(reason, remedy string) int {
	fmt.Fprintf(s.in.Stderr, "ACCEPT REFUSED: %s (%s)\n", oneline.Escape(reason), oneline.Escape(remedy))
	return 2
}

func (s *selftest) run0() int {
	in := s.in
	if in.Fixtures == nil {
		return s.refused("no fixture tree", "pass --fixtures <dir>, or use the binary's own embedded fixtures")
	}
	if in.Root == "" {
		return s.refused("no root", "pass --root <dir>: the passing selftest is put on file under <root>/accept/control/")
	}
	digest, err := FixtureDigest(in.Fixtures)
	if err != nil {
		return s.refused(err.Error(), "the fixture tree is cmd/nova-pulse/testdata/accept or a copy of it")
	}
	s.digest = digest
	cardRaw, err := fs.ReadFile(in.Fixtures, "card.md")
	if err != nil {
		return s.refused("fixture has no card.md", "the fixture card is the header the gate reads")
	}
	s.cardText = string(cardRaw)
	idRaw, err := fs.ReadFile(in.Fixtures, "identity.txt")
	if err != nil {
		return s.refused("fixture has no identity.txt", "the identity the fixture commits carry")
	}
	id, ok := parseIdentity(strings.TrimSpace(string(idRaw)))
	if !ok {
		return s.refused("identity.txt is not `Name <email>`", "one line, the pool identity the fixture commits carry")
	}
	s.identity = id
	seeds, err := readSeeds(in.Fixtures)
	if err != nil {
		return s.refused(err.Error(), "seeds/<name>/seed.txt, one per token of the spec's table")
	}
	s.seeds = seeds

	// The card's legs decide which certification the bench needs. The fixture card
	// says LEGS: go, and the certification is checked once here and once more by every
	// inner accept.
	legs := []string{"go"}
	if h, err := parseCardHeaderText(s.cardText); err == nil {
		legs = h.LegsOrDefault()
	}
	certID, why := acceptReadCert(in.Cert, in.Bench, legs)
	if why != "" {
		return s.refused("bench uncertified: "+why, "a certification record for this bench with the fixture's legs; until nova-pulse certify exists, a hand-written `bench=<name> legs=go,git` line")
	}
	s.certID = certID
	s.control = ControlID(s.build, s.digest, s.certID)

	acceptDir := filepath.Join(in.Root, "accept")
	if err := os.MkdirAll(acceptDir, 0o755); err != nil {
		return s.refused(fmt.Sprintf("could not make %s: %v", acceptDir, err), "pass a root this verb may write under")
	}
	run, err := os.MkdirTemp(acceptDir, "selftest-"+s.control+"-")
	if err != nil {
		return s.refused(fmt.Sprintf("could not make the selftest directory: %v", err), "pass a root this verb may write under")
	}
	s.run = run
	defer func() { _ = safepath.RemoveUnder(acceptDir, run) }()

	if err := s.buildFixtureRepo(); err != nil {
		return s.refused("could not build the fixture repository: "+err.Error(), "git must be on PATH and the fixture tree whole")
	}

	// The known-good fix, which must be ACCEPT OK.
	got, _ := s.gate(s.card)
	if got == "ACCEPT" {
		s.accepted = 1
	} else {
		s.note("the known-good fix was not accepted: " + got)
	}

	for _, seed := range s.seeds {
		edits, err := s.plant(seed)
		if err != nil {
			var count *seedCountError
			if errors.As(err, &count) {
				return s.refused(fmt.Sprintf("seed %s made %d edits, want exactly 1", seed.name, count.edits), "a seed is one changed, added, removed or moved line, or one whole object")
			}
			return s.refused(fmt.Sprintf("seed %s could not be planted: %v", seed.name, err), "the seed must apply on the fixture's fix")
		}
		card := s.card
		if seed.kind == "card" {
			card = filepath.Join(s.run, "card-"+seed.name+".md")
			if err := os.WriteFile(card, []byte(replaceHeaderLine(s.cardText, seed.line)), 0o644); err != nil {
				return s.refused(err.Error(), "the selftest directory must be writable")
			}
		}
		got, inner := s.gate(card)
		verdict := "ok"
		if got != seed.want {
			verdict = "WRONG"
			s.note(fmt.Sprintf("seed %s: %s", seed.name, inner))
		} else {
			s.rejected++
		}
		s.rows = append(s.rows, fmt.Sprintf("ACCEPT SEED name=%s edits=%d want=%s got=%s %s",
			oneline.Field(seed.name), edits, oneline.Field(seed.want), oneline.Field(got), verdict))
	}

	pass := s.accepted == 1 && s.rejected == len(s.seeds)
	word := "FAIL"
	if pass {
		word = "PASS"
	}
	for _, row := range s.rows {
		fmt.Fprintln(in.Stdout, row)
	}
	if len(s.notes) > 0 {
		max := in.Max
		if max < 0 {
			max = 0
		}
		shown := 0
		for _, n := range s.notes {
			if max > 0 && shown >= max {
				fmt.Fprintf(in.Stdout, "ACCEPT MORE notes=%d shown=%d (--max 0 prints every note)\n", len(s.notes), shown)
				break
			}
			fmt.Fprintf(in.Stdout, "ACCEPT NOTE %s\n", oneline.Escape(n))
			shown++
		}
	}
	line := fmt.Sprintf("ACCEPT SELFTEST control=%s accepted=%d/1 rejected=%d/%d edits=1 build=%s fixtures=%s bench=%s %s",
		s.control, s.accepted, s.rejected, len(s.seeds), oneline.Field(s.build), s.digest, oneline.Field(in.Bench), word)
	fmt.Fprintln(in.Stdout, line)
	if !pass {
		return 1
	}
	if err := writeControl(in.Root, s.control, line); err != nil {
		return s.refused(fmt.Sprintf("the selftest passed and could not be put on file: %v", err), "the root must be writable")
	}
	return 0
}

func (s *selftest) note(n string) { s.notes = append(s.notes, n) }

// gate runs accept over the fixture repository's HEAD with the given card and answers
// the token it drew: the reject reason, "ACCEPT" for OK, "ABSTAIN:<reason>" or "REFUSED".
// The inner run is told its control id and not to look for one on file, because this IS
// the run that puts it there.
func (s *selftest) gate(card string) (string, string) {
	var out, errb bytes.Buffer
	Accept(AcceptInput{
		Job: s.job, Card: card, Base: s.base, Bench: s.in.Bench, Cert: s.in.Cert,
		Identities: []hyg.Identity{s.identity}, Sandbox: s.in.Sandbox, Root: s.in.Root,
		Timeout: s.in.Timeout, Max: 0, Stdout: &out, Stderr: &errb, Now: s.in.Now,
		control: s.control, noControlCheck: true, Build: s.build,
	})
	for _, l := range strings.Split(out.String(), "\n") {
		switch {
		case strings.HasPrefix(l, "ACCEPT OK "):
			return "ACCEPT", l
		case strings.HasPrefix(l, "ACCEPT REJECT "):
			return fieldOf(l, "reason="), l
		case strings.HasPrefix(l, "ACCEPT ABSTAIN "):
			return "ABSTAIN:" + fieldOf(l, "reason="), l
		}
	}
	for _, l := range strings.Split(errb.String(), "\n") {
		if strings.HasPrefix(l, "ACCEPT REFUSED") {
			return "REFUSED", l
		}
	}
	return "-", strings.TrimSpace(out.String() + errb.String())
}

func fieldOf(line, key string) string {
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key); ok {
			return v
		}
	}
	return "-"
}

// buildFixtureRepo makes <run>/slot/jobs/FIXTURE: main holds the base, card holds the fix.
func (s *selftest) buildFixtureRepo() error {
	s.job = filepath.Join(s.run, "slot", "jobs", "FIXTURE")
	if err := os.MkdirAll(s.job, 0o755); err != nil {
		return err
	}
	s.card = filepath.Join(s.run, "card.md")
	if err := os.WriteFile(s.card, []byte(s.cardText), 0o644); err != nil {
		return err
	}
	if _, err := s.git(nil, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	if err := copyTree(s.in.Fixtures, "base", s.job); err != nil {
		return err
	}
	if err := s.commit(nil, "base"); err != nil {
		return err
	}
	// The gate takes the base as a full sha and refuses a ref (the cold read of
	// f927bccc, HIGH 2); the selftest owns this repository and reads the sha itself.
	base, err := s.git(nil, "rev-parse", "main^{commit}")
	if err != nil {
		return err
	}
	s.base = base
	if _, err := s.git(nil, "checkout", "-q", "-b", "card"); err != nil {
		return err
	}
	if err := copyTree(s.in.Fixtures, "fix", s.job); err != nil {
		return err
	}
	return s.commit(nil, "the known-good fix, red test first")
}

func copyTree(fsys fs.FS, from, to string) error {
	return fs.WalkDir(fsys, from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, from), "/")
		out := filepath.Join(to, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, raw, 0o644)
	})
}

// git runs one git command in the fixture repository with the fixture identity and no
// bench config; env adds to or overrides that identity (the wrong-author seed).
func (s *selftest) git(env []string, args ...string) (string, error) {
	// Hook-off and monitor-off, as every git call the gate makes (cold read HIGH 1); this
	// repository is the selftest's own, but the rule has no exceptions.
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
	cmd.Dir = s.job
	cmd.Env = append(append(os.Environ(),
		"GIT_AUTHOR_NAME="+s.identity.Name, "GIT_AUTHOR_EMAIL="+s.identity.Email,
		"GIT_COMMITTER_NAME="+s.identity.Name, "GIT_COMMITTER_EMAIL="+s.identity.Email,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *selftest) commit(env []string, msg string) error {
	if _, err := s.git(nil, "add", "-A"); err != nil {
		return err
	}
	_, err := s.git(env, "commit", "-q", "--allow-empty", "-m", msg)
	return err
}

type seedCountError struct{ edits int }

func (e *seedCountError) Error() string { return fmt.Sprintf("%d edits", e.edits) }

// plant puts one seed on a fresh branch off the fix and answers how many edits it made,
// by the seed's kind: a line count for patch, insert and file seeds (review.CountEdits,
// the one definition), a hunk count for a hunk seed, and one for the whole-object seeds
// (a commit re-authored, a header line replaced).
func (s *selftest) plant(seed seedSpec) (int, error) {
	if _, err := s.git(nil, "checkout", "-q", "-B", "seed-"+seed.name, "card"); err != nil {
		return 0, err
	}
	var env []string
	edits := 0
	switch seed.kind {
	case "patch":
		patch := filepath.Join(s.run, "seed-"+seed.name+".patch")
		if err := os.WriteFile(patch, seed.patch, 0o644); err != nil {
			return 0, err
		}
		if _, err := s.git(nil, "apply", "--whitespace=nowarn", patch); err != nil {
			return 0, fmt.Errorf("the seed does not apply at the fix: %v", err)
		}
	case "insert":
		p := filepath.Join(s.job, filepath.FromSlash(seed.file))
		raw, err := os.ReadFile(p)
		if err != nil {
			return 0, err
		}
		text := strings.ReplaceAll(seed.text, "{{KEY}}", fixtureKeyShape())
		lines := strings.Split(string(raw), "\n")
		done := false
		for i, l := range lines {
			if l == seed.after {
				lines = append(lines[:i+1], append([]string{text}, lines[i+1:]...)...)
				done = true
				break
			}
		}
		if !done {
			return 0, fmt.Errorf("insert seed: no line %q in %s", seed.after, seed.file)
		}
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return 0, err
		}
	case "file":
		p := filepath.Join(s.job, filepath.FromSlash(seed.file))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(p, []byte(seed.text+"\n"), 0o644); err != nil {
			return 0, err
		}
	case "hunk":
		// The card's hunk in this file dropped: the file put back as the base has it.
		if _, err := s.git(nil, "checkout", "main", "--", seed.file); err != nil {
			return 0, err
		}
	case "author":
		name, email, ok := strings.Cut(seed.author, "<")
		if !ok {
			return 0, errors.New("author seed: want `Name <email>`")
		}
		env = []string{"GIT_AUTHOR_NAME=" + strings.TrimSpace(name), "GIT_AUTHOR_EMAIL=" + strings.TrimSpace(strings.TrimSuffix(email, ">"))}
		// The commit is the same fix, re-authored: the tree is unchanged.
		if _, err := s.git(nil, "reset", "-q", "--soft", "main"); err != nil {
			return 0, err
		}
		edits = 1
	case "card":
		if seed.line == "" {
			return 0, errors.New("card seed: no line=")
		}
		edits = 1
		return edits, nil
	default:
		return 0, fmt.Errorf("unknown seed kind %q", seed.kind)
	}
	if _, err := s.git(nil, "add", "-A"); err != nil {
		return 0, err
	}
	if seed.kind != "author" {
		if seed.kind == "hunk" {
			diff, err := s.git(nil, "diff", "--cached", "--no-ext-diff", "--no-renames", "-U0")
			if err != nil {
				return 0, err
			}
			edits = strings.Count("\n"+diff, "\n@@")
		} else {
			// The line count is review.CountEdits over git's own --numstat arithmetic,
			// the one definition the seed form uses (T01's repair moved it from the
			// patch text to --numstat; feeding it a unified diff counted a one-line
			// seed as zero and a two-line one as one, found by the hulk gate of
			// 58bcb348).
			numstat, err := s.git(nil, "diff", "--cached", "--no-ext-diff", "--no-renames", "--numstat")
			if err != nil {
				return 0, err
			}
			edits = review.CountEdits(numstat)
		}
		if edits != 1 {
			return edits, &seedCountError{edits: edits}
		}
	}
	if err := s.commit(env, "seed "+seed.name); err != nil {
		return 0, err
	}
	return edits, nil
}

// fixtureKeyShape is a key-SHAPED string made at run time: the forge's token prefix and
// thirty-six random alphanumerics. It is a valid key for nobody, and it is never
// written into the fixture tree (SPEC-TOOLWORK §3 rule 6).
func fixtureKeyShape() string {
	const alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 36)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = 'A'
		}
	}
	for i := range b {
		b[i] = alnum[int(b[i])%len(alnum)]
	}
	return "gh" + "p_" + string(b)
}

// readSeeds reads seeds/<name>/seed.txt for every name of the spec's table, in its
// order, and refuses a tree that lacks one or carries an extra.
func readSeeds(fsys fs.FS) ([]seedSpec, error) {
	entries, err := fs.ReadDir(fsys, "seeds")
	if err != nil {
		return nil, errors.New("fixture has no seeds/ directory")
	}
	present := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			present[e.Name()] = true
		}
	}
	for name := range present {
		known := false
		for _, want := range selftestSeeds {
			if want == name {
				known = true
			}
		}
		if !known {
			return nil, fmt.Errorf("fixture carries a seed the spec's table does not name: %s", name)
		}
	}
	var out []seedSpec
	for _, name := range selftestSeeds {
		if !present[name] {
			return nil, fmt.Errorf("fixture lacks the %s seed", name)
		}
		raw, err := fs.ReadFile(fsys, "seeds/"+name+"/seed.txt")
		if err != nil {
			return nil, fmt.Errorf("seeds/%s has no seed.txt", name)
		}
		spec := seedSpec{name: name, fields: map[string]string{}}
		for _, l := range strings.Split(string(raw), "\n") {
			l = strings.TrimRight(l, "\r")
			if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
				continue
			}
			k, v, ok := strings.Cut(l, "=")
			if !ok {
				return nil, fmt.Errorf("seeds/%s/seed.txt: %q is not key=value", name, l)
			}
			spec.fields[k] = v
		}
		spec.kind, spec.want = spec.fields["kind"], spec.fields["want"]
		spec.file, spec.text, spec.after = spec.fields["file"], spec.fields["text"], spec.fields["after"]
		spec.author, spec.line = spec.fields["author"], spec.fields["line"]
		if spec.kind == "" || spec.want == "" {
			return nil, fmt.Errorf("seeds/%s/seed.txt: kind= and want= are required", name)
		}
		if spec.kind == "patch" {
			spec.patch, err = fs.ReadFile(fsys, "seeds/"+name+"/seed.patch")
			if err != nil {
				return nil, fmt.Errorf("seeds/%s is a patch seed with no seed.patch", name)
			}
		}
		out = append(out, spec)
	}
	return out, nil
}

func parseIdentity(s string) (hyg.Identity, bool) {
	name, email, ok := strings.Cut(s, "<")
	if !ok || !strings.HasSuffix(email, ">") {
		return hyg.Identity{}, false
	}
	return hyg.Identity{Name: strings.TrimSpace(name), Email: strings.TrimSpace(strings.TrimSuffix(email, ">"))}, true
}

// replaceHeaderLine replaces the header line whose key matches the given line's key.
func replaceHeaderLine(card, line string) string {
	key, _, _ := strings.Cut(line, ":")
	lines := strings.Split(card, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, key+":") {
			lines[i] = line
			break
		}
	}
	return strings.Join(lines, "\n")
}

// parseCardHeaderText reads a header from card text rather than a file.
func parseCardHeaderText(text string) (CardHeader, error) {
	f, err := os.CreateTemp("", "nova-pulse-card-")
	if err != nil {
		return CardHeader{}, err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return CardHeader{}, err
	}
	f.Close()
	return ReadCardHeader(f.Name())
}
