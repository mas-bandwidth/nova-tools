package main

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"
)

func TestEveryVerbFlagIsInHelpAndCLIDoc(t *testing.T) {
	docRaw, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(docRaw)
	old := observeVerbFlags
	defer func() { observeVerbFlags = old }()
	seen := map[string]*flag.FlagSet{}
	observeVerbFlags = func(verb string, fs *flag.FlagSet) { seen[verb] = fs }
	for _, verb := range []string{"route", "review", "outcome", "tune", "classify", "help", "log"} {
		var out, errOut bytes.Buffer
		run([]string{verb, "--help"}, &out, &errOut)
		fs := seen[verb]
		if fs == nil {
			t.Fatalf("%s FlagSet was not observed", verb)
		}
		help := verbUsage(usage, verb)
		start := strings.Index(doc, "### "+verb)
		if start < 0 {
			t.Fatalf("docs/CLI.md has no %s section", verb)
		}
		section := doc[start:]
		if next := strings.Index(section[4:], "\n### "); next >= 0 {
			section = section[:next+4]
		}
		fs.VisitAll(func(f *flag.Flag) {
			needle := "--" + f.Name
			if !strings.Contains(help, needle) {
				t.Errorf("%s help omits %s", verb, needle)
			}
			if !strings.Contains(section, needle) {
				t.Errorf("%s docs omit %s", verb, needle)
			}
		})
	}
}
