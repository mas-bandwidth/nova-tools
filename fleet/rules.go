// Package fleet holds the child rules files this repository's members inject into every
// card at stage time (nova-tools#5174 rule 6, rules by reference): fleet/child-rules.txt,
// and fleet/child-rules.<repo>.txt for a repository with rules of its own. go:embed cannot
// reach outside its own directory, so the embed lives beside the files; the member holds
// the rules of the build it runs, and the logic that reads them is pkg/swarm's
// (heldrules.go). Nothing else belongs here.
package fleet

import (
	"bytes"
	"embed"
	"fmt"
	"regexp"
	"strings"
)

// Rules is every fleet/child-rules*.txt of this build, by base name.
//
//go:embed child-rules*.txt
var Rules embed.FS

// A key reaches a process only by nova-secrets exec's --only, never through a
// preserved environment, a command line or an environment dump: the novadog
// auth.log leak of 2026-10-19. LeakGuard is the static refusal the fleet's
// plays and templates are held to (tla/ProviderKey.tla NoValueOutsideTheStore;
// docs/SPEC-SECRETS.md, "The value reaches a process only by exec").
var (
	sudoPreserveEnv = regexp.MustCompile(`--preserve-env(?:=|\s|$)`)
	sudoEnvironment = regexp.MustCompile(`(?:^|[\s;&|(])sudo\s+(?:[^#\n]*\s)?-[A-Za-z]*E(?:\s|$)`)
	printEnv        = regexp.MustCompile(`(?:^|[\s;&|(])printenv(?:\s|$)`)
	envDump         = regexp.MustCompile(`(?:^|[\s;&|(])(?:/usr/bin/|/bin/)?env\s*(?:$|[|>])`)
)

// LeakGuard refuses a play or template body that passes a key through sudo
// --preserve-env, sudo -E, printenv or a bare env dump. name and the line
// number are in the refusal so the fix is the line, never the whole file.
func LeakGuard(name string, body []byte) error {
	for i, line := range bytes.Split(body, []byte("\n")) {
		text := string(line)
		switch {
		case sudoPreserveEnv.MatchString(text):
			return leakRefusal(name, i+1, "sudo --preserve-env", text)
		case sudoEnvironment.MatchString(text):
			return leakRefusal(name, i+1, "sudo -E", text)
		case printEnv.MatchString(text):
			return leakRefusal(name, i+1, "a printenv dump", text)
		case envDump.MatchString(text):
			return leakRefusal(name, i+1, "an env dump", text)
		}
	}
	return nil
}

func leakRefusal(name string, line int, how, text string) error {
	return fmt.Errorf("%s:%d: passes the environment through %s (%s); a key reaches a process only by nova-secrets exec --only, never sudo's preserved environment, an argv or an environment dump", name, line, how, strings.TrimSpace(text))
}
