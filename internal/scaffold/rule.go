package scaffold

import (
	"embed"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed templates/rule/*
var ruleTemplates embed.FS

var ruleNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

type ruleData struct {
	Name      string
	CamelName string
}

func toCamel(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for i := range parts {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// Rule writes a new class rule skeleton into root.
func Rule(root, name string) ([]string, error) {
	if !ruleNameRe.MatchString(name) {
		return nil, fmt.Errorf("rule name %q must start with a lowercase letter and contain only 1-32 lowercase letters, digits, '_' or '-'", name)
	}
	if token.IsKeyword(name) {
		return nil, fmt.Errorf("rule name %q is a Go keyword", name)
	}
	if info, err := os.Stat(filepath.Join(root, "go.mod")); err != nil || info.IsDir() {
		return nil, fmt.Errorf("%s is not a nova-tools checkout (no go.mod) — run from repository root or pass --root", root)
	}

	r := ruleData{
		Name:      name,
		CamelName: toCamel(name),
	}

	targets := []struct {
		tmpl string
		rel  string
	}{
		{"templates/rule/class_test.go.tmpl", fmt.Sprintf("internal/ci/%s_class_test.go", r.Name)},
		{"templates/rule/fixture.txt.tmpl", fmt.Sprintf("internal/ci/testdata/%s/fixture.txt", r.Name)},
		{"templates/rule/rule.mk.tmpl", fmt.Sprintf("make/rule_%s.mk", r.Name)},
	}

	var outs []Planned
	for _, t := range targets {
		data, err := Render(ruleTemplates, t.tmpl, r)
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
