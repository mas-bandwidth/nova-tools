package reconcile_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// checkForgeReads inspects an AST for forge read violations (#2930 rev 5).
func checkForgeReads(fset *token.FileSet, f *ast.File) []string {
	var violations []string

	httpAliases := make(map[string]bool)
	var httpDotImport bool

	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "net/http" {
			continue
		}
		if imp.Name == nil {
			httpAliases["http"] = true
		} else if imp.Name.Name == "." {
			httpDotImport = true
		} else if imp.Name.Name != "_" {
			httpAliases[imp.Name.Name] = true
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && (httpAliases[id.Name] || id.Name == "http") && (x.Sel.Name == "MethodGet" || x.Sel.Name == "MethodHead") {
				violations = append(violations, fmt.Sprintf("%s: %s.%s in the reconcile package (a forge read)", fset.Position(x.Pos()), id.Name, x.Sel.Name))
			}
			if x.Sel.Name == "FindOpen" {
				violations = append(violations, fmt.Sprintf("%s: FindOpen in the reconcile package (a forge read)", fset.Position(x.Pos())))
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				val, err := strconv.Unquote(x.Value)
				if err == nil && (val == "GET" || val == "HEAD") {
					violations = append(violations, fmt.Sprintf("%s: %s literal in the reconcile package (a forge read)", fset.Position(x.Pos()), x.Value))
				}
			}
		case *ast.Ident:
			if x.Name == "FindOpen" {
				violations = append(violations, fmt.Sprintf("%s: FindOpen declared in the reconcile package (a forge read)", fset.Position(x.Pos())))
			}
			if httpDotImport && (x.Name == "MethodGet" || x.Name == "MethodHead") {
				violations = append(violations, fmt.Sprintf("%s: %s via dot-import in the reconcile package (a forge read)", fset.Position(x.Pos()), x.Name))
			}
		}
		return true
	})
	return violations
}

// TestNoForgeReadInReconcile: GitHub is a git remote only (#2930 rev 5). No
// production file of the reconcile package names http.MethodGet or FindOpen.
// Test files (*_test.go) are excluded because test fixtures in TestASTForgeReadGuard
// intentionally contain these constructs to verify the AST guard.
func TestNoForgeReadInReconcile(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	must(t, err)
	if len(files) == 0 {
		t.Fatal("no Go files in the reconcile package directory")
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		f, err := parser.ParseFile(fset, name, nil, 0)
		must(t, err)
		for _, v := range checkForgeReads(fset, f) {
			t.Errorf("%s: %s", name, v)
		}
	}
	if checked == 0 {
		t.Fatal("no non-test Go files checked in the reconcile package")
	}
}

// TestASTForgeReadGuard verifies that checkForgeReads detects forge reads
// even across raw string literals (`GET`, `HEAD`), aliased imports
// (import h "net/http"), and dot-imports (import . "net/http").
func TestASTForgeReadGuard(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	tests := []struct {
		name      string
		src       string
		wantMatch bool
	}{
		{
			name:      "raw string GET",
			src:       "package p\nvar _ = `GET`\n",
			wantMatch: true,
		},
		{
			name:      "raw string HEAD",
			src:       "package p\nvar _ = `HEAD`\n",
			wantMatch: true,
		},
		{
			name:      "aliased import MethodGet",
			src:       "package p\nimport h \"net/http\"\nvar _ = h.MethodGet\n",
			wantMatch: true,
		},
		{
			name:      "aliased import MethodHead",
			src:       "package p\nimport custom \"net/http\"\nvar _ = custom.MethodHead\n",
			wantMatch: true,
		},
		{
			name:      "dot import MethodGet",
			src:       "package p\nimport . \"net/http\"\nvar _ = MethodGet\n",
			wantMatch: true,
		},
		{
			name:      "interpreted string GET",
			src:       "package p\nvar _ = \"GET\"\n",
			wantMatch: true,
		},
		{
			name:      "interpreted string HEAD",
			src:       "package p\nvar _ = \"HEAD\"\n",
			wantMatch: true,
		},
		{
			name:      "standard import MethodGet",
			src:       "package p\nimport \"net/http\"\nvar _ = http.MethodGet\n",
			wantMatch: true,
		},
		{
			name:      "FindOpen call",
			src:       "package p\nfunc f() { host.FindOpen() }\n",
			wantMatch: true,
		},
		{
			name:      "FindOpen declaration",
			src:       "package p\nfunc FindOpen() {}\n",
			wantMatch: true,
		},
		{
			name:      "clean net/http MethodPost",
			src:       "package p\nimport \"net/http\"\nvar _ = http.MethodPost\nvar _ = \"POST\"\n",
			wantMatch: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parser.ParseFile(fset, tc.name+".go", tc.src, 0)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			violations := checkForgeReads(fset, f)
			if tc.wantMatch && len(violations) == 0 {
				t.Fatalf("%s: expected forge read violation, got none", tc.name)
			}
			if !tc.wantMatch && len(violations) > 0 {
				t.Fatalf("%s: expected 0 violations, got %v", tc.name, violations)
			}
		})
	}
}

// ---- fixtures ----

// runCard seeds a dealt card and drives it to running through the real
// launched and beat functions.
func runCard(t *testing.T, ctx context.Context, st *store.Store, client *redis.Client, id card.Identity, token string) {
	t.Helper()
	seedDealt(t, ctx, client, id, token)
	branch := fmt.Sprintf("nova/%s/%s-a%d", id.Sprint, id.Label, id.Attempt)
	if r, err := card.Launched(ctx, st, card.LaunchRequest{Sprint: id.Sprint, Label: id.Label, Token: token, Branch: branch, JobDir: t.TempDir()}); err != nil || !r.Resolved {
		t.Fatalf("launched: %+v %v", r, err)
	}
	if r, err := card.Beat(ctx, st, card.BeatRequest{Sprint: id.Sprint, Label: id.Label, Token: token}); err != nil || !r.Resolved {
		t.Fatalf("beat: %+v %v", r, err)
	}
}

func seedDealt(t *testing.T, ctx context.Context, client *redis.Client, id card.Identity, token string) {
	t.Helper()
	now, err := client.Time(ctx).Result()
	must(t, err)
	must(t, client.HSet(ctx, card.CardKey(id.Sprint, id.Label), map[string]string{
		"state": "dealt", "attempt": fmt.Sprint(id.Attempt), "token": token, "token_sha": card.TokenSHA(token),
		"identity": id.String(), "bench": id.Bench, "base_sha": id.BaseSHA, "priority": "10",
		"dealt_at": fmt.Sprint(now.UnixMilli()),
	}).Err())
	must(t, client.SAdd(ctx, card.IdxKey(id.Sprint, "dealt"), id.Label).Err())
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
