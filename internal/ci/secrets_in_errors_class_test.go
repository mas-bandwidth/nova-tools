package ci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"io"
	"log"
	"log/slog"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/check"
	ciallowlist "github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/converge"
	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
	sprintstore "github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/cardtree"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/diffcheck"
	"github.com/mas-bandwidth/nova-tools/pkg/dogfood"
	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
	"github.com/mas-bandwidth/nova-tools/pkg/fleet"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
	nslog "github.com/mas-bandwidth/nova-tools/pkg/log"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/mas-bandwidth/nova-tools/pkg/pkgselect"
	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: NO SECRET REACHES AN ERROR (docs/SPEC-CI.md,
// `secrets-never-in-errors`).
//
// A Postgres DSN parse error printed the password (pkg/config/pg.go,
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
// The functions are found by go/ast over cmd/ and internal/ and driven from a
// reviewed table (secretOpeners) that the test keeps complete by comparing it
// with what go/ast finds, in both directions: a function in the tree and not in
// the table is red, and so is a table row naming no function. A function the
// rule cannot drive safely is in secretExempt with its reason, and is held to the
// same comparison. A leak the tree already has is a row of secretLeakAllowlist
// with its reason, checked in both directions too: an unlisted leak is red and a
// listed function that no longer leaks is red, so the list only shrinks.

// secretShape is one secret-shaped input: the string a function is handed, the
// secret inside it, and the public prefix of a token whose own spelling is not
// the secret.
type secretShape struct {
	name   string
	input  string
	secret string
	public string
}

// secretWindow is the substring length that counts as the secret reaching a
// text: eight bytes of it, anywhere.
const secretWindow = 8

// secretShapes are the inputs. Every secret holds a marker no other shape, no
// message of this tree and no path shares.
var secretShapes = func() []secretShape {
	dsnPW := "pwQm4Zt9Xv2LkR7bNc5W"
	urlPW := "pwJt8Hs3Yd6FwP1gUe9A"
	kvPW := "pwVx2Kb7Mn4RtC8hLs3E"
	userPW := "pwGd5Zc9Qa1XvT6mHb2K"
	orToken := "sk-or-v1-" + "Zq7xK2mVd3Wn8Ys5Jc0Tu6Pe1Gf4Ba9Lr2Oi7Hk"
	ghToken := "ghp_" + "Fh6Rj3Ux8Cw1Nb5Ty9Ea2Lv4Mk7Sd0Pq"
	antToken := "sk-ant-api03-" + "Jw4Ct9Bn2Xs7Hy1Rk5Vd8Fe3Zm6Qa0Lg-Uo"
	return []secretShape{
		{"dsn-url-unreachable", "postgres://nova:" + dsnPW + "@%2Fnowhere-nova-ci/nova", dsnPW, ""},
		{"dsn-url-malformed", "postgres://nova:" + urlPW + "@db.invalid:notaport/nova", urlPW, ""},
		{"dsn-keyword-malformed", "host=/nowhere-nova-ci user=nova port=notaport password=" + kvPW, kvPW, ""},
		{"url-userinfo", "https://" + "deploy" + ":" + userPW + "@" + "git.invalid" + "/nova.git", userPW, ""},
		{"openrouter-token", orToken, orToken, "sk-or-v1-"},
		{"github-token", ghToken, ghToken, "ghp_"},
		{"anthropic-token", antToken, antToken, "sk-ant-api03-"},
	}
}()

// secretWindows is every 8-byte substring of s that lies after the public
// prefix: a token's own spelling (sk-or-v1-, ghp_) is not the secret.
func secretWindows(s, public string) []string {
	var out []string
	for i := 0; i+secretWindow <= len(s); i++ {
		if i < len(public) {
			continue
		}
		out = append(out, s[i:i+secretWindow])
	}
	return out
}

// secretLeaks is each named text that holds an 8-byte substring of the secret,
// as `<where> holds "<window>"`, sorted by where.
func secretLeaks(secret, public string, texts map[string]string) []string {
	windows := secretWindows(secret, public)
	var out []string
	for where, text := range texts {
		for _, w := range windows {
			if strings.Contains(text, w) {
				out = append(out, fmt.Sprintf("%s holds %q", where, w))
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// secretLogMu serialises the capture: the std logger and slog's default are
// process-wide, so two captures must not overlap.
var secretLogMu sync.Mutex

// captureSecretLogs runs f with the std logger and slog's default writing to a
// buffer, and returns what they wrote.
func captureSecretLogs(f func()) string {
	secretLogMu.Lock()
	defer secretLogMu.Unlock()
	var buf bytes.Buffer
	prevSlog, prevWriter, prevFlags := slog.Default(), log.Writer(), log.Flags()
	log.SetOutput(&buf)
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer func() {
		slog.SetDefault(prevSlog)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	}()
	f()
	return buf.String()
}

var (
	secretCtxType = reflect.TypeOf((*context.Context)(nil)).Elem()
	secretErrType = reflect.TypeOf((*error)(nil)).Elem()
)

// secretArg is the argument for one parameter: the input for a string, a live
// context, a stand-in that returns zero values for a func, io.Discard where it
// fits an interface, and the zero value of anything else.
func secretArg(ctx context.Context, pt reflect.Type, input string) reflect.Value {
	switch {
	case pt.Kind() == reflect.String:
		return reflect.ValueOf(input).Convert(pt)
	case pt == secretCtxType:
		return reflect.ValueOf(ctx)
	case pt.Kind() == reflect.Func:
		return reflect.MakeFunc(pt, func([]reflect.Value) []reflect.Value {
			outs := make([]reflect.Value, pt.NumOut())
			for i := range outs {
				outs[i] = reflect.Zero(pt.Out(i))
			}
			return outs
		})
	case pt.Kind() == reflect.Interface && reflect.TypeOf(io.Discard).Implements(pt):
		return reflect.ValueOf(io.Discard)
	}
	return reflect.Zero(pt)
}

// driveSecretOpener calls fn with the input in every string parameter and zero
// values elsewhere, and returns the texts a leak could be in: `error`, `result`
// (a returned refusal value), `panic` and `log`.
func driveSecretOpener(fn any, input string) map[string]string {
	texts := map[string]string{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	v := reflect.ValueOf(fn)
	t := v.Type()
	var args []reflect.Value
	for i := 0; i < t.NumIn(); i++ {
		pt := t.In(i)
		if t.IsVariadic() && i == t.NumIn()-1 {
			if pt.Elem().Kind() == reflect.String {
				args = append(args, reflect.ValueOf(input).Convert(pt.Elem()))
			}
			break
		}
		args = append(args, secretArg(ctx, pt, input))
	}
	texts["log"] = captureSecretLogs(func() {
		defer func() {
			if r := recover(); r != nil {
				texts["panic"] = fmt.Sprint(r)
			}
		}()
		var errs, results []string
		for _, out := range v.Call(args) {
			switch out.Kind() {
			case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func:
				if out.IsNil() {
					continue
				}
			}
			if out.Type().Implements(secretErrType) {
				errs = append(errs, out.Interface().(error).Error())
			} else if strings.Contains(out.Type().String(), "Refusal") {
				results = append(results, fmt.Sprintf("%+v", out.Interface()))
			}
		}
		texts["error"] = strings.Join(errs, "\n")
		texts["result"] = strings.Join(results, "\n")
	})
	return texts
}

// secretFunctionName is the qualified name the runtime gives fn.
func secretFunctionName(fn any) string {
	return runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
}

// secretOpeners is the reviewed table of every function the rule drives, keyed
// `<repo-relative package directory>.<Name>`. TestNoSecretReachesAnError holds
// it complete against the tree.
var secretOpeners = map[string]any{
	"pkg/buildinfo.Parse":                         buildinfo.Parse,
	"pkg/bus/bustest.NewFake":                     bustest.NewFake,
	"pkg/cardcost.ParseSpend":                     cardcost.ParseSpend,
	"pkg/cardcost.ParseTotal":                     cardcost.ParseTotal,
	"pkg/cardcost.ParseUsage":                     cardcost.ParseUsage,
	"internal/cardgen.ParseFindings":              cardgen.ParseFindings,
	"internal/cardgen.ParseLedger":                cardgen.ParseLedger,
	"pkg/cardhdr.ParseBase":                       cardhdr.ParseBase,
	"pkg/cardhdr.ParseTest":                       cardhdr.ParseTest,
	"pkg/cardtree.Parse":                          cardtree.Parse,
	"pkg/cardtree.ParseRegex":                     cardtree.ParseRegex,
	"pkg/cardtree.ParseVerdicts":                  cardtree.ParseVerdicts,
	"internal/check.ParseAllowlist":               check.ParseAllowlist,
	"internal/check.ParseDenyList":                check.ParseDenyList,
	"internal/ci/allowlist.Parse":                 ciallowlist.Parse,
	"pkg/config.OpenFile":                         config.OpenFile,
	"pkg/config.OpenPG":                           config.OpenPG,
	"internal/converge.ParseCerts":                converge.ParseCerts,
	"internal/converge.ParseRetired":              converge.ParseRetired,
	"internal/converge.ParseSince":                converge.ParseSince,
	"internal/converge.ParseVersions":             converge.ParseVersions,
	"pkg/decide.ParseBar":                         decide.ParseBar,
	"pkg/decide.ParseBars":                        decide.ParseBars,
	"pkg/decide.ParseBriefBar":                    decide.ParseBriefBar,
	"pkg/decide.ParseDecided":                     decide.ParseDecided,
	"pkg/decide.ParseGateBars":                    decide.ParseGateBars,
	"pkg/decide.ParseGateOutput":                  decide.ParseGateOutput,
	"pkg/decide.ParseJudgmentBar":                 decide.ParseJudgmentBar,
	"pkg/diffcheck.Parse":                         diffcheck.Parse,
	"pkg/dogfood.NewShipped":                      dogfood.NewShipped,
	"pkg/dogfood.ParseAuthors":                    dogfood.ParseAuthors,
	"pkg/dogfood.ParseCLI":                        dogfood.ParseCLI,
	"pkg/dogfood.ParseHelp":                       dogfood.ParseHelp,
	"pkg/dogfood.ParseReference":                  dogfood.ParseReference,
	"pkg/filelock.ParseStamp":                     filelock.ParseStamp,
	"pkg/fleet.ParseWorkload":                     fleet.ParseWorkload,
	"pkg/friend.NewClaude":                        friend.NewClaude,
	"pkg/friend.NewDeliverer":                     friend.NewDeliverer,
	"pkg/friend.NewestCodexSession":               friend.NewestCodexSession,
	"pkg/friend.NewestConversation":               friend.NewestConversation,
	"pkg/friend.NewestDSHSession":                 friend.NewestDSHSession,
	"pkg/friend.ParseHeld":                        friend.ParseHeld,
	"pkg/friend.ParseJob":                         friend.ParseJob,
	"pkg/friend.ParseLaneCaps":                    friend.ParseLaneCaps,
	"pkg/friend.ParseLimit":                       friend.ParseLimit,
	"pkg/friend.ParsePing":                        friend.ParsePing,
	"pkg/friend.ParsePong":                        friend.ParsePong,
	"pkg/friend.ParseProfile":                     friend.ParseProfile,
	"pkg/friend.ParseMachine":                     friend.ParseMachine,
	"pkg/friend.ParseReadQueue":                   friend.ParseReadQueue,
	"pkg/friend.ParseReadSlots":                   friend.ParseReadSlots,
	"pkg/friend.ParseRow":                         friend.ParseRow,
	"pkg/friend.ParseView":                        friend.ParseView,
	"pkg/hostload.ParseProcStat":                  hostload.ParseProcStat,
	"pkg/hostload.ParseTopCPU":                    hostload.ParseTopCPU,
	"pkg/log.New":                                 nslog.New,
	"internal/nsprint/store.Open":                 nsstore.Open,
	"pkg/nsprint/verbflag.New":                    verbflag.New,
	"pkg/ntable.NewReader":                        ntable.NewReader,
	"pkg/ntable.NewRow":                           ntable.NewRow,
	"pkg/ntable.ParseColumn":                      ntable.ParseColumn,
	"pkg/ntable.ParseColumns":                     ntable.ParseColumns,
	"pkg/ntable.ParseFormula":                     ntable.ParseFormula,
	"pkg/ntable.ParseWidths":                      ntable.ParseWidths,
	"pkg/onboarding.OpeningSentence":              onboarding.OpeningSentence,
	"pkg/pkgselect.ParseDeprecated":               pkgselect.ParseDeprecated,
	"internal/record.DialLedger":                  record.DialLedger,
	"pkg/sandbox.ParseGPUMode":                    sandbox.ParseGPUMode,
	"pkg/secrets.NewSecret":                       secrets.NewSecret,
	"pkg/secrets.OpenSeatFile":                    secrets.OpenSeatFile,
	"pkg/secrets.ParseSopsConfig":                 secrets.ParseSopsConfig,
	"pkg/secrets.ParseStoreFileWithoutDecrypting": secrets.ParseStoreFileWithoutDecrypting,
	"internal/sprint.NewTable":                    sprint.NewTable,
	"internal/sprint.OpenKey":                     sprint.OpenKey,
	"internal/sprint.ParseAttempts":               sprint.ParseAttempts,
	"internal/sprint.ParseDeadline":               sprint.ParseDeadline,
	"internal/sprint.ParseFriendReadReport":       sprint.ParseFriendReadReport,
	"internal/sprint.ParseLaneCap":                sprint.ParseLaneCap,
	"internal/sprint.ParseMembers":                sprint.ParseMembers,
	"internal/sprint.ParseReadCard":               sprint.ParseReadCard,
	"internal/sprint.ParseReaderTiers":            sprint.ParseReaderTiers,
	"internal/sprint.ParseRoute":                  sprint.ParseRoute,
	"internal/sprint.ParseWidth":                  sprint.ParseWidth,
	"internal/sprint.ParseWorkCard":               sprint.ParseWorkCard,
	"internal/sprint/refmodel.New":                refmodel.New,
	"internal/sprint/store.NewDeliverer":          sprintstore.NewDeliverer,
	"pkg/swarm.NewWallReader":                     swarm.NewWallReader,
	"pkg/swarm.OpenCodeStoreLocations":            swarm.OpenCodeStoreLocations,
	"pkg/swarm.ParseChildRules":                   swarm.ParseChildRules,
	"pkg/swarm.ParseIdentity":                     swarm.ParseIdentity,
	"pkg/swarm.ParseRouteList":                    swarm.ParseRouteList,
	"pkg/tlc.Parse":                               tlc.Parse,
	"internal/tokens.ParseDayFile":                tokens.ParseDayFile,
	"internal/tokens.ParseMicro":                  tokens.ParseMicro,
	"internal/tokens.ParseSubject":                tokens.ParseSubject,
	"internal/tokens.ParseWeights":                tokens.ParseWeights,
	"internal/tokens.ParserColumns":               tokens.ParserColumns,
	"pkg/typedrec.ParseTableRefusal":              typedrec.ParseTableRefusal,
}

// secretExempt are the functions the rule finds and does not drive, each with
// the reason; a row is held to the same two-way comparison as the table.
var secretExempt = map[string]string{
	"pkg/hostload.ParseIostat":            "built on darwin only, so a test binary on another platform cannot name it",
	"pkg/hostload.ParseProcLoadavg":       "built on linux only, so a test binary on another platform cannot name it",
	"pkg/hostload.ParseFileNr":            "built on linux only, so a test binary on another platform cannot name it",
	"pkg/hostload.ParseLsof":              "built on darwin only, so a test binary on another platform cannot name it",
	"internal/cairn.Open":                 "writes a session record under the store directory its first string names; driving it would write into the working tree",
	"pkg/nsprint/testutil.NewLocalRemote": "takes a *testing.T and builds a git remote on disk; it is a test fixture, not an opener of a secret",
	"pkg/friend.DialCodexAppServer":       "takes the Codex home, a directory path, never a secret; it dials the app-server socket under it",
}

// secretLeakAllowlist are the functions known to carry a secret-shaped string
// into an error, one `<key> <reason>` per line (a row with no reason is
// refused). It only shrinks: a function that no longer leaks is a red row to
// delete. pkg/config.OpenPG, the case that started the rule, is never a row.
const secretLeakAllowlist = `
pkg/cardtree.ParseRegex echoes the rejected line with %q (pkg/cardtree/tree.go:421)
internal/converge.ParseCerts echoes the file name and the header it read (internal/converge/sources.go:328)
internal/converge.ParseSince echoes the rejected --since value (internal/converge/converge.go:479)
internal/converge.ParseVersions echoes the file name and the header it read (internal/converge/sources.go:297)
pkg/decide.ParseBar echoes the rejected bar with %q (pkg/decide/attempt.go:190)
pkg/decide.ParseBars echoes the rejected bars with %q (pkg/decide/firstread.go:47)
pkg/decide.ParseBriefBar echoes the rejected bar with %q (pkg/decide/brief.go:168)
pkg/decide.ParseGateBars echoes the rejected bars with %q (pkg/decide/gate.go:313)
pkg/decide.ParseJudgmentBar echoes the rejected bar with %q (pkg/decide/judgment.go:243)
pkg/dogfood.ParseAuthors wraps the os.ReadFile error, which names the path argument (pkg/dogfood/authors.go:28)
pkg/dogfood.ParseCLI wraps the os.ReadFile error, which names the path argument (pkg/dogfood/cli.go:97)
pkg/fleet.ParseWorkload echoes its source argument (pkg/fleet/certify.go:184)
pkg/friend.NewDeliverer echoes an unknown harness with %q (pkg/friend/adapter.go:253)
pkg/friend.NewestCodexSession wraps the lstat error, which names the dir argument (pkg/friend/codex_session.go:21)
pkg/friend.NewestDSHSession echoes its dir argument (pkg/friend/adapter_dsh.go:65)
pkg/ntable.ParseColumn echoes the rejected column name with %q (pkg/ntable/ntable.go:405)
pkg/ntable.ParseColumns echoes the rejected column name with %q (pkg/ntable/ntable.go:405)
pkg/ntable.ParseFormula echoes the rejected projection with %q (pkg/ntable/ntable.go:133)
pkg/ntable.ParseWidths echoes the rejected part with %q (pkg/ntable/ntable.go:536)
pkg/onboarding.OpeningSentence echoes the tool name and the first help line (pkg/onboarding/opening.go:46)
pkg/sandbox.ParseGPUMode echoes the rejected mode in a Refusal (pkg/sandbox/gpu.go:30)
pkg/secrets.OpenSeatFile echoes the seat name and the store path in its preflight refusal (pkg/secrets/seatfile.go:126, pkg/secrets/seatfile.go:149)
pkg/secrets.ParseSopsConfig wraps the read error, which names the store path (pkg/secrets/store.go:82)
pkg/secrets.ParseStoreFileWithoutDecrypting returns the os.Open error, which names the path argument (pkg/secrets/store.go:250)
internal/sprint.ParseAttempts echoes the rejected value with %q (internal/sprint/brief_bound.go:83)
internal/sprint.ParseDeadline echoes the rejected value with %q (internal/sprint/deadline.go:165)
internal/sprint.ParseMembers echoes the rejected width with %q (internal/sprint/width.go:42)
internal/sprint.ParseReaderTiers echoes the rejected tier names (internal/sprint/reader_tiers.go:51)
internal/sprint.ParseRoute echoes the rejected route with %q (internal/sprint/remind.go:108)
internal/sprint.ParseWidth echoes the rejected width with %q (internal/sprint/width.go:42)
internal/sprint/store.NewDeliverer echoes the rejected route with %q (internal/sprint/store/remind.go:147 returns the error of internal/sprint/remind.go:108; the bus refusal at remind.go:155 echoes it too)
pkg/swarm.ParseIdentity echoes the rejected identity with %q (pkg/swarm/staging.go:89)
internal/tokens.ParseWeights echoes the rejected weights (internal/tokens/claude_session.go:53)
`

// parseSecretAllowlist reads the rows into key -> reason, naming a row with no
// reason.
func parseSecretAllowlist(raw string) (map[string]string, []string) {
	rows := map[string]string{}
	var bad []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, reason, _ := strings.Cut(line, " ")
		if strings.TrimSpace(reason) == "" {
			bad = append(bad, key+": a row needs a reason after the function")
			continue
		}
		rows[key] = strings.TrimSpace(reason)
	}
	return rows, bad
}

// secretOpenersInTree is every exported top-level function in the non-test Go of
// cmd/ and internal/ whose name begins Open, Parse, Dial or New and that takes a
// string, found by go/ast and keyed `<package directory>.<Name>`.
func secretOpenersInTree(files []*treeFile) map[string]bool {
	found := map[string]bool{}
	for _, f := range files {
		if f.AST == nil || f.HasDirNamed("testdata") {
			continue
		}
		dir := f.Rel[:strings.LastIndex(f.Rel, "/")]
		for _, d := range f.AST.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || !fd.Name.IsExported() || !secretOpenerName(fd.Name.Name) {
				continue
			}
			if takesString(fd.Type.Params) {
				found[dir+"."+fd.Name.Name] = true
			}
		}
	}
	return found
}

func secretOpenerName(name string) bool {
	for _, p := range []string{"Open", "Parse", "Dial", "New"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// takesString is whether a parameter list holds a string or ...string.
func takesString(params *ast.FieldList) bool {
	if params == nil {
		return false
	}
	for _, p := range params.List {
		switch x := p.Type.(type) {
		case *ast.Ident:
			if x.Name == "string" {
				return true
			}
		case *ast.Ellipsis:
			if id, ok := x.Elt.(*ast.Ident); ok && id.Name == "string" {
				return true
			}
		}
	}
	return false
}

// secretTableFindings compares what the tree has with the table and the exempt
// rows, in both directions, and checks each table value is the function its key
// names.
func secretTableFindings(found map[string]bool, table map[string]any, exempt map[string]string) []string {
	var out []string
	for key := range found {
		_, driven := table[key]
		_, skipped := exempt[key]
		if !driven && !skipped {
			out = append(out, key+": an exported Open*/Parse*/Dial*/New* function that takes a string is not in secretOpeners; add it there, or to secretExempt with the reason it cannot be driven safely")
		}
	}
	for key, fn := range table {
		if !found[key] {
			out = append(out, key+": a secretOpeners row names no such function; delete the row")
			continue
		}
		want := "/" + key
		if !strings.HasSuffix(secretFunctionName(fn), want) {
			out = append(out, fmt.Sprintf("%s: the row holds %s, not that function", key, secretFunctionName(fn)))
		}
	}
	for key := range exempt {
		if !found[key] {
			out = append(out, key+": a secretExempt row names no such function; delete the row")
		}
		if _, dup := table[key]; dup {
			out = append(out, key+": is both driven and exempt; keep one")
		}
	}
	sort.Strings(out)
	return out
}

// secretLeakFindings drives every function in the table with every shape and
// returns the functions that leak, key -> `<shape>: <where> holds "<window>"`.
func secretLeakFindings(table map[string]any, shapes []secretShape) map[string][]string {
	leaks := map[string][]string{}
	for key, fn := range table {
		for _, s := range shapes {
			for _, l := range secretLeaks(s.secret, s.public, driveSecretOpener(fn, s.input)) {
				leaks[key] = append(leaks[key], s.name+": "+l)
			}
		}
		sort.Strings(leaks[key])
	}
	return leaks
}

// secretVerdict compares the leaks with the allowlist, in both directions.
func secretVerdict(leaks map[string][]string, allowed map[string]string) []string {
	var out []string
	for key, ls := range leaks {
		if _, ok := allowed[key]; !ok && len(ls) > 0 {
			out = append(out, fmt.Sprintf("%s carries a secret into its output (%s); refuse with the error's type or a fixed sentence and never the input (pkg/config/pg.go openPGWithin), the allowlist does not grow", key, ls[0]))
		}
	}
	for key := range allowed {
		if len(leaks[key]) == 0 {
			out = append(out, key+": listed in secretLeakAllowlist and no longer leaks; delete the row")
		}
	}
	sort.Strings(out)
	return out
}

// TestNoSecretReachesAnError is the class rule; the helpers above carry its
// reasoning, and the witness below holds the check itself.
func TestNoSecretReachesAnError(t *testing.T) {
	t.Parallel()
	files := repoTree(t).GoFilesUnder(false, "cmd", "internal")
	found := secretOpenersInTree(files)
	require.NotEmpty(t, found, "the walk found no Open*/Parse*/Dial*/New* function; a rule that checks nothing passes")
	for _, f := range secretTableFindings(found, secretOpeners, secretExempt) {
		t.Error(f)
	}
	allowed, bad := parseSecretAllowlist(secretLeakAllowlist)
	for _, b := range bad {
		t.Error(b)
	}
	assert.NotContains(t, allowed, "pkg/config.OpenPG", "the Postgres DSN opener is the case the rule was written for and is never allowlisted")
	for _, f := range secretVerdict(secretLeakFindings(secretOpeners, secretShapes), allowed) {
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
	dsn := secretShapes[0]
	or := secretShapes[4]
	leaks := func(fn func(string) error, s secretShape) []string {
		return secretLeaks(s.secret, s.public, driveSecretOpener(fn, s.input))
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
		for _, s := range secretShapes {
			assert.Empty(t, leaks(secretFixtures.redacted, s), s.name)
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
		for _, s := range secretShapes {
			assert.Contains(t, s.input, s.secret, s.name)
			for _, w := range secretWindows(s.secret, s.public) {
				if other, dup := seen[w]; dup {
					assert.Equal(t, s.name, other, "the window %q is in two shapes; each shape's marker is its own", w)
				}
				seen[w] = s.name
			}
		}
	})
	t.Run("the verdict refuses an unlisted leak and a stale row", func(t *testing.T) {
		t.Parallel()
		leaking := map[string][]string{"internal/x.Open": {`dsn: error holds "pwQm4Zt9"`}}
		got := secretVerdict(leaking, nil)
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "internal/x.Open carries a secret")
		assert.Empty(t, secretVerdict(leaking, map[string]string{"internal/x.Open": "echoes"}))
		stale := secretVerdict(nil, map[string]string{"internal/x.Open": "echoes"})
		require.Len(t, stale, 1)
		assert.Contains(t, stale[0], "no longer leaks")
		_, bad := parseSecretAllowlist("internal/x.Open")
		assert.Len(t, bad, 1)
	})
	t.Run("a table that misses a function or names a stale one is refused", func(t *testing.T) {
		t.Parallel()
		found := map[string]bool{"pkg/config.OpenPG": true, "internal/new.Open": true}
		table := map[string]any{"pkg/config.OpenPG": config.OpenPG, "internal/gone.Parse": config.OpenPG}
		got := secretTableFindings(found, table, nil)
		require.Len(t, got, 2)
		assert.Contains(t, got[0], "internal/gone.Parse: a secretOpeners row names no such function")
		assert.Contains(t, got[1], "internal/new.Open: an exported Open*")
		wrong := secretTableFindings(map[string]bool{"pkg/config.OpenFile": true}, map[string]any{"pkg/config.OpenFile": config.OpenPG}, nil)
		require.Len(t, wrong, 1)
		assert.Contains(t, wrong[0], "not that function")
	})
	t.Run("the walk finds a function by name and by a string parameter", func(t *testing.T) {
		t.Parallel()
		found := secretOpenersInTree(repoTree(t).GoFilesUnder(false, "pkg/config"))
		assert.True(t, found["pkg/config.OpenPG"])
		assert.True(t, found["pkg/config.OpenFile"])
		assert.False(t, found["pkg/config.Redact"], "a name outside Open/Parse/Dial/New is not read")
	})
}
