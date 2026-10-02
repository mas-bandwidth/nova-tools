package cardcontract

import (
	"fmt"
	"path/filepath"
	"strings"
)

// shq is a value quoted for sh: inside single quotes, each single quote closed, escaped
// and reopened, so any byte string is the value it was.
func shq(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }

// prelude is every shim's head: what it is, the frame's values, and the two helpers.
// nova_key folds a repository's spellings (https, ssh, scp form, .git or not, a user, a
// trailing slash, the scheme's default port, case) into one, so a clone of the card's
// repository is known in any of them.
func prelude(name, family string, f Frame, s Staged) string {
	base := f.ReviewBase
	if base == "" {
		base = f.BaseRef
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#!/bin/sh\n# nova-swarm %s shim, profile %s (docs/SPEC-CARD-CONTRACT.md): the card's frame,\n", name, family)
	b.WriteString("# met through the commands the child knows; nothing reaches a forge from inside the wall.\n")
	for _, kv := range [][2]string{
		{"NOVA_GIT", s.Git}, {"NOVA_JOB", s.Job}, {"NOVA_STAGED", s.Repo}, {"NOVA_HEAD", s.Head},
		{"NOVA_REPO", f.Repo}, {"NOVA_BRANCH", f.Branch}, {"NOVA_BASE", base}, {"NOVA_START", s.Start}, {"NOVA_KIND", f.Kind}, {"NOVA_CARD", f.Card},
	} {
		fmt.Fprintf(&b, "%s=%s\n", kv[0], shq(kv[1]))
	}
	b.WriteString(`nova_q() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }
nova_key() { printf '%s' "$1" | sed -e 's#/*$##' -e 's#\.git$##' -e 's#^\(ssh://[^/]*\):22/#\1/#' -e 's#^\(https://[^/]*\):443/#\1/#' -e 's#^\(http://[^/]*\):80/#\1/#' -e 's#^\(git://[^/]*\):9418/#\1/#' -e 's#^[A-Za-z][A-Za-z0-9+.-]*://##' -e 's#^[^@/]*@##' -e 's#^\([^/:]*\):#\1/#' | tr 'A-Z' 'a-z'; }
`)
	return b.String()
}

// gitHead finds git's subcommand past its global options, keeping them for nova_git.
const gitHead = `nova_globals=""; nova_sub=""; nova_skip=""; nova_n=0
for nova_a in "$@"; do
	nova_n=$((nova_n + 1))
	if [ -n "$nova_skip" ]; then nova_globals="$nova_globals $(nova_q "$nova_a")"; nova_skip=""; continue; fi
	case "$nova_a" in
	-C|-c|--git-dir|--work-tree|--namespace|--super-prefix) nova_globals="$nova_globals $(nova_q "$nova_a")"; nova_skip=1 ;;
	-*) nova_globals="$nova_globals $(nova_q "$nova_a")" ;;
	*) nova_sub="$nova_a"; break ;;
	esac
done
nova_git() { eval "\"\$NOVA_GIT\" $nova_globals \"\$@\""; }
case "$nova_sub" in
`

// gitPush is the contract's push: a long flag that is a prefix of a refused long flag is refused as
// typed, since git accepts any unambiguous prefix (docs/SPEC-CARD-CONTRACT.md, the push); the head the refspec names (else HEAD) and the branch it
// comes from, recorded in <job>/.sprint/pushed.tsv, answered as the push the member makes
// at the finish: that branch to the card's branch on origin.
const gitPush = `push)
	shift "$nova_n"
	nova_pos=""; nova_skip=""
	for nova_a in "$@"; do
		if [ -n "$nova_skip" ]; then nova_skip=""; continue; fi
		case "$nova_a" in
		-d|--delete|--mirror|--prune|--all|--branches|--tags|-o|--push-option|--push-option=*|-o?*)
			echo "error: REFUSED git push $nova_a: a card's push carries one commit to the card's branch, which the sprint pushes when the card finishes; it deletes, prunes, mirrors and passes options for nothing" >&2; exit 1 ;;
		--repo|--receive-pack|--exec|--signed) nova_skip=1 ;;
		--) ;;
		--?*)
			nova_opt="${nova_a#--}"; nova_opt="${nova_opt%%=*}"
			for nova_r in delete mirror prune all branches tags push-option; do
				case "$nova_r" in "$nova_opt"*)
					echo "error: REFUSED git push $nova_a: a card's push carries one commit to the card's branch, which the sprint pushes when the card finishes; it deletes, prunes, mirrors and passes options for nothing" >&2; exit 1 ;;
				esac
			done ;;
		-*) ;;
		*) nova_pos="$nova_pos $(nova_q "$nova_a")" ;;
		esac
	done
	eval "set -- $nova_pos"
	nova_src=HEAD; nova_dst=""
	if [ $# -ge 2 ]; then
		nova_spec="${2#+}"; nova_src="${nova_spec%%:*}"
		case "$nova_spec" in *:*) nova_dst="${nova_spec#*:}" ;; esac
	fi
	if [ -z "$nova_src" ]; then echo "error: REFUSED: a card deletes no branch; the sprint owns its branches" >&2; exit 1; fi
	nova_head=$(nova_git rev-parse --verify -q "$nova_src^{commit}") || { echo "error: src refspec $nova_src does not match any" >&2; exit 1; }
	nova_branch=$(nova_git symbolic-ref -q --short HEAD 2>/dev/null)
	[ "$nova_src" = HEAD ] || nova_branch="$nova_src"
	[ -z "$nova_dst" ] || nova_branch="${nova_dst#refs/heads/}"
	[ -n "$nova_branch" ] || nova_branch=HEAD
	nova_top=$(nova_git rev-parse --show-toplevel 2>/dev/null)
	mkdir -p "$NOVA_JOB/.sprint" && printf '%s\t%s\t%s\n' "$nova_branch" "$nova_head" "$nova_top" >> "$NOVA_JOB/.sprint/pushed.tsv" || exit 1
	printf 'To %s\n * [new branch]      %s -> %s (pushed by the sprint when this card finishes)\n' "$NOVA_REPO" "$nova_branch" "$NOVA_BRANCH" >&2
	exit 0 ;;
`

// gitClone is the claude profile's clone: the card's repository, in any spelling, becomes a
// link to the staged checkout (kept out of the checkout's status when it lands inside it);
// any other repository is refused.
const gitClone = `clone)
	shift "$nova_n"
	nova_url=""; nova_dir=""; nova_skip=""
	for nova_a in "$@"; do
		if [ -n "$nova_skip" ]; then nova_skip=""; continue; fi
		case "$nova_a" in
		-b|--branch|--depth|-o|--origin|--reference|--reference-if-able|-c|--config|--template|--separate-git-dir|-u|--upload-pack|-j|--jobs|--shallow-since|--shallow-exclude|--filter|--server-option|--bundle-uri) nova_skip=1 ;;
		-*) ;;
		*) if [ -z "$nova_url" ]; then nova_url="$nova_a"; elif [ -z "$nova_dir" ]; then nova_dir="$nova_a"; fi ;;
		esac
	done
	if [ -z "$nova_url" ] || [ "$(nova_key "$nova_url")" != "$(nova_key "$NOVA_REPO")" ]; then
		echo "fatal: REFUSED clone of $nova_url: this card works in $NOVA_REPO, already staged at $NOVA_STAGED; no other repository is cloned in a card" >&2
		exit 128
	fi
	[ -n "$nova_dir" ] || nova_dir=$(basename "${nova_url%/}" .git)
	if [ -e "$nova_dir" ] || [ -L "$nova_dir" ]; then
		if [ -d "$nova_dir" ] && [ ! -L "$nova_dir" ] && [ -z "$(ls -A "$nova_dir" 2>/dev/null)" ]; then rmdir "$nova_dir" || exit 128
		else echo "fatal: destination path '$nova_dir' already exists and is not an empty directory." >&2; exit 128; fi
	fi
	ln -s "$NOVA_STAGED" "$nova_dir" || exit 128
	nova_parent=$(cd "$(dirname "$nova_dir")" && pwd -P)
	nova_root=$(cd "$NOVA_STAGED" && pwd -P)
	case "$nova_parent/" in
	"$nova_root/"*) mkdir -p "$nova_root/.git/info" && printf '/%s/%s\n' "${nova_parent#"$nova_root"}" "$(basename "$nova_dir")" | sed 's#^//*#/#' >> "$nova_root/.git/info/exclude" ;;
	esac
	echo "Cloning into '$nova_dir'..." >&2
	echo "nova-swarm: '$nova_dir' is the staged checkout of $NOVA_REPO at $NOVA_HEAD on branch $NOVA_BRANCH; work and commit there" >&2
	exit 0 ;;
`

// gitTail hands every other git command to the real git as it was typed.
const gitTail = `esac
exec "$NOVA_GIT" "$@"
`

// ContractShim is the git every profile may reuse: push recorded, everything else real.
func ContractShim(family string, f Frame, s Staged) Shim {
	return Shim{Name: "git", Script: prelude("git", family, f, s) + gitHead + gitPush + gitTail}
}

// refusingGh is a gh that refuses every command with one line.
func refusingGh(family string, f Frame, s Staged) Shim {
	return Shim{Name: "gh", Script: prelude("gh", family, f, s) +
		`echo "gh: REFUSED $1 $2: no GitHub CLI in a card; the sprint pushes and opens what the card finishes with" >&2
exit 2
`}
}

// ghClaude is the claude profile's gh: pr create finishes a work card (a title starting
// `nothing:` says there is nothing to do), pr review finishes a read, pr diff/view/checks
// answer from the staged checkout, and everything else is refused: the wall holds no forge
// credential and no network. The finish is recorded in <job>/.sprint/finish.md, which the
// child is never told to write.
const ghClaude = `nova_here() { if "$NOVA_GIT" rev-parse --git-dir >/dev/null 2>&1; then "$NOVA_GIT" "$@"; else "$NOVA_GIT" -C "$NOVA_STAGED" "$@"; fi; }
nova_refuse() { echo "gh: REFUSED $1: the wall holds no GitHub credential and no network; this card's change is in the staged checkout (gh pr diff, gh pr view) and the sprint opens its pull request when it finishes" >&2; exit 2; }
nova_need() { [ "$1" -ge 2 ] || { echo "gh: flag needs an argument: $2" >&2; exit 1; }; }
nova_base() { if [ -n "$NOVA_START" ]; then echo "$NOVA_START"; elif "$NOVA_GIT" -C "$NOVA_STAGED" rev-parse -q --verify "origin/$NOVA_BASE^{commit}" >/dev/null 2>&1; then echo "origin/$NOVA_BASE"; else echo "$NOVA_BASE"; fi; }
nova_result() {
	nova_h=$(nova_here rev-parse HEAD 2>/dev/null)
	nova_br=$(nova_here symbolic-ref -q --short HEAD 2>/dev/null); [ -n "$nova_br" ] || nova_br="$NOVA_BRANCH"
	nova_rep=$(printf '%s\n' "$2" | awk 'NF { print; exit }')
	[ -n "$nova_rep" ] || nova_rep="$1"
	{
		printf 'head: %s\nbranch: %s\nverdict: %s\ngate: -\noutput: -\nreport: %s\n' "${nova_h:--}" "$nova_br" "$1" "$nova_rep"
		[ -z "$4" ] || printf 'title: %s\n' "$4"
		printf '\n## Body\n\n%s\n' "$3"
	} > "$NOVA_JOB/.sprint/finish.md" || exit 1
}
mkdir -p "$NOVA_JOB/.sprint" || exit 1
nova_body=""
case "$1 $2" in
"pr create")
	if [ "$NOVA_KIND" = read ]; then echo "gh: REFUSED pr create: this card is a read; it ends with gh pr review" >&2; exit 2; fi
	nova_title=""; nova_fill=""
	shift 2
	while [ $# -gt 0 ]; do
		case "$1" in
		-t|--title) nova_need $# "$1"; nova_title="$2"; shift 2 ;;
		--title=*) nova_title="${1#--title=}"; shift ;;
		-b|--body) nova_need $# "$1"; nova_body="$2"; shift 2 ;;
		--body=*) nova_body="${1#--body=}"; shift ;;
		-F|--body-file) nova_need $# "$1"; if [ "$2" = - ]; then nova_body=$(cat); else nova_body=$(cat "$2") || exit 1; fi; shift 2 ;;
		--body-file=*) nova_f="${1#--body-file=}"; if [ "$nova_f" = - ]; then nova_body=$(cat); else nova_body=$(cat "$nova_f") || exit 1; fi; shift ;;
		-f|--fill|--fill-first|--fill-verbose) nova_fill=1; shift ;;
		-B|--base|-H|--head|-R|--repo|-a|--assignee|-l|--label|-m|--milestone|-p|--project|-r|--reviewer|-T|--template) nova_need $# "$1"; shift 2 ;;
		*) shift ;;
		esac
	done
	if [ -n "$nova_fill" ]; then
		[ -n "$nova_title" ] || nova_title=$(nova_here log -1 --format=%s)
		[ -n "$nova_body" ] || nova_body=$(nova_here log -1 --format=%b)
	fi
	if [ -z "$nova_title" ]; then echo "gh: REFUSED pr create with no --title (or --fill): the title is what the sprint opens the pull request with" >&2; exit 1; fi
	nova_v=ok
	case "$nova_title" in [Nn]othing:*) nova_v=nothing ;; esac
	nova_result "$nova_v" "$nova_title" "$nova_body" "$nova_title"
	echo "pull request recorded for $nova_br: the sprint pushes it to $NOVA_BRANCH and opens it when this card finishes"
	exit 0 ;;
"pr review")
	if [ "$NOVA_KIND" != read ]; then echo "gh: REFUSED pr review: this card is work; it ends with gh pr create" >&2; exit 2; fi
	nova_v=""
	shift 2
	while [ $# -gt 0 ]; do
		case "$1" in
		-a|--approve) nova_v=ok; shift ;;
		-r|--request-changes) nova_v=broken; shift ;;
		-c|--comment) nova_v=comment; shift ;;
		-b|--body) nova_need $# "$1"; nova_body="$2"; shift 2 ;;
		--body=*) nova_body="${1#--body=}"; shift ;;
		-F|--body-file) nova_need $# "$1"; if [ "$2" = - ]; then nova_body=$(cat); else nova_body=$(cat "$2") || exit 1; fi; shift 2 ;;
		*) shift ;;
		esac
	done
	if [ "$nova_v" != ok ] && [ "$nova_v" != broken ]; then echo "gh: REFUSED pr review without --approve or --request-changes: the review is this read's verdict" >&2; exit 1; fi
	if [ "$nova_v" = broken ] && [ -z "$nova_body" ]; then echo "gh: REFUSED pr review --request-changes with no --body: the findings are what the work is fixed by" >&2; exit 1; fi
	nova_result "$nova_v" "$nova_body" "$nova_body" ""
	echo "- Reviewed pull request: the sprint records the verdict when this read finishes"
	exit 0 ;;
"pr diff")
	shift 2
	case " $* " in *" --name-only "*) exec "$NOVA_GIT" -C "$NOVA_STAGED" diff --name-only "$(nova_base)...HEAD" ;; esac
	exec "$NOVA_GIT" -C "$NOVA_STAGED" diff "$(nova_base)...HEAD" ;;
"pr view")
	printf 'title:\t%s\nstate:\tOPEN (the sprint opens it when this card finishes)\nbase:\t%s\nhead:\t%s\n--\n' "$NOVA_CARD" "$NOVA_BASE" "$NOVA_BRANCH"
	exec "$NOVA_GIT" -C "$NOVA_STAGED" log --oneline "$(nova_base)..HEAD" ;;
"pr checks")
	echo "no checks reported: the sprint runs the gate; run the card's gate yourself"
	exit 0 ;;
esac
nova_refuse "$1 $2"
`

// claude is the profile of the claude family: the worktree shape, where git push works,
// gh pr create is the finish and gh pr review is the read.
type claude struct{}

func (claude) Family() string { return "claude" }

func (claude) Shims(f Frame, s Staged) []Shim {
	return []Shim{
		{Name: "git", Script: prelude("git", "claude", f, s) + gitHead + gitPush + gitClone + gitTail},
		{Name: "gh", Script: prelude("gh", "claude", f, s) + ghClaude},
	}
}

func (claude) JobText(f Frame, s Staged) string {
	var b strings.Builder
	if f.Kind == "read" {
		fmt.Fprintf(&b, "# JOB: read %s, attempt %d\n\n", f.Card, f.Attempt)
		fmt.Fprintf(&b, "You are in a checkout of %s on branch %s at %s: the change under review, against %s. The checkout is %s.\n\n", f.Repo, f.Branch, s.Head, orDash(f.ReviewBase), s.Repo)
		fmt.Fprintf(&b, "Review the change on this branch against %s as you would a pull request: `gh pr diff`, `gh pr view` and `gh pr checks` show it. Run the card's gate. Change nothing and commit nothing.\n\n", orDash(f.ReviewBase))
		writeReadDiff(&b, f, s)
		b.WriteString("Approve or request changes with gh pr review; that ends the read:\n\n")
		b.WriteString("    gh pr review --approve --body \"<what you checked>\"\n")
		b.WriteString("    gh pr review --request-changes --body \"<findings, each with file:line>\"\n\n")
	} else {
		fmt.Fprintf(&b, "# JOB: %s, attempt %d\n\n", f.Card, f.Attempt)
		fmt.Fprintf(&b, "You are in a checkout of %s on branch %s at %s, from %s. The checkout is %s; work there. Commit as usual.\n\n", f.Repo, f.Branch, s.Head, orDash(f.BaseRef), s.Repo)
		b.WriteString("`git push` and `gh pr create` work as you expect: the sprint does them for you when this card finishes, from outside this machine's wall, with its own credential. `git clone` of this repository gives you this same checkout; nothing else is cloned, and nothing reaches GitHub from here.\n\n")
		b.WriteString("When the work is done: commit, `git push`, then `gh pr create --title \"<title>\" --body \"<body>\"`. That ends the card; there is nothing else to write. The title is the card's report; put the gate command and its output in the body, which the readers see.\n")
		b.WriteString("Every card ends with a commit. If there is nothing to do, end with `gh pr create --title \"nothing: <why>\"`. If the work cannot be done, write " + s.Job + "/RESULT.md with `verdict: not-done` in this shape:\n\n")
		b.WriteString(ShapeText("work") + "\n")
	}
	writeCommon(&b, f, s)
	return b.String()
}

// plain is the contract alone, for a family with no profile of its own: push recorded,
// gh refused, the result written by the child in the shape.
type plain struct{ family string }

func (p plain) Family() string { return p.family }

func (p plain) Shims(f Frame, s Staged) []Shim {
	return []Shim{ContractShim(p.family, f, s), refusingGh(p.family, f, s)}
}

func (plain) JobText(f Frame, s Staged) string {
	var b strings.Builder
	if f.Kind == "read" {
		fmt.Fprintf(&b, "# JOB: read %s, attempt %d\n\n", f.Card, f.Attempt)
		review := fmt.Sprintf("`git diff %s...HEAD`", orDash(f.ReviewBase))
		if s.Start != "" {
			review = "the commands below"
		}
		fmt.Fprintf(&b, "The checkout %s holds %s on branch %s at %s: the change under review, against %s. Review it (%s), run the card's gate, change nothing and commit nothing.\n\n", s.Repo, f.Repo, f.Branch, s.Head, orDash(f.ReviewBase), review)
		writeReadDiff(&b, f, s)
		b.WriteString("End by writing " + s.Job + "/RESULT.md in this shape (verdict ok, or broken with your findings):\n\n")
		b.WriteString(ShapeText("read") + "\n")
	} else {
		fmt.Fprintf(&b, "# JOB: %s, attempt %d\n\n", f.Card, f.Attempt)
		fmt.Fprintf(&b, "The checkout %s holds %s on branch %s at %s, from %s. Work there and commit on that branch. Do not clone: the repository is already here. `git push` is recorded and done for you when the card finishes; there is no GitHub CLI.\n\n", s.Repo, f.Repo, f.Branch, s.Head, orDash(f.BaseRef))
		b.WriteString("End by writing " + s.Job + "/RESULT.md in this shape (verdict ok when the work is committed, not-done when it cannot be, nothing with the reason as the report when there is nothing to do: every card ends with a commit):\n\n")
		b.WriteString(ShapeText("work") + "\n")
	}
	writeCommon(&b, f, s)
	return b.String()
}

// writeReadDiff is what a read's JOB.md says the work changed (docs/SPEC-CARD-CONTRACT.md,
// JOB.md): the commit the work started from and the one command that shows its change, and
// that the base branch is not what to compare against. On a 1000-card load test (2026-10-01)
// a reader ran `git diff origin/dev` while cards landed on dev every few seconds, saw every
// file landed since as a deletion, and sent a correct work card back. Nothing when the
// start is unknown.
func writeReadDiff(b *strings.Builder, f Frame, s Staged) {
	if s.Start == "" {
		return
	}
	base := orDash(f.ReviewBase)
	fmt.Fprintf(b, "The work's change is exactly %s..HEAD: %s is the commit the work started from (the merge base of this head and %s when this checkout was staged). See it with:\n\n", s.Start, s.Start, base)
	fmt.Fprintf(b, "    git diff %s..HEAD\n    git diff --stat %s..HEAD\n\n", s.Start, s.Start)
	fmt.Fprintf(b, "%s may have moved since the work began (other cards land on it); it is not what to compare against: a diff against %s shows every change landed since as a deletion.\n\n", base, base)
}

// writeCommon is what every JOB.md ends with: the files staged for the child, the test
// environment, the tier, and what the attempt before left. The sprint's rules are the
// brief's own RULES paragraph (the add lint holds it), never repeated here.
func writeCommon(b *strings.Builder, f Frame, s Staged) {
	if len(f.Stage) > 0 {
		fmt.Fprintf(b, "Staged for you in %s: %s.\n", filepath.Join(s.Job, RecipesName), strings.Join(f.Stage, ", "))
	}
	fmt.Fprintf(b, "Tests: export GOCACHE=%s/gocache; run every go command as `nice -n 19`, every go test with -count=1 and -timeout 600s.\n", s.Job)
	if f.Tier != "" {
		fmt.Fprintf(b, "Tier: %s.\n", f.Tier)
	}
	if f.Attempt > 1 {
		fmt.Fprintf(b, "\nAttempt %d of this card.", f.Attempt)
		if f.PrevHead != "" {
			fmt.Fprintf(b, " This checkout continues attempt %d: its head, %s, is the last pushed by any attempt before this one, and the checkout starts from it.", f.PrevFrom, f.PrevHead)
		}
		b.WriteString("\n")
		writeWhy(b, f)
	}
}

// writeWhy is what a rework's JOB.md says right after the attempt line, so the child learns
// why the attempt exists and what to do first (docs/SPEC-CARD-CONTRACT.md, JOB.md): how the
// attempt before ended, what a reader found, what the coordinator asks; a line whose value is
// empty is left out, and the coordinator's line too when its words are the finding's or
// already in how the attempt ended (a rework with no --fix takes the finding as its fix).
func writeWhy(b *strings.Builder, f Frame) {
	why, finding, fix := strings.TrimSpace(f.Why), strings.TrimSpace(f.Finding), strings.TrimSpace(f.Fix)
	if fix == finding || fix != "" && strings.Contains(why, fix) {
		fix = ""
	}
	n := 0
	for _, l := range []struct{ label, text string }{{"This attempt exists because: ", why}, {"A reader found: ", finding}, {"The coordinator asks: ", fix}} {
		if l.text != "" {
			fmt.Fprintf(b, "%s%s\n", l.label, l.text)
			n++
		}
	}
	if n > 0 {
		b.WriteString("Do that first; a finish with no new commit is refused.\n")
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
