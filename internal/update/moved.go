// moved writes the note of what two commits' binaries changed.
package update

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// movedChildTimeout is the default deadline one child of `moved` gets, and
// `--timeout` is how a caller changes it. It is snapshot's thirty seconds, not
// report's five, for the same measured reason: every binary this verb
// reads is one it built a moment ago, so the platform's one-time assessment of
// a never-seen executable is charged to the first exec of every tool at every
// revision. A five-second bound here refused healthy builds and sent the reader
// to repair a revision that was fine.
var movedChildTimeout = 30 * time.Second

// movedBudget bounds the whole run -- two checkouts, two builds and every
// help -- as the same per-child/per-run pair `check`, `report`, `watch` and
// `snapshot` already take.
var movedBudget = 60 * time.Second

// movedVerb is SPEC-VERSION's TOOLS MOVED note: it compares two
// revisions by BUILDING both and reading what each build's own `help` prints,
// never a hand-written list. The hurt it removes is the ADOPT EVERYTHING note,
// which names flags no shipped binary answers: a list
// a person wrote can announce a flag no binary ever answered, and a reader
// cannot tell that from a reading. Here every announced verb and flag was
// parsed off a `<tool> help` this run executed, so a flag on no binary's help
// cannot be announced (SPEC-VERSION rules 2 and 10). A tool that vanishes is
// deleted and one that appears is added; a rename is counted only when a commit
// message or a MOVED file states it and the builds confirm it (rule 2); an
// empty diff is three zeros, exit 0, never a refusal (rule 3).
func movedVerb(c *tool.Call, env Environment) *tool.Out {
	// Read once at entry, so a refusal on the way keeps its own reason: the
	// skeleton fails a --dry-run call whose verb never read it.
	dryRun := c.DryRun()
	started := env.Now()
	from, to, repo, outPath := c.Str("from"), c.Str("to"), c.Str("repo"), c.Str("out")
	timeout, budget := c.Dur("timeout"), c.Dur("budget")
	// One deadline per child and one for the run: every git, every go build
	// and every help hangs off both, through the same bounded process
	// machinery -- internal/bounded's capture -- the rest of this package
	// already runs its children through.
	run, cancelRun := context.WithTimeout(context.Background(), budget)
	defer cancelRun()
	runChild := func(argv []string) ProcessResult {
		ctx, cancel := context.WithTimeout(run, timeout)
		p := env.runProcess(ctx, argv, nil, ChildCap)
		cancel()
		return p
	}
	// Both revisions resolve in --repo alone, before anything is built or
	// written (SPEC-VERSION rule 1: never the cwd, never origin/HEAD). A
	// revision that is not a commit names the git fetch that would bring it;
	// the fetch itself is the caller's, because this verb reaches for no
	// network of its own (rule 12).
	// A --repo that is no checkout is named as such first: a revision cannot be a
	// commit there, and a fetch would not help.
	if p := runChild([]string{"git", "-C", repo, "rev-parse", "--git-dir"}); strings.HasPrefix(p.Reason, "exit ") {
		return tool.Refuse(fmt.Sprintf("--repo %s is not a git checkout (name the checkout holding both revisions)", repo))
	}
	resolve := func(rev string) (string, error) {
		p := runChild([]string{"git", "-C", repo, "rev-parse", "--verify", rev + "^{commit}"})
		if p.Reason != "" {
			// git answered and said no: the revision is not a commit here.
			// Anything else -- git missing, not executable, past the deadline
			// -- is not a fact about the revision, and naming the fetch would
			// send the reader to repair a checkout that is fine.
			if strings.HasPrefix(p.Reason, "exit ") {
				return "", fmt.Errorf("revision %s is not a commit in %s (run git fetch to bring it, or name a revision this checkout holds)", rev, repo)
			}
			return "", fmt.Errorf("cannot run git against %s (%s) (supply a --repo and an environment where git answers)", repo, oneline.Escape(p.Reason))
		}
		return strings.TrimSpace(strings.SplitN(p.Stdout, "\n", 2)[0]), nil
	}
	fromSha, err := resolve(from)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	toSha, err := resolve(to)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	// A rename is stated, never inferred: the commits between the two
	// revisions and a MOVED file at --to are the only sources, and a
	// statement counts only when the builds confirm it.
	stated := map[string]string{}
	readStated := func(text string) {
		for _, line := range strings.Split(text, "\n") {
			f := strings.Fields(strings.ToLower(line))
			if len(f) == 4 && f[0] == "renamed" && f[2] == "to" {
				stated[f[1]] = f[3]
			}
		}
	}
	if p := runChild([]string{"git", "-C", repo, "log", "--format=%B", fromSha + ".." + toSha}); p.Reason != "" {
		return tool.Refuse(fmt.Sprintf("cannot read the commits between %s and %s in %s (%s)", fromSha, toSha, repo, oneline.Escape(p.Reason)))
	} else {
		readStated(p.Stdout)
	}
	if p := runChild([]string{"git", "-C", repo, "show", toSha + ":MOVED"}); p.Reason == "" {
		readStated(p.Stdout)
	}
	// Each revision's inventory comes from its own build: cmd/* at the
	// revision, checked out into a worktree of that revision, built there,
	// and each built binary asked its help. Nothing here reads a list any
	// person wrote.
	stage, err := os.MkdirTemp("", "nova-version-moved-")
	if err != nil {
		return tool.Refuse(fmt.Sprintf("cannot create a staging directory (%s)", oneline.Escape(err.Error())))
	}
	// The staging tree comes down by the names it went up with, through
	// os.Remove alone: the removeall class rule allows no os.RemoveAll of a
	// computed path, and every path below stage is one this function created
	// and knows -- the built binaries, the per-revision build directories,
	// and stage itself, last.
	var staged []string
	defer func() {
		for i := len(staged) - 1; i >= 0; i-- {
			_ = os.Remove(staged[i]) // ignored: staging cleanup
		}
		_ = os.Remove(stage) // ignored: staging cleanup
	}()
	inventory := func(rev string) (movedInv, error) {
		p := runChild([]string{"git", "-C", repo, "ls-tree", "-d", "--name-only", rev + ":cmd"})
		if p.Reason != "" {
			return nil, fmt.Errorf("cannot list cmd/* at %s in %s (%s) (supply a --repo whose %s revision holds a cmd directory)", rev, repo, oneline.Escape(p.Reason), rev)
		}
		var tools []string
		for _, line := range strings.Split(p.Stdout, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				tools = append(tools, line)
			}
		}
		work := filepath.Join(stage, "wt-"+safeRevision(rev))
		if p = runChild([]string{"git", "-C", repo, "worktree", "add", "--detach", work, rev}); p.Reason != "" {
			return nil, fmt.Errorf("cannot check out %s into a worktree (%s) (supply a --repo this revision resolves in)", rev, oneline.Escape(p.Reason))
		}
		// The worktree is git's own tree, so git takes it down; anything it
		// leaves behind stays behind rather than being removed by hand.
		// ignored: git takes its own worktree down (see the comment above); anything left stays in the scratch root
		defer func() { _ = runChild([]string{"git", "-C", repo, "worktree", "remove", "--force", work}) }()
		// The build directory is named for the revision, so the two builds
		// cannot collide and the set each revision produced sits in one
		// place to be asked its help.
		built := filepath.Join(stage, safeRevision(rev))
		if err := os.MkdirAll(built, 0o755); err != nil {
			return nil, fmt.Errorf("cannot create a build directory (%s)", oneline.Escape(err.Error()))
		}
		staged = append(staged, built)
		if p = runChild([]string{"go", "build", "-C", work, "-o", built, "./cmd/..."}); p.Reason != "" {
			return nil, fmt.Errorf("cannot build ./cmd/... at %s (%s) (repair the package at that revision)", rev, oneline.Escape(p.Reason))
		}
		inv := movedInv{}
		for _, cmd := range tools {
			bin := filepath.Join(built, cmd)
			staged = append(staged, bin)
			p := runChild([]string{bin, "help"})
			what := "it printed no help"
			if p.Reason != "" {
				what = p.Reason
				// A child killed because the RUN ran out reports `timeout`
				// like any other, since all it can see is its own cancelled
				// context; the run's context is the one that knows which
				// bound was spent, so a spent budget is never reported as a
				// slow binary.
				if run.Err() != nil {
					what = "the run's " + budget.String() + " budget was spent before " + cmd + " was read"
				} else if p.Reason == "timeout" {
					what = "timeout after " + timeout.String()
				}
			}
			if p.Reason != "" || strings.TrimSpace(p.Stdout) == "" {
				// SPEC-VERSION rule 4: a cmd/* that builds but answers no
				// help names the cmd, the revision and the build to repair
				// there.
				return nil, fmt.Errorf("cannot read %s help at %s (%s) (repair the build there: go build ./cmd/%s, or raise --timeout)", cmd, rev, what, cmd)
			}
			inv[cmd] = parseMovedHelp(cmd, p.Stdout)
		}
		return inv, nil
	}
	before, err := inventory(fromSha)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	after := before
	if toSha != fromSha {
		if after, err = inventory(toSha); err != nil {
			return tool.Refuse(err.Error())
		}
	}
	entries, counts := diffMoved(before, after, stated)
	var note strings.Builder
	fmt.Fprintf(&note, "MOVED from=%s to=%s at=%s\n", field(fromSha), field(toSha), field(started.UTC().Format(time.RFC3339)))
	for _, e := range entries {
		fmt.Fprintln(&note, e)
	}
	// The one line, every field named (SPEC-VERSION rule 3): added, deleted
	// and renamed count tools, and verbs counts the (tool, verb) pairs the
	// --to build answers -- the size of the surface the note describes.
	o := tool.Done().Fact("from", fromSha).Fact("to", toSha).Fact("added", counts.added).Fact("deleted", counts.deleted).
		Fact("renamed", counts.renamed).Fact("verbs", counts.verbs).Fact("file", outPath)
	// --dry-run is the same builds and reads with the note printed, not written.
	if dryRun { // the skeleton adds dry_run=true
		o.Payload = note.String()
		return o
	}
	if err := os.WriteFile(outPath, []byte(note.String()), 0o644); err != nil {
		return tool.Refuse(fmt.Sprintf("cannot write --out %s (supply a writable --out path)", outPath))
	}
	return o
}

// movedInv is one revision's inventory as its own builds reported it: every
// cmd/* tool the revision builds, and for each, the verbs and flags its `help`
// printed.
type movedInv map[string]map[string]map[string]bool

// parseMovedHelp reads one built tool's `help` into verbs and flags. Only lines
// that begin with the tool's own name count, after any indent (a tool on
// internal/tool indents its usage lines): a help that prints another tool's
// usage line (SPEC-VERSION's block names nova-update's in nova-version's help)
// cannot add that tool to THIS revision's inventory, and a line that is not a
// usage line -- the defaults, the notes, the examples -- contributes nothing.
// This function is the whole of "never a hand-written list": whatever
// these lines do not print, the note cannot announce.
func parseMovedHelp(tool, help string) map[string]map[string]bool {
	verbs := map[string]map[string]bool{}
	for _, line := range strings.Split(help, "\n") {
		if line = strings.TrimSpace(line); !strings.HasPrefix(line, tool+" ") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[1], "-") {
			continue
		}
		if verbs[f[1]] == nil {
			verbs[f[1]] = map[string]bool{}
		}
		for _, tok := range f[2:] {
			tok = strings.TrimLeft(tok, "[(")
			if !strings.HasPrefix(tok, "--") {
				continue
			}
			tok = strings.TrimRight(tok, "),.;:]")
			if pre, _, ok := strings.Cut(tok, "="); ok {
				tok = pre
			}
			verbs[f[1]][tok] = true
		}
	}
	return verbs
}

// diffMoved compares the two inventories and returns the note's entry lines
// and the counts the MOVED OK line prints. A tool that vanishes is deleted and
// one that appears is added; a rename is counted only when it was stated (in a
// commit message or a MOVED file) AND the inventories confirm it -- the original
// name built only at --from, the new name only at --to -- so help text alone,
// however identical, never makes a rename (SPEC-VERSION rule 2). A confirmed
// rename consumes its pair: the statement, not a guess, is what moved the tool.
func diffMoved(before, after movedInv, stated map[string]string) (entries []string, counts struct{ added, deleted, renamed, verbs int }) {
	consumed := map[string]bool{}
	var renames [][2]string
	for old, name := range stated {
		_, oldBuiltBefore := before[old]
		_, oldBuiltAfter := after[old]
		_, newBuiltBefore := before[name]
		_, newBuiltAfter := after[name]
		if oldBuiltBefore && !oldBuiltAfter && newBuiltAfter && !newBuiltBefore {
			renames = append(renames, [2]string{old, name})
			consumed[old], consumed[name] = true, true
		}
	}
	sort.Slice(renames, func(i, j int) bool { return renames[i][0] < renames[j][0] })
	for _, r := range renames {
		entries = append(entries, "renamed="+r[0]+"->"+r[1])
		counts.renamed++
	}
	afterTools := slices.Sorted(maps.Keys(after))
	beforeTools := slices.Sorted(maps.Keys(before))
	for _, tool := range afterTools {
		counts.verbs += len(after[tool])
		if _, ok := before[tool]; ok || consumed[tool] {
			continue
		}
		entries = append(entries, "added="+tool)
		counts.added++
	}
	for _, tool := range beforeTools {
		if _, ok := after[tool]; ok || consumed[tool] {
			continue
		}
		entries = append(entries, "deleted="+tool)
		counts.deleted++
	}
	// Verb and flag entries for the tools the two revisions share, and for
	// each confirmed rename, the pair the statement joined.
	pairs := map[string]string{}
	for _, tool := range afterTools {
		if _, ok := before[tool]; ok {
			pairs[tool] = tool
		}
	}
	for _, r := range renames {
		pairs[r[1]] = r[0]
	}
	for _, tool := range afterTools {
		old, ok := pairs[tool]
		if !ok {
			continue
		}
		beforeVerbs, afterVerbs := before[old], after[tool]
		names := map[string]bool{}
		for v := range beforeVerbs {
			names[v] = true
		}
		for v := range afterVerbs {
			names[v] = true
		}
		for _, v := range slices.Sorted(maps.Keys(names)) {
			_, hadBefore := beforeVerbs[v]
			_, hasAfter := afterVerbs[v]
			switch {
			case hasAfter && !hadBefore:
				entries = append(entries, "added="+v+" tool="+tool)
			case hadBefore && !hasAfter:
				entries = append(entries, "deleted="+v+" tool="+tool)
			}
			if !hadBefore || !hasAfter {
				continue
			}
			flags := map[string]bool{}
			for f := range beforeVerbs[v] {
				flags[f] = true
			}
			for f := range afterVerbs[v] {
				flags[f] = true
			}
			for _, f := range slices.Sorted(maps.Keys(flags)) {
				_, hadFlagBefore := beforeVerbs[v][f]
				_, hasFlagAfter := afterVerbs[v][f]
				switch {
				case hasFlagAfter && !hadFlagBefore:
					entries = append(entries, "added="+f+" tool="+tool+" verb="+v)
				case hadFlagBefore && !hasFlagAfter:
					entries = append(entries, "deleted="+f+" tool="+tool+" verb="+v)
				}
			}
		}
	}
	return entries, counts
}

// safeRevision makes a revision usable as one directory name under the staging
// root: the hex a real `git rev-parse` answers needs no change, and anything
// else a caller hands the flag is folded to the characters a single path
// segment can hold. "." and ".." are refused as names outright -- a staging
// subdir that climbed out of the staging root is how a temp dir becomes a
// deletion.
func safeRevision(rev string) string {
	var b strings.Builder
	for _, r := range rev {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if s := b.String(); s != "" && s != "." && s != ".." {
		return s
	}
	return "revision"
}
