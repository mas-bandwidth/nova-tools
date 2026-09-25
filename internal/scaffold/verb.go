package scaffold

import (
	"embed"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed templates/verb/*
var verbTemplates embed.FS

var verbNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

type verbData struct {
	Tool      string
	Verb      string
	CamelVerb string
}

// Verb writes a new CLI verb skeleton into root.
func Verb(root, tool, verb string) ([]string, error) {
	if !verbNameRe.MatchString(tool) {
		return nil, fmt.Errorf("tool name %q must start with a lowercase letter and contain only 1-32 lowercase letters, digits, '_' or '-'", tool)
	}
	if token.IsKeyword(tool) {
		return nil, fmt.Errorf("tool name %q is a Go keyword", tool)
	}
	if !verbNameRe.MatchString(verb) {
		return nil, fmt.Errorf("verb name %q must start with a lowercase letter and contain only 1-32 lowercase letters, digits, '_' or '-'", verb)
	}
	if token.IsKeyword(verb) {
		return nil, fmt.Errorf("verb name %q is a Go keyword", verb)
	}
	if info, err := os.Stat(filepath.Join(root, "go.mod")); err != nil || info.IsDir() {
		return nil, fmt.Errorf("%s is not a nova-tools checkout (no go.mod) — run from repository root or pass --root", root)
	}
	if ok, err := hasMain(filepath.Join(root, "cmd", tool)); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("cmd/%s has no func main — new-verb adds a verb to an existing tool, and a verb in a package with no entry point stops the tree building; create cmd/%s/main.go with func main and its dispatch switch first", tool, tool)
	}

	v := verbData{
		Tool:      tool,
		Verb:      verb,
		CamelVerb: toCamel(verb),
	}

	targets := []struct {
		tmpl string
		rel  string
	}{
		{"templates/verb/verb.go.tmpl", fmt.Sprintf("cmd/%s/%s.go", v.Tool, v.Verb)},
		{"templates/verb/verb_test.go.tmpl", fmt.Sprintf("cmd/%s/%s_test.go", v.Tool, v.Verb)},
		{"templates/verb/fixture.txt.tmpl", fmt.Sprintf("cmd/%s/testdata/%s/fixture.txt", v.Tool, v.Verb)},
		{"templates/verb/verb.mk.tmpl", fmt.Sprintf("make/verb_%s_%s.mk", v.Tool, v.Verb)},
	}

	var outs []Planned
	for _, t := range targets {
		data, err := Render(verbTemplates, t.tmpl, v)
		if err != nil {
			return nil, err
		}
		outs = append(outs, Planned{
			Rel:  t.rel,
			Data: data,
		})
	}

	return Write(root, outs)
}

// hasMain reports whether dir holds a non-test Go file of package main that
// declares a plain func main (no receiver). A missing dir is simply false.
func hasMain(dir string) (bool, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%s: cannot list it to find func main: %w", dir, err)
	}
	for _, e := range ents {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return false, fmt.Errorf("%s: cannot parse it to find func main: %w", filepath.Join(dir, name), err)
		}
		if f.Name.Name != "main" {
			continue
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "main" {
				return true, nil
			}
		}
	}
	return false, nil
}

// Dispatch is the case a scaffolded verb needs in its tool's dispatch switch,
// calling the function verb.go.tmpl declares. new-verb prints it and never
// edits the switch itself (rowan hold 6 on #3616, item 4).
func Dispatch(verb string) string {
	return fmt.Sprintf("case %q:\n\treturn cmd%s(args[1:], stdout, stderr)", verb, toCamel(verb))
}

// DispatchNote is what new-verb prints after the files it wrote: where the
// case goes, then the case itself, indented as it sits in the switch.
func DispatchNote(tool, verb string) string {
	return fmt.Sprintf("add to the dispatch switch in cmd/%s (new-verb never edits it):\n\t%s\n",
		tool, strings.ReplaceAll(Dispatch(verb), "\n", "\n\t"))
}
