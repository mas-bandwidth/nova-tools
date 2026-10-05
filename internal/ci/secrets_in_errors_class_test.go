package ci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/internal/cairn"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/mas-bandwidth/nova-tools/internal/check"
	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/converge"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	novalog "github.com/mas-bandwidth/nova-tools/internal/log"
	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
	sprintstore "github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/tlc"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// secretsInErrorsAllowlistPath names the table below in messages; the leaks
// this rule found and has not yet fixed live in the test file itself, one
// `<package>.<Func>  # <file:line>: <reason>` row each, and only shrink.
const secretsInErrorsAllowlistPath = "internal/ci/secrets_in_errors_class_test.go (secretsInErrorsAllowlist)"

// secretsInErrorsAllowlist is the allowlist, in the allowlist package's row
// format, kept here so the card edits no file beside the test.
const secretsInErrorsAllowlist = `# The leaks TestNoSecretReachesAnError (internal/ci/secrets_in_errors_class_test.go)
# found and has not yet fixed, one '<package>.<Func>  # <file:line>: <reason>' per
# line. A row with no reason is refused. The list is checked in both directions: a
# row whose leak is gone is a red run, so it only shrinks, and a new row is a
# refusal, not a parking place.
#
# Every row below is a parser of ONE flag or field value that quotes the value it
# could not read in its error ("... got <value>"). None of them takes a secret
# today; each is a place where a secret pasted into the wrong flag would be
# printed back, so the fix is to name the field and the rule, not the value.
# ceiling: 30
internal/cairn.Open  # internal/cairn/cairn.go:497: echoes the session id it rejected, quoted (the publish policy at :500 too)
internal/cardtree.ParseRegex  # internal/cardtree/tree.go:413: names the regex line it could not read, quoted
internal/converge.ParseCerts  # internal/converge/sources.go:322: names the header text it could not read
internal/converge.ParseSince  # internal/converge/converge.go:459: echoes the --since value it could not read
internal/converge.ParseVersions  # internal/converge/sources.go:283: names the header text it could not read
internal/decide.ParseBar  # internal/decide/attempt.go:182: echoes the bar value it could not read
internal/decide.ParseBars  # internal/decide/firstread.go:37: echoes the decide_bounce/decide_review value it could not read
internal/decide.ParseBriefBar  # internal/decide/brief.go:161: echoes the decide_brief_bar value it could not read
internal/decide.ParseGateBars  # internal/decide/gate.go:300: echoes the decide_gate_* value it could not read
internal/decide.ParseJudgmentBar  # internal/decide/judgment.go:239: echoes the decide_judgment_bar value it could not read
internal/fleet.ParseWorkload  # internal/fleet/certify.go:178: names the source it could not read, quoted
internal/friend.NewDeliverer  # internal/friend/adapter.go:170: echoes the harness name it does not know
internal/friend.NewestCodexSession  # internal/friend/codex_session.go:21: wraps the lstat error, which names the directory argument it was given
internal/friend.NewestDSHSession  # internal/friend/adapter_dsh.go:55: echoes the directory it found no session for
internal/ntable.ParseColumn  # internal/ntable/ntable.go:479: echoes the column spec it could not read
internal/ntable.ParseColumns  # internal/ntable/ntable.go:506: echoes the column spec it could not read
internal/ntable.ParseFormula  # internal/ntable/ntable.go:131: echoes the projection it could not read
internal/ntable.ParseWidths  # internal/ntable/ntable.go:526: echoes the width spec it could not read
internal/onboarding.OpeningSentence  # internal/onboarding/opening.go:41: names the help text line it could not read
internal/secrets.OpenSeatFile  # internal/secrets/seatfile.go:126: echoes the seat name it rejected, quoted
internal/sandbox.ParseGPUMode  # internal/sandbox/gpu.go:23: echoes the --gpu value it does not know
internal/sprint.ParseAttempts  # internal/sprint/brief_bound.go:76: echoes the --attempts value it could not read
internal/sprint.ParseDeadline  # internal/sprint/deadline.go:103: echoes the --deadline value it could not read
internal/sprint.ParseMembers  # internal/sprint/width.go:81: echoes the member width it could not read
internal/sprint.ParseRoute  # internal/sprint/remind.go:105: echoes the route it could not read
internal/sprint.ParseWidth  # internal/sprint/width.go:39: echoes the width it could not read
internal/sprint/store.NewDeliverer  # internal/sprint/store/remind.go:144: echoes the route it could not read
internal/swarm.ParseIdentity  # internal/swarm/staging.go:87: echoes the --identity value it could not read
internal/tokens.ParseWeights  # internal/tokens/claude_session.go:50: echoes the --weights value it could not read
`

// secretInput is one secret-shaped input: the string a caller hands an opener,
// built around a marker no honest message holds by chance.
type secretInput struct {
	name  string
	build func(marker string) string
}

// secretInputs are the shapes of a secret that reach an opener as one string:
// a Postgres DSN in both spellings (and each malformed, so the parse error path
// runs), a URL with userinfo, and an OpenRouter, a GitHub and an Anthropic
// token. Each carries its own marker, so a leak names the shape that leaked.
var secretInputs = []secretInput{
	{name: "dsn-url", build: func(m string) string { return "postgres://nova:" + m + "@127.0.0.1:1/nova?sslmode=disable" }},
	{name: "dsn-url-bad-port", build: func(m string) string { return "postgres://nova:" + m + "@127.0.0.1:notaport/nova" }},
	{name: "dsn-keyword", build: func(m string) string {
		return "host=127.0.0.1 port=1 user=nova password=" + m + " dbname=nova sslmode=disable"
	}},
	{name: "dsn-keyword-bad", build: func(m string) string { return "host=127.0.0.1 port=1 user=nova password=" + m + " sslmode=bogus" }},
	{name: "url-userinfo", build: func(m string) string { return "https://" + "deploy:" + m + "@example.invalid/repo.git" }},
	{name: "token-openrouter", build: func(m string) string { return "sk-or-v1-" + m }},
	{name: "token-github", build: func(m string) string { return "ghp_" + m }},
	{name: "token-anthropic", build: func(m string) string { return "sk-ant-api03-" + m }},
}

// markerFor is the unique marker of one input shape: 22 bytes, any 8-byte
// window of which in an output is the secret.
func markerFor(i int) string { return fmt.Sprintf("Zq%dKx9Wm4Rt2Bv8Nc3Hy6", i) }

// leakedWindow is the first 8-byte substring of secret found in hay, "" when
// none is.
func leakedWindow(hay, secret string) string {
	for i := 0; i+8 <= len(secret); i++ {
		if strings.Contains(hay, secret[i:i+8]) {
			return secret[i : i+8]
		}
	}
	return ""
}

// The openers are driven in a child process: the test binary re-run on the
// one test below, so that what an opener logs, prints or panics with is the
// child's own stdout and stderr, and no package-level writer (log, slog,
// os.Stderr) is swapped under the parallel tests of this package.
const (
	secretsChildEnv  = "NOVA_SECRETS_IN_ERRORS_CHILD"
	secretsCallMark  = "\n@@call "
	secretsChildWait = 3 * time.Minute
)

// TestSecretsInErrorsChild is the child half of TestNoSecretReachesAnError and
// TestSecretCheckReadsItsFixtures; run by itself it does nothing.
func TestSecretsInErrorsChild(t *testing.T) {
	t.Parallel()
	which := os.Getenv(secretsChildEnv)
	if which == "" {
		t.Skip("child of the secrets-in-errors class test; it runs only inside it")
	}
	table := openers
	if which == "fixtures" {
		table = fixtureOpeners
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.SetFlags(0)
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for i, in := range secretInputs {
			fmt.Fprintf(os.Stdout, "%s%s|%d\n", secretsCallMark, name, i)
			driveOpener(table[name], in.build(markerFor(i)))
			// an opener's own goroutine may still be writing; the next mark is the boundary
		}
	}
}

// driveOpener calls fn with every string parameter set to input, a cancelled
// context (so an opener that dials fails at once and opens no socket), a
// discard writer, a stub for each function and zero values elsewhere, and
// prints what came back in an error or a panic.
func driveOpener(fn any, input string) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := reflect.ValueOf(fn)
	typ := v.Type()
	args := make([]reflect.Value, typ.NumIn())
	for i := range args {
		args[i] = zeroArg(typ.In(i), input, ctx)
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("panic: %v\n", r)
		}
	}()
	var out []reflect.Value
	if typ.IsVariadic() {
		out = v.CallSlice(args)
	} else {
		out = v.Call(args)
	}
	for _, o := range out {
		if e, ok := o.Interface().(error); ok && e != nil {
			fmt.Printf("error: %v | %+v\n", e, e)
		}
	}
}

var (
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	writerType  = reflect.TypeOf((*io.Writer)(nil)).Elem()
)

// zeroArg is the argument for one parameter of an opener.
func zeroArg(t reflect.Type, input string, ctx context.Context) reflect.Value {
	switch {
	case t.Kind() == reflect.String:
		return reflect.ValueOf(input).Convert(t)
	case t == contextType:
		return reflect.ValueOf(ctx)
	case t == writerType:
		return reflect.ValueOf(io.Writer(os.Stderr))
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.String:
		return reflect.ValueOf([]string{input}).Convert(t)
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8:
		return reflect.ValueOf([]byte(input)).Convert(t)
	case t.Kind() == reflect.Func:
		return reflect.MakeFunc(t, func([]reflect.Value) []reflect.Value {
			out := make([]reflect.Value, t.NumOut())
			for i := range out {
				out[i] = reflect.Zero(t.Out(i))
			}
			return out
		})
	}
	return reflect.Zero(t)
}

// driveTable runs the child over one table and returns one entry per opener
// that put a window of a marker in anything it wrote: the shapes that leaked
// and the first output that carried one.
func driveTable(t *testing.T, which string) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), secretsChildWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSecretsInErrorsChild$", "-test.count=1")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{secretsChildEnv + "=" + which, "HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH"), "NOVA_TEST_NO_HOST=1"}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	require.NoError(t, cmd.Run(), "the child that drives the openers died; its output:\n%s", truncate(out.String(), 2000))

	shapes := map[string][]string{}
	first := map[string]string{}
	for _, section := range strings.Split(out.String(), secretsCallMark)[1:] {
		head, body, _ := strings.Cut(section, "\n")
		name, idx, ok := strings.Cut(head, "|")
		i, err := strconv.Atoi(idx)
		require.True(t, ok && err == nil && i < len(secretInputs), "a section header this test cannot read: %q", head)
		// the rest of the section runs to the next mark; it is the call's output
		if w := leakedWindow(body, markerFor(i)); w != "" {
			shapes[name] = append(shapes[name], secretInputs[i].name)
			if first[name] == "" {
				first[name] = fmt.Sprintf("%q in %s", w, truncate(strings.TrimSpace(body), 160))
			}
		}
	}
	leaks := map[string]string{}
	for name, ss := range shapes {
		leaks[name] = fmt.Sprintf("under %s: %s", strings.Join(ss, ","), first[name])
	}
	return leaks
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// openerCandidates are the exported functions named Open*, Parse*, Dial* or
// New* with a string parameter, found by go/ast over every non-test file of the
// tree, as `<repo-relative dir>.<Func>`.
func openerCandidates(t *testing.T, root string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || !fd.Name.IsExported() || !takesString(fd.Type.Params) {
				continue
			}
			for _, p := range []string{"Open", "Parse", "Dial", "New"} {
				if strings.HasPrefix(fd.Name.Name, p) {
					found[filepath.ToSlash(rel)+"."+fd.Name.Name] = true
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
	return found
}

func takesString(fl *ast.FieldList) bool {
	for _, f := range fl.List {
		typ := f.Type
		if el, ok := typ.(*ast.Ellipsis); ok {
			typ = el.Elt
		}
		if id, ok := typ.(*ast.Ident); ok && id.Name == "string" {
			return true
		}
	}
	return false
}

// TestNoSecretReachesAnError drives secret-shaped strings through every opener
// in the tree and refuses a window of the secret in a returned error or in
// what the call logged. The openers are the reviewed table below, held
// complete against go/ast in both directions; a leak not yet fixed is a row in
// the allowlist with its reason, and the list only shrinks.
func TestNoSecretReachesAnError(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	cands := openerCandidates(t, root)

	var missing, stale []string
	for name := range cands {
		_, driven := openers[name]
		_, skipped := notDriven[name]
		switch {
		case driven && skipped:
			stale = append(stale, name+" is in both openers and notDriven")
		case !driven && !skipped:
			missing = append(missing, name)
		}
	}
	for name := range openers {
		if !cands[name] {
			stale = append(stale, name+" is in the openers table and no such function is in the tree")
		}
	}
	for name, why := range notDriven {
		assert.NotEmpty(t, strings.TrimSpace(why), "%s is in notDriven with no reason", name)
		if !cands[name] {
			stale = append(stale, name+" is in notDriven and no such function is in the tree")
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	assert.Empty(t, missing, "an exported Open*/Parse*/Dial*/New* function taking a string is in the tree and in neither `openers` (driven with secrets) nor `notDriven` (with a reason); add it to one in internal/ci/secrets_in_errors_class_test.go")
	assert.Empty(t, stale, "the openers tables name a function that is not there; delete the row")

	leaks := driveTable(t, "openers")

	allow, err := allowlist.Parse(secretsInErrorsAllowlistPath, secretsInErrorsAllowlist, shrinkOnly)
	require.NoError(t, err)
	for _, row := range allow.Rows() {
		i := strings.Index(row.Text, "#")
		assert.False(t, i < 0 || strings.TrimSpace(row.Text[i+1:]) == "", "%s: %q carries no reason; every exception says why, or nobody can ever delete it", secretsInErrorsAllowlistPath, row.Text)
	}
	measured := map[string]bool{}
	for k := range leaks {
		measured[k] = true
	}
	res := allowlist.CheckMode(t, allow, measured, false)
	for _, k := range res.Unlisted {
		t.Errorf("%s leaks its secret input: %s\nremedy: redact the input before it reaches the error or the log (config.Redact is the DSN's); never print it", k, leaks[k])
	}
	for _, row := range res.Stale {
		t.Errorf("%s lists %s, which no longer leaks; delete the row (the list only shrinks)", secretsInErrorsAllowlistPath, row.Key)
	}
}

// echoOpener is the fixture: an opener that puts its DSN in its error.
func echoOpener(dsn string) error { return fmt.Errorf("open %s: refused", dsn) }

// logOpener is the fixture: an opener that logs its argument and returns nil.
func logOpener(dsn string) error { log.Printf("dialing %s", dsn); return nil }

// writeOpener is the fixture: an opener that writes its argument to the writer it
// was handed and returns nil.
func writeOpener(dsn string, w io.Writer) error {
	_, err := fmt.Fprintf(w, "connecting to %s\n", dsn)
	return err
}

// quietOpener is the fixture that keeps its input out of both.
func quietOpener(dsn string) error { return errors.New("open: refused") }

// fixtureOpeners are the openers the measure is pinned against.
var fixtureOpeners = map[string]any{"fx.Echo": echoOpener, "fx.Log": logOpener, "fx.Write": writeOpener, "fx.Quiet": quietOpener}

// TestSecretCheckReadsItsFixtures pins the measure: an opener that echoes its
// DSN, or logs it, is caught under every shape; one that does not is not; and
// a window of 8 bytes is enough to catch a partial echo.
func TestSecretCheckReadsItsFixtures(t *testing.T) {
	t.Parallel()
	leaks := driveTable(t, "fixtures")
	all := "under dsn-url,dsn-url-bad-port,dsn-keyword,dsn-keyword-bad,url-userinfo,token-openrouter,token-github,token-anthropic"
	assert.Contains(t, leaks["fx.Echo"], all, "an opener that echoes its input leaks under every shape")
	assert.Contains(t, leaks["fx.Log"], all, "an opener that logs its input leaks under every shape")
	assert.Contains(t, leaks["fx.Write"], all, "an opener that writes its input to the io.Writer it was handed leaks under every shape")
	assert.NotContains(t, leaks, "fx.Quiet")
	assert.Equal(t, "Kx9Wm4Rt", leakedWindow("tail Kx9Wm4Rt2 end", "Zq0Kx9Wm4Rt2Bv8Nc3Hy6"))
	assert.Empty(t, leakedWindow("Kx9Wm4R only seven", "Zq0Kx9Wm4Rt2Bv8Nc3Hy6"))
}

// notDriven are the candidates this rule does not drive, each with why: a
// path the function opens (its error names the caller's own path, by design),
// a platform file this build does not carry, or a test helper.
var notDriven = map[string]string{
	"internal/config.OpenFile":                         "opens the path it is given and its error names that path by design; its only string argument is a file name, not a secret-bearing string",
	"internal/secrets.ParseSopsConfig":                 "opens the path it is given and its error names that path by design; its only string argument is a file name, not a secret-bearing string",
	"internal/secrets.ParseStoreFileWithoutDecrypting": "opens the path it is given and its error names that path by design; its only string argument is a file name, not a secret-bearing string",
	"internal/dogfood.ParseAuthors":                    "opens the path it is given and its error names that path by design; its only string argument is a file name, not a secret-bearing string",
	"internal/dogfood.ParseCLI":                        "opens the path it is given and its error names that path by design; its only string argument is a file name, not a secret-bearing string",
	"internal/hostload.ParseIostat":                    "defined in hostload_darwin.go only; this build carries no such function",
	"internal/hostload.ParseProcLoadavg":               "defined in hostload_linux.go only; this build carries no such function",
	"internal/nsprint/testutil.NewLocalRemote":         "a test helper that takes *testing.T and builds a git remote on disk",
}

// openers are the reviewed openers, driven with every secret shape. The
// table is held complete against go/ast by TestNoSecretReachesAnError.
var openers = map[string]any{
	"internal/buildinfo.Parse":              buildinfo.Parse,
	"internal/cairn.Open":                   cairn.Open,
	"internal/bus/bustest.NewFake":          bustest.NewFake,
	"internal/cardcost.ParseSpend":          cardcost.ParseSpend,
	"internal/cardcost.ParseTotal":          cardcost.ParseTotal,
	"internal/cardcost.ParseUsage":          cardcost.ParseUsage,
	"internal/cardgen.ParseFindings":        cardgen.ParseFindings,
	"internal/cardgen.ParseLedger":          cardgen.ParseLedger,
	"internal/cardhdr.ParseTest":            cardhdr.ParseTest,
	"internal/cardtree.Parse":               cardtree.Parse,
	"internal/cardtree.ParseRegex":          cardtree.ParseRegex,
	"internal/cardtree.ParseVerdicts":       cardtree.ParseVerdicts,
	"internal/check.ParseAllowlist":         check.ParseAllowlist,
	"internal/check.ParseDenyList":          check.ParseDenyList,
	"internal/ci/allowlist.Parse":           allowlist.Parse,
	"internal/config.OpenPG":                config.OpenPG,
	"internal/converge.ParseCerts":          converge.ParseCerts,
	"internal/converge.ParseRetired":        converge.ParseRetired,
	"internal/converge.ParseSince":          converge.ParseSince,
	"internal/converge.ParseVersions":       converge.ParseVersions,
	"internal/decide.ParseBar":              decide.ParseBar,
	"internal/decide.ParseBars":             decide.ParseBars,
	"internal/decide.ParseBriefBar":         decide.ParseBriefBar,
	"internal/decide.ParseDecided":          decide.ParseDecided,
	"internal/decide.ParseGateBars":         decide.ParseGateBars,
	"internal/decide.ParseGateOutput":       decide.ParseGateOutput,
	"internal/decide.ParseJudgmentBar":      decide.ParseJudgmentBar,
	"internal/diffcheck.Parse":              diffcheck.Parse,
	"internal/dogfood.NewShipped":           dogfood.NewShipped,
	"internal/dogfood.ParseHelp":            dogfood.ParseHelp,
	"internal/dogfood.ParseReference":       dogfood.ParseReference,
	"internal/filelock.ParseStamp":          filelock.ParseStamp,
	"internal/fleet.ParseWorkload":          fleet.ParseWorkload,
	"internal/friend.NewDeliverer":          friend.NewDeliverer,
	"internal/friend.NewestConversation":    friend.NewestConversation,
	"internal/friend.NewestCodexSession":    friend.NewestCodexSession,
	"internal/friend.NewestDSHSession":      friend.NewestDSHSession,
	"internal/friend.NewestSession":         friend.NewestSession,
	"internal/friend.ParsePing":             friend.ParsePing,
	"internal/friend.ParsePong":             friend.ParsePong,
	"internal/friend.ParseProfile":          friend.ParseProfile,
	"internal/friend.ParseRow":              friend.ParseRow,
	"internal/hostload.ParseProcStat":       hostload.ParseProcStat,
	"internal/hostload.ParseTopCPU":         hostload.ParseTopCPU,
	"internal/log.New":                      novalog.New,
	"internal/nsprint/store.Open":           nsstore.Open,
	"internal/nsprint/verbflag.New":         verbflag.New,
	"internal/ntable.NewReader":             ntable.NewReader,
	"internal/ntable.NewRow":                ntable.NewRow,
	"internal/ntable.ParseColumn":           ntable.ParseColumn,
	"internal/ntable.ParseColumns":          ntable.ParseColumns,
	"internal/ntable.ParseFormula":          ntable.ParseFormula,
	"internal/ntable.ParseWidths":           ntable.ParseWidths,
	"internal/onboarding.OpeningSentence":   onboarding.OpeningSentence,
	"internal/pkgselect.ParseDeprecated":    pkgselect.ParseDeprecated,
	"internal/record.DialLedger":            record.DialLedger,
	"internal/sandbox.ParseGPUMode":         sandbox.ParseGPUMode,
	"internal/secrets.NewSecret":            secrets.NewSecret,
	"internal/secrets.OpenSeatFile":         secrets.OpenSeatFile,
	"internal/sprint.NewTable":              sprint.NewTable,
	"internal/sprint.OpenKey":               sprint.OpenKey,
	"internal/sprint.ParseAttempts":         sprint.ParseAttempts,
	"internal/sprint.ParseDeadline":         sprint.ParseDeadline,
	"internal/sprint.ParseFriendReadReport": sprint.ParseFriendReadReport,
	"internal/sprint.ParseMembers":          sprint.ParseMembers,
	"internal/sprint.ParseReadCard":         sprint.ParseReadCard,
	"internal/sprint.ParseRoute":            sprint.ParseRoute,
	"internal/sprint.ParseWidth":            sprint.ParseWidth,
	"internal/sprint.ParseWorkCard":         sprint.ParseWorkCard,
	"internal/sprint/refmodel.New":          refmodel.New,
	"internal/sprint/store.NewDeliverer":    sprintstore.NewDeliverer,
	"internal/swarm.NewWallReader":          swarm.NewWallReader,
	"internal/swarm.OpenCodeStoreLocations": swarm.OpenCodeStoreLocations,
	"internal/swarm.ParseChildRules":        swarm.ParseChildRules,
	"internal/swarm.ParseIdentity":          swarm.ParseIdentity,
	"internal/swarm.ParseRouteList":         swarm.ParseRouteList,
	"internal/tlc.Parse":                    tlc.Parse,
	"internal/tokens.ParseDayFile":          tokens.ParseDayFile,
	"internal/tokens.ParseMicro":            tokens.ParseMicro,
	"internal/tokens.ParseSubject":          tokens.ParseSubject,
	"internal/tokens.ParseWeights":          tokens.ParseWeights,
	"internal/tokens.ParserColumns":         tokens.ParserColumns,
	"internal/typedrec.ParseTableRefusal":   typedrec.ParseTableRefusal,
}
