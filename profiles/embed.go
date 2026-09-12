// Package profiles holds the generated-policy TEMPLATES this repository ships, and it
// exists so that the tool and profiles/darwin-check.sh fill ONE text rather than two
// (SPEC-SANDBOX rule 15, and the work list's "the file is embedded with go:embed").
//
// go:embed cannot reach outside its own directory, so the embed lives beside the
// template rather than in internal/sandbox. Nothing else belongs here: this package
// carries no logic, so that the only way the profile text can change is by editing the
// template a reader can read.
package profiles

import _ "embed"

// DarwinTemplate is profiles/darwin.sb.tmpl verbatim, markers and all. The generator in
// internal/sandbox replaces the five marker lines the template's own header documents.
//
//go:embed darwin.sb.tmpl
var DarwinTemplate string
