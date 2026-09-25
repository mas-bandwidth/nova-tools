package ci

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// progressRegistryFile is the ONE place this repository answers "is this line
// nova-bus progress or nova-bus protocol". Both halves of the rule are held
// against it: nova-bus's own test refuses a progress prefix on stdout
// (cmd/nova-bus, TestProgressNeverEntersTheProtocolStream), and every consumer
// that reads nova-bus's output drops a progress line before it classifies
// anything (internal/wake, TestProgressIsNeverRelayedAsABusLine).
const progressRegistryFile = "internal/bus/protocol.go"

// registryReaders are the ways a package may satisfy the rule. Either it asks
// the registry itself, or it hands the bytes to the ONE classifier that asks it
// -- internal/wake's Bus.Classify, which both nova-wake verbs run, because the
// 2026-09-10 hurt was two readers of the same stream and one of them wrong.
var registryReaders = []string{"bus.IsProgress(", "Classify("}

// TestEveryNovaBusConsumerDropsProgressLines is the class rule, kept where a
// future consumer will trip over it.
//
// Glenn's rule has two halves: a program that takes longer than 0.1 s says what
// it is doing on stderr, AND a progress line never enters a protocol stream a
// consumer parses. On 2026-09-18 the second half was paid for -- nova-bus's
// since-walk narrated `INBOX WALK commits=1/1 notes=0 elapsed=3ms` on stderr,
// exactly as the first half asks, and nova-wake reads that program's stdout and
// stderr TOGETHER so that an INBOX REFUSED is never lost. The progress line
// reached a classifier whose default case PRINTS, was relayed as
// `WAKE BUS LINE INBOX WALK ...`, counted as a change in the world, and ended a
// poll before the mail it was waiting for came down.
//
// The instance fix would have been to teach nova-wake that one token. The class
// fix is that ANY program in this tree that starts nova-bus AND READS WHAT IT
// SAID asks internal/bus whether a line is progress -- so the next progress
// line is one entry in one registry and is safe in every consumer at once.
//
// A start that DISCARDS both streams is not a consumer of the protocol and is
// not held to this: internal/pulse sends a note and reads nothing back.
func TestEveryNovaBusConsumerDropsProgressLines(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	if _, err := os.Stat(filepath.Join(tree.Root, filepath.FromSlash(progressRegistryFile))); err != nil {
		t.Fatalf("%s is the one registry both halves of the rule read: %v", progressRegistryFile, err)
	}

	var readers, discarders, missing []string
	for _, dir := range []string{"cmd", "internal"} {
		for _, f := range tree.GoFilesUnder(false, dir) {
			// nova-bus itself is the PRODUCER. Its own package test holds its
			// stdout to the registry; reading it against itself here would be
			// circular, and it has no reason to drop its own progress.
			if strings.HasPrefix(f.Rel, "cmd/nova-bus/") || f.Rel == progressRegistryFile {
				continue
			}
			reads, discards := novaBusStarts(string(f.Src))
			if discards {
				discarders = append(discarders, f.Rel)
			}
			if !reads {
				continue
			}
			readers = append(readers, f.Rel)
			// The file that STARTS nova-bus need not be the file that
			// classifies its lines -- nova-wake starts it in serve.go and
			// main.go and classifies in internal/wake -- so the check is per
			// PACKAGE: somewhere in this package, the registry is reached.
			if !packageReachesTheRegistry(tree, path.Dir(f.Rel)) {
				missing = append(missing, f.Rel)
			}
		}
	}

	// A walk that found nothing would pass in silence, and the rule would be
	// unheld from the day somebody moved the code.
	if len(readers) == 0 {
		t.Fatal("no file in this tree was found reading nova-bus's output; this test is then holding nothing, and the pattern it looks for has moved")
	}
	if len(discarders) == 0 {
		t.Fatal("no file was found starting nova-bus and discarding its output; the exemption this test grants is then untested, and a reader misread as a discarder would go unheld")
	}
	for _, rel := range missing {
		t.Errorf("%s reads nova-bus's output and nothing in its package reaches internal/bus.IsProgress; a progress line on stderr will be parsed as protocol, which is the 2026-09-18 defect -- drop progress through the registry, or read stdout alone", rel)
	}
}

// novaBusStarts reads a source file for starts of the nova-bus binary and says
// whether any of them READS what it said, and whether any of them DISCARDS it.
// The binary is started by name in this tree, so the name inside an exec call is
// the signal: a file that merely mentions nova-bus in prose does not match.
//
// A start is a discarder when the lines just under it send both streams to
// io.Discard, which is how a caller says "I am sending, not reading". Anything
// else is a read: CombinedOutput, Output, or a buffer on either stream.
func novaBusStarts(text string) (reads, discards bool) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !strings.Contains(line, `"nova-bus"`) {
			continue
		}
		if !strings.Contains(line, "exec.Command") && !strings.Contains(line, "exec.CommandContext") {
			continue
		}
		// The window is the few lines a caller has to say what it does with the
		// two streams, which in this tree is always the next statement.
		window := strings.Join(lines[i:min(i+4, len(lines))], "\n")
		if strings.Count(window, "io.Discard") >= 2 {
			discards = true
			continue
		}
		reads = true
	}
	return reads, discards
}

// packageReachesTheRegistry answers whether any non-test file in a package asks
// internal/bus whether a line is progress, or hands the bytes to the one
// classifier that asks for it. dir is the package's repo-relative, slash
// separated directory, and the files come from the shared tree.
func packageReachesTheRegistry(tree *repoTreeIndex, dir string) bool {
	for _, f := range tree.Files {
		if !f.Go || f.Test || path.Dir(f.Rel) != dir {
			continue
		}
		for _, marker := range registryReaders {
			if strings.Contains(string(f.Src), marker) {
				return true
			}
		}
	}
	return false
}
