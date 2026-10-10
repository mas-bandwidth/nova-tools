// Package secretcheck is the harness of the class rule "no secret reaches an error"
// (docs/SPEC-CI.md, `secrets-never-in-errors`): secret-shaped inputs, a driver that hands
// them to a function by reflection and reads what came back, and the two-way comparisons
// of a reviewed table and allowlist with what a walk of the tree finds. internal/ci's
// TestNoSecretReachesAnError holds the rule over this repository's tree; a tree whose
// packages this repository cannot import (nova-sprint's) holds it with the same harness.
// It is test support: only tests import it.
package secretcheck

import (
	"bytes"
	"context"
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
	"time"
)

// Shape is one secret-shaped input: the string a function is handed, the
// secret inside it, and the public prefix of a token whose own spelling is not
// the secret.
type Shape struct {
	Name   string
	Input  string
	Secret string
	Public string
}

// Window is the substring length that counts as the secret reaching a
// text: eight bytes of it, anywhere.
const Window = 8

// Shapes are the inputs. Every secret holds a marker no other shape, no
// message of this tree and no path shares.
var Shapes = func() []Shape {
	dsnPW := "pwQm4Zt9Xv2LkR7bNc5W"
	urlPW := "pwJt8Hs3Yd6FwP1gUe9A"
	kvPW := "pwVx2Kb7Mn4RtC8hLs3E"
	userPW := "pwGd5Zc9Qa1XvT6mHb2K"
	orToken := "sk-or-v1-" + "Zq7xK2mVd3Wn8Ys5Jc0Tu6Pe1Gf4Ba9Lr2Oi7Hk"
	ghToken := "ghp_" + "Fh6Rj3Ux8Cw1Nb5Ty9Ea2Lv4Mk7Sd0Pq"
	antToken := "sk-ant-api03-" + "Jw4Ct9Bn2Xs7Hy1Rk5Vd8Fe3Zm6Qa0Lg-Uo"
	return []Shape{
		{"dsn-url-unreachable", "postgres://nova:" + dsnPW + "@%2Fnowhere-nova-ci/nova", dsnPW, ""},
		{"dsn-url-malformed", "postgres://nova:" + urlPW + "@db.invalid:notaport/nova", urlPW, ""},
		{"dsn-keyword-malformed", "host=/nowhere-nova-ci user=nova port=notaport password=" + kvPW, kvPW, ""},
		{"url-userinfo", "https://" + "deploy" + ":" + userPW + "@" + "git.invalid" + "/nova.git", userPW, ""},
		{"openrouter-token", orToken, orToken, "sk-or-v1-"},
		{"github-token", ghToken, ghToken, "ghp_"},
		{"anthropic-token", antToken, antToken, "sk-ant-api03-"},
	}
}()

// Windows is every 8-byte substring of s that lies after the public
// prefix: a token's own spelling (sk-or-v1-, ghp_) is not the secret.
func Windows(s, public string) []string {
	var out []string
	for i := 0; i+Window <= len(s); i++ {
		if i < len(public) {
			continue
		}
		out = append(out, s[i:i+Window])
	}
	return out
}

// Leaks is each named text that holds an 8-byte substring of the secret,
// as `<where> holds "<window>"`, sorted by where.
func Leaks(secret, public string, texts map[string]string) []string {
	windows := Windows(secret, public)
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

// logMu serialises the capture: the std logger and slog's default are
// process-wide, so two captures must not overlap.
var logMu sync.Mutex

// captureLogs runs f with the std logger and slog's default writing to a
// buffer, and returns what they wrote.
func captureLogs(f func()) string {
	logMu.Lock()
	defer logMu.Unlock()
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
	ctxType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errType = reflect.TypeOf((*error)(nil)).Elem()
)

// arg is the argument for one parameter: the input for a string, a live
// context, a stand-in that returns zero values for a func, io.Discard where it
// fits an interface, and the zero value of anything else.
func arg(ctx context.Context, pt reflect.Type, input string) reflect.Value {
	switch {
	case pt.Kind() == reflect.String:
		return reflect.ValueOf(input).Convert(pt)
	case pt == ctxType:
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

// Drive calls fn with the input in every string parameter and zero
// values elsewhere, and returns the texts a leak could be in: `error`, `result`
// (a returned refusal value), `panic` and `log`.
func Drive(fn any, input string) map[string]string {
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
		args = append(args, arg(ctx, pt, input))
	}
	texts["log"] = captureLogs(func() {
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
			if out.Type().Implements(errType) {
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

// FunctionName is the qualified name the runtime gives fn.
func FunctionName(fn any) string {
	return runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
}

// ParseAllowlist reads the rows into key -> reason, naming a row with no
// reason.
func ParseAllowlist(raw string) (map[string]string, []string) {
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

// OpenersIn is every exported top-level function in the non-test Go of
// cmd/ and internal/ whose name begins Open, Parse, Dial or New and that takes a
// string, found by go/ast and keyed `<package directory>.<Name>`.
// File is one Go file of a tree: its path from the tree's root, its syntax (nil when it
// did not parse), and whether a directory on its path is named testdata.
type File struct {
	Rel      string
	AST      *ast.File
	Testdata bool
}

func OpenersIn(files []File) map[string]bool {
	found := map[string]bool{}
	for _, f := range files {
		if f.AST == nil || f.Testdata {
			continue
		}
		dir := f.Rel[:strings.LastIndex(f.Rel, "/")]
		for _, d := range f.AST.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || !fd.Name.IsExported() || !OpenerName(fd.Name.Name) {
				continue
			}
			if TakesString(fd.Type.Params) {
				found[dir+"."+fd.Name.Name] = true
			}
		}
	}
	return found
}

func OpenerName(name string) bool {
	for _, p := range []string{"Open", "Parse", "Dial", "New"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// TakesString is whether a parameter list holds a string or ...string.
func TakesString(params *ast.FieldList) bool {
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

// TableFindings compares what the tree has with the table and the exempt
// rows, in both directions, and checks each table value is the function its key
// names.
func TableFindings(found map[string]bool, table map[string]any, exempt map[string]string) []string {
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
		if !strings.HasSuffix(FunctionName(fn), want) {
			out = append(out, fmt.Sprintf("%s: the row holds %s, not that function", key, FunctionName(fn)))
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

// LeakFindings drives every function in the table with every shape and
// returns the functions that leak, key -> `<shape>: <where> holds "<window>"`.
func LeakFindings(table map[string]any, shapes []Shape) map[string][]string {
	leaks := map[string][]string{}
	for key, fn := range table {
		for _, s := range shapes {
			for _, l := range Leaks(s.Secret, s.Public, Drive(fn, s.Input)) {
				leaks[key] = append(leaks[key], s.Name+": "+l)
			}
		}
		sort.Strings(leaks[key])
	}
	return leaks
}

// Verdict compares the leaks with the allowlist, in both directions.
func Verdict(leaks map[string][]string, allowed map[string]string) []string {
	var out []string
	for key, ls := range leaks {
		if _, ok := allowed[key]; !ok && len(ls) > 0 {
			out = append(out, fmt.Sprintf("%s carries a secret into its output (%s); refuse with the error's type or a fixed sentence and never the input (internal/config/pg.go openPGWithin), the allowlist does not grow", key, ls[0]))
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
