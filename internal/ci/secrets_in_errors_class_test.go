package ci

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/mas-bandwidth/nova-tools/internal/check"
	ciallowlist "github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/converge"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	nslog "github.com/mas-bandwidth/nova-tools/internal/log"
	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/secretcheck"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/tlc"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: NO SECRET REACHES AN ERROR (docs/SPEC-CI.md,
// `secrets-never-in-errors`).
//
// A Postgres DSN parse error printed the password (internal/config/pg.go,
// fix-pg-dsn-parse-error-leak): the parser's message carried the DSN it was
// given, a refusal travelled to a terminal and a log, and the secret went with
// it. The fix was one function; the class is every opener that is handed a
// string that may be a secret. The rule drives secret-shaped inputs -- a DSN with
// a password (well formed and unreachable, malformed in the URL form, malformed
// in the keyword form), a URL with userinfo, and strings shaped like an
// OpenRouter, a GitHub and an Anthropic token, each carrying its own unique
// marker -- through every exported top-level function named Open*, Parse*, Dial*
// or New* that takes a string, and refuses the function when any 8-byte
// substring of the secret appears in an error it returns, in a value it returns
// that names a refusal, in the text of a panic, or in the log output captured
// during the call.
//
// The harness is internal/secretcheck's. The functions are found by go/ast over cmd/
// and internal/, nova-sprint's paths aside (sprintOnly), and driven from a
// reviewed table (secretOpeners) that the test keeps complete by comparing it
// with what go/ast finds, in both directions: a function in the tree and not in
// the table is red, and so is a table row naming no function. A function the
// rule cannot drive safely is in secretExempt with its reason, and is held to the
// same comparison. A leak the tree already has is a row of secretLeakAllowlist
// with its reason, checked in both directions too: an unlisted leak is red and a
// listed function that no longer leaks is red, so the list only shrinks.

// secretFiles is the walk's files as the harness reads them, without nova-sprint's
// (sprintOnly): its functions are held by the same rule in its own tree, with this
// harness (internal/sprint, secrets_in_errors_test.go).
func secretFiles(files []*treeFile) []secretcheck.File {
	var out []secretcheck.File
	for _, f := range files {
		if sprintOnly(f.Rel) {
			continue
		}
		out = append(out, secretcheck.File{Rel: f.Rel, AST: f.AST, Testdata: f.HasDirNamed("testdata")})
	}
	return out
}

// secretOpeners is the reviewed table of every function the rule drives, keyed
// `<repo-relative package directory>.<Name>`. TestNoSecretReachesAnError holds
// it complete against the tree.
var secretOpeners = map[string]any{
	"internal/buildinfo.Parse":                         buildinfo.Parse,
	"internal/bus/bustest.NewFake":                     bustest.NewFake,
	"internal/cardcost.ParseSpend":                     cardcost.ParseSpend,
	"internal/cardcost.ParseTotal":                     cardcost.ParseTotal,
	"internal/cardcost.ParseUsage":                     cardcost.ParseUsage,
	"internal/cardhdr.ParseBase":                       cardhdr.ParseBase,
	"internal/cardhdr.ParseTest":                       cardhdr.ParseTest,
	"internal/cardtree.Parse":                          cardtree.Parse,
	"internal/cardtree.ParseRegex":                     cardtree.ParseRegex,
	"internal/cardtree.ParseVerdicts":                  cardtree.ParseVerdicts,
	"internal/check.ParseAllowlist":                    check.ParseAllowlist,
	"internal/check.ParseDenyList":                     check.ParseDenyList,
	"internal/ci/allowlist.Parse":                      ciallowlist.Parse,
	"internal/config.OpenFile":                         config.OpenFile,
	"internal/config.OpenPG":                           config.OpenPG,
	"internal/converge.ParseCerts":                     converge.ParseCerts,
	"internal/converge.ParseRetired":                   converge.ParseRetired,
	"internal/converge.ParseSince":                     converge.ParseSince,
	"internal/converge.ParseVersions":                  converge.ParseVersions,
	"internal/decide.ParseBar":                         decide.ParseBar,
	"internal/decide.ParseBars":                        decide.ParseBars,
	"internal/decide.ParseBriefBar":                    decide.ParseBriefBar,
	"internal/decide.ParseDecided":                     decide.ParseDecided,
	"internal/decide.ParseGateBars":                    decide.ParseGateBars,
	"internal/decide.ParseGateOutput":                  decide.ParseGateOutput,
	"internal/decide.ParseJudgmentBar":                 decide.ParseJudgmentBar,
	"internal/diffcheck.Parse":                         diffcheck.Parse,
	"internal/dogfood.NewShipped":                      dogfood.NewShipped,
	"internal/dogfood.ParseAuthors":                    dogfood.ParseAuthors,
	"internal/dogfood.ParseCLI":                        dogfood.ParseCLI,
	"internal/dogfood.ParseHelp":                       dogfood.ParseHelp,
	"internal/dogfood.ParseReference":                  dogfood.ParseReference,
	"internal/filelock.ParseStamp":                     filelock.ParseStamp,
	"internal/fleet.ParseWorkload":                     fleet.ParseWorkload,
	"internal/friend.NewClaude":                        friend.NewClaude,
	"internal/friend.NewDeliverer":                     friend.NewDeliverer,
	"internal/friend.NewestCodexSession":               friend.NewestCodexSession,
	"internal/friend.NewestConversation":               friend.NewestConversation,
	"internal/friend.NewestDSHSession":                 friend.NewestDSHSession,
	"internal/friend.ParseHeld":                        friend.ParseHeld,
	"internal/friend.ParseJob":                         friend.ParseJob,
	"internal/friend.ParseLaneCaps":                    friend.ParseLaneCaps,
	"internal/friend.ParseLimit":                       friend.ParseLimit,
	"internal/friend.ParsePing":                        friend.ParsePing,
	"internal/friend.ParsePong":                        friend.ParsePong,
	"internal/friend.ParseProfile":                     friend.ParseProfile,
	"internal/friend.ParseMachine":                     friend.ParseMachine,
	"internal/friend.ParseReadQueue":                   friend.ParseReadQueue,
	"internal/friend.ParseReadSlots":                   friend.ParseReadSlots,
	"internal/friend.ParseRow":                         friend.ParseRow,
	"internal/friend.ParseView":                        friend.ParseView,
	"internal/hostload.ParseProcStat":                  hostload.ParseProcStat,
	"internal/hostload.ParseTopCPU":                    hostload.ParseTopCPU,
	"internal/log.New":                                 nslog.New,
	"internal/nsprint/store.Open":                      nsstore.Open,
	"internal/nsprint/verbflag.New":                    verbflag.New,
	"internal/ntable.NewReader":                        ntable.NewReader,
	"internal/ntable.NewRow":                           ntable.NewRow,
	"internal/ntable.ParseColumn":                      ntable.ParseColumn,
	"internal/ntable.ParseColumns":                     ntable.ParseColumns,
	"internal/ntable.ParseFormula":                     ntable.ParseFormula,
	"internal/ntable.ParseWidths":                      ntable.ParseWidths,
	"internal/onboarding.OpeningSentence":              onboarding.OpeningSentence,
	"internal/pkgselect.ParseDeprecated":               pkgselect.ParseDeprecated,
	"internal/record.DialLedger":                       record.DialLedger,
	"internal/sandbox.ParseGPUMode":                    sandbox.ParseGPUMode,
	"internal/secretcheck.OpenerName":                  secretcheck.OpenerName,
	"internal/secretcheck.ParseAllowlist":              secretcheck.ParseAllowlist,
	"internal/secrets.NewSecret":                       secrets.NewSecret,
	"internal/secrets.OpenSeatFile":                    secrets.OpenSeatFile,
	"internal/secrets.ParseSopsConfig":                 secrets.ParseSopsConfig,
	"internal/secrets.ParseStoreFileWithoutDecrypting": secrets.ParseStoreFileWithoutDecrypting,
	"internal/swarm.NewWallReader":                     swarm.NewWallReader,
	"internal/swarm.OpenCodeStoreLocations":            swarm.OpenCodeStoreLocations,
	"internal/swarm.ParseChildRules":                   swarm.ParseChildRules,
	"internal/swarm.ParseIdentity":                     swarm.ParseIdentity,
	"internal/swarm.ParseRouteList":                    swarm.ParseRouteList,
	"internal/tlc.Parse":                               tlc.Parse,
	"internal/tokens.ParseDayFile":                     tokens.ParseDayFile,
	"internal/tokens.ParseMicro":                       tokens.ParseMicro,
	"internal/tokens.ParseSubject":                     tokens.ParseSubject,
	"internal/tokens.ParseWeights":                     tokens.ParseWeights,
	"internal/tokens.ParserColumns":                    tokens.ParserColumns,
	"internal/typedrec.ParseTableRefusal":              typedrec.ParseTableRefusal,
}

// secretExempt are the functions the rule finds and does not drive, each with
// the reason; a row is held to the same two-way comparison as the table.
var secretExempt = map[string]string{
	"internal/hostload.ParseIostat":            "built on darwin only, so a test binary on another platform cannot name it",
	"internal/hostload.ParseProcLoadavg":       "built on linux only, so a test binary on another platform cannot name it",
	"internal/hostload.ParseFileNr":            "built on linux only, so a test binary on another platform cannot name it",
	"internal/hostload.ParseLsof":              "built on darwin only, so a test binary on another platform cannot name it",
	"internal/cairn.Open":                      "writes a session record under the store directory its first string names; driving it would write into the working tree",
	"internal/nsprint/testutil.NewLocalRemote": "takes a *testing.T and builds a git remote on disk; it is a test fixture, not an opener of a secret",
	"internal/friend.DialCodexAppServer":       "takes the Codex home, a directory path, never a secret; it dials the app-server socket under it",
}

// secretLeakAllowlist are the functions known to carry a secret-shaped string
// into an error, one `<key> <reason>` per line (a row with no reason is
// refused). It only shrinks: a function that no longer leaks is a red row to
// delete. internal/config.OpenPG, the case that started the rule, is never a row.
const secretLeakAllowlist = `
internal/cardtree.ParseRegex echoes the rejected line with %q (internal/cardtree/tree.go:421)
internal/converge.ParseCerts echoes the file name and the header it read (internal/converge/sources.go:328)
internal/converge.ParseSince echoes the rejected --since value (internal/converge/converge.go:479)
internal/converge.ParseVersions echoes the file name and the header it read (internal/converge/sources.go:297)
internal/decide.ParseBar echoes the rejected bar with %q (internal/decide/attempt.go:190)
internal/decide.ParseBars echoes the rejected bars with %q (internal/decide/firstread.go:47)
internal/decide.ParseBriefBar echoes the rejected bar with %q (internal/decide/brief.go:168)
internal/decide.ParseGateBars echoes the rejected bars with %q (internal/decide/gate.go:313)
internal/decide.ParseJudgmentBar echoes the rejected bar with %q (internal/decide/judgment.go:243)
internal/dogfood.ParseAuthors wraps the os.ReadFile error, which names the path argument (internal/dogfood/authors.go:28)
internal/dogfood.ParseCLI wraps the os.ReadFile error, which names the path argument (internal/dogfood/cli.go:97)
internal/fleet.ParseWorkload echoes its source argument (internal/fleet/certify.go:184)
internal/friend.NewDeliverer echoes an unknown harness with %q (internal/friend/adapter.go:253)
internal/friend.NewestCodexSession wraps the lstat error, which names the dir argument (internal/friend/codex_session.go:21)
internal/friend.NewestDSHSession echoes its dir argument (internal/friend/adapter_dsh.go:65)
internal/ntable.ParseColumn echoes the rejected column name with %q (internal/ntable/ntable.go:405)
internal/ntable.ParseColumns echoes the rejected column name with %q (internal/ntable/ntable.go:405)
internal/ntable.ParseFormula echoes the rejected projection with %q (internal/ntable/ntable.go:133)
internal/ntable.ParseWidths echoes the rejected part with %q (internal/ntable/ntable.go:536)
internal/onboarding.OpeningSentence echoes the tool name and the first help line (internal/onboarding/opening.go:46)
internal/sandbox.ParseGPUMode echoes the rejected mode in a Refusal (internal/sandbox/gpu.go:30)
internal/secrets.OpenSeatFile echoes the seat name and the store path in its preflight refusal (internal/secrets/seatfile.go:126, internal/secrets/seatfile.go:149)
internal/secrets.ParseSopsConfig wraps the read error, which names the store path (internal/secrets/store.go:82)
internal/secrets.ParseStoreFileWithoutDecrypting returns the os.Open error, which names the path argument (internal/secrets/store.go:250)
internal/swarm.ParseIdentity echoes the rejected identity with %q (internal/swarm/staging.go:89)
internal/tokens.ParseWeights echoes the rejected weights (internal/tokens/claude_session.go:53)
`

// TestNoSecretReachesAnError is the class rule; the helpers above carry its
// reasoning, and the witness below holds the check itself.
func TestNoSecretReachesAnError(t *testing.T) {
	t.Parallel()
	found := secretcheck.OpenersIn(secretFiles(repoTree(t).GoFilesUnder(false, "cmd", "internal")))
	require.NotEmpty(t, found, "the walk found no Open*/Parse*/Dial*/New* function; a rule that checks nothing passes")
	for _, f := range secretcheck.TableFindings(found, secretOpeners, secretExempt) {
		t.Error(f)
	}
	allowed, bad := secretcheck.ParseAllowlist(secretLeakAllowlist)
	for _, b := range bad {
		t.Error(b)
	}
	assert.NotContains(t, allowed, "internal/config.OpenPG", "the Postgres DSN opener is the case the rule was written for and is never allowlisted")
	for _, f := range secretcheck.Verdict(secretcheck.LeakFindings(secretOpeners, secretcheck.Shapes), allowed) {
		t.Error(f)
	}
}

// secretFixtures are the openers the witness drives: each leaks one way, or
// does not leak, and the check must say so.
var secretFixtures = struct {
	echo     func(string) error
	wrapped  func(string) error
	logs     func(string) error
	panics   func(string) error
	redacted func(string) error
	short    func(string) error
	long     func(string) error
	prefix   func(string) error
}{
	echo:    func(dsn string) error { return fmt.Errorf("postgres dsn %s could not be parsed", dsn) },
	wrapped: func(dsn string) error { return fmt.Errorf("open: %w", errors.New("bad input "+dsn)) },
	logs: func(tok string) error {
		slog.Warn("token rejected", "token", tok)
		return errors.New("token rejected")
	},
	panics:   func(s string) error { panic("cannot open " + s) },
	redacted: func(string) error { return fmt.Errorf("postgres dsn could not be parsed (%T)", errors.New("x")) },
	short:    func(s string) error { return errors.New("bad input " + s[len(s)-7:]) },
	long:     func(s string) error { return errors.New("bad input " + s[len(s)-8:]) },
	prefix:   func(s string) error { return errors.New("want a token that begins sk-or-v1-") },
}

// TestSecretCheckReadsItsFixtures holds the check against openers whose answer
// is known: each way a secret can reach a caller is found, the eight-byte
// boundary is exact, a public token prefix is not a secret, a refusal that names
// only a type passes, and a table that misses a function or names a stale one is
// refused.
func TestSecretCheckReadsItsFixtures(t *testing.T) {
	t.Parallel()
	dsn := secretcheck.Shapes[0]
	or := secretcheck.Shapes[4]
	leaks := func(fn func(string) error, s secretcheck.Shape) []string {
		return secretcheck.Leaks(s.Secret, s.Public, secretcheck.Drive(fn, s.Input))
	}
	t.Run("an opener that echoes its DSN is found", func(t *testing.T) {
		t.Parallel()
		assert.Len(t, leaks(secretFixtures.echo, dsn), 1)
		assert.Contains(t, leaks(secretFixtures.echo, dsn)[0], "error holds")
	})
	t.Run("a secret wrapped with %w is found", func(t *testing.T) {
		t.Parallel()
		assert.Len(t, leaks(secretFixtures.wrapped, dsn), 1)
	})
	t.Run("a secret written to the log is found", func(t *testing.T) {
		t.Parallel()
		got := leaks(secretFixtures.logs, or)
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "log holds")
	})
	t.Run("a secret in a panic is found", func(t *testing.T) {
		t.Parallel()
		got := leaks(secretFixtures.panics, or)
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "panic holds")
	})
	t.Run("a refusal that names only a type is clean", func(t *testing.T) {
		t.Parallel()
		for _, s := range secretcheck.Shapes {
			assert.Empty(t, leaks(secretFixtures.redacted, s), s.Name)
		}
	})
	t.Run("seven bytes of the secret are clean and eight are a leak", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, leaks(secretFixtures.short, or))
		assert.Len(t, leaks(secretFixtures.long, or), 1)
	})
	t.Run("a token's public prefix is not the secret", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, leaks(secretFixtures.prefix, or))
	})
	t.Run("every shape reaches the check with its own marker", func(t *testing.T) {
		t.Parallel()
		seen := map[string]string{}
		for _, s := range secretcheck.Shapes {
			assert.Contains(t, s.Input, s.Secret, s.Name)
			for _, w := range secretcheck.Windows(s.Secret, s.Public) {
				if other, dup := seen[w]; dup {
					assert.Equal(t, s.Name, other, "the window %q is in two shapes; each shape's marker is its own", w)
				}
				seen[w] = s.Name
			}
		}
	})
	t.Run("the verdict refuses an unlisted leak and a stale row", func(t *testing.T) {
		t.Parallel()
		leaking := map[string][]string{"internal/x.Open": {`dsn: error holds "pwQm4Zt9"`}}
		got := secretcheck.Verdict(leaking, nil)
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "internal/x.Open carries a secret")
		assert.Empty(t, secretcheck.Verdict(leaking, map[string]string{"internal/x.Open": "echoes"}))
		stale := secretcheck.Verdict(nil, map[string]string{"internal/x.Open": "echoes"})
		require.Len(t, stale, 1)
		assert.Contains(t, stale[0], "no longer leaks")
		_, bad := secretcheck.ParseAllowlist("internal/x.Open")
		assert.Len(t, bad, 1)
	})
	t.Run("a table that misses a function or names a stale one is refused", func(t *testing.T) {
		t.Parallel()
		found := map[string]bool{"internal/config.OpenPG": true, "internal/new.Open": true}
		table := map[string]any{"internal/config.OpenPG": config.OpenPG, "internal/gone.Parse": config.OpenPG}
		got := secretcheck.TableFindings(found, table, nil)
		require.Len(t, got, 2)
		assert.Contains(t, got[0], "internal/gone.Parse: a secretOpeners row names no such function")
		assert.Contains(t, got[1], "internal/new.Open: an exported Open*")
		wrong := secretcheck.TableFindings(map[string]bool{"internal/config.OpenFile": true}, map[string]any{"internal/config.OpenFile": config.OpenPG}, nil)
		require.Len(t, wrong, 1)
		assert.Contains(t, wrong[0], "not that function")
	})
	t.Run("the walk finds a function by name and by a string parameter", func(t *testing.T) {
		t.Parallel()
		found := secretcheck.OpenersIn(secretFiles(repoTree(t).GoFilesUnder(false, "internal/config")))
		assert.True(t, found["internal/config.OpenPG"])
		assert.True(t, found["internal/config.OpenFile"])
		assert.False(t, found["internal/config.Redact"], "a name outside Open/Parse/Dial/New is not read")
	})
}
