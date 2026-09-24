package scaffold

import (
	"embed"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
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
