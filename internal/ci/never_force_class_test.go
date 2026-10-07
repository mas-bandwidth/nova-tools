package ci

import (
	"testing"
)

// never_force_class_test.go is the never_force class test: nothing in nova-tools
// may rewrite a shared ref. The test scans Go, shell, Makefile, workflow YAML,
// and card templates for force push patterns when used against refs that are
// not the caller's own job branch (origin/<anything>, dev, main).
//
// THE RULE. No file under cmd/ or internal/ may use push --force, push -f,
// --force-with-lease, push origin +, or reset --hard origin/ against a shared
// ref. Legitimate uses are listed in never_force_allowlist.txt.
//
// THE TEST. TestNoForcePushOrHardResetOfASharedRef scans the designated files
// for force push patterns and refuses those against shared refs that are not
// on the allowlist.
//
// THE LEDGER. never_force_allowlist.txt is shrink-only: it holds one path:line
// per legitimate use, with a reason. A new entry is a refusal; a stale entry
// whose file or line no longer matches fails the test.
