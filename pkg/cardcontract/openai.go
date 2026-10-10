package cardcontract

import (
	"fmt"
	"strings"
)

// openai uses the staged repository and its linked worktrees. Section 5 of
// docs/SPEC-CARD-CONTRACT.md keeps pushes and pull-request finishes inside the card frame.
type openai struct{}

func (openai) Family() string { return "openai" }

// Shims keeps the section 5 push and finish contract through shared recorders.
func (openai) Shims(f Frame, s Staged) []Shim {
	return []Shim{
		{Name: "git", Script: prelude("git", "openai", f, s) + openaiGitGuard + gitHead + gitPush + gitTail},
		{Name: "gh", Script: prelude("gh", "openai", f, s) + openaiGhGuard + ghClaude},
	}
}

// openaiGitGuard limits push to forms the shared recorder represents faithfully.
// All other supported Git commands still use the real Git (contract section 5).
const openaiGitGuard = `nova_openai_git_refuse() {
	echo "git: REFUSED push: use git push [-u] origin [HEAD|branch] from the card checkout or its linked worktree; the sprint owns deletion and force" >&2
	exit 2
}
nova_openai_push_args() {
	nova_remote=""; nova_ref=""; nova_up=""
	while [ $# -gt 0 ]; do
		case "$1" in
		-u|--set-upstream) [ -z "$nova_up" ] || nova_openai_git_refuse; nova_up=1 ;;
		-*) nova_openai_git_refuse ;;
		*) if [ -z "$nova_remote" ]; then nova_remote="$1"
		   elif [ -z "$nova_ref" ]; then nova_ref="$1"
		   else nova_openai_git_refuse; fi ;;
		esac
		shift
	done
	[ -z "$nova_remote" ] || [ "$nova_remote" = origin ] || nova_openai_git_refuse
	[ -z "$nova_up" ] || [ "$nova_remote" = origin ] || nova_openai_git_refuse
	case "$nova_ref" in
	*[!A-Za-z0-9_./-]*|.*|*..*|*/|*.) nova_openai_git_refuse ;;
	esac
}
nova_openai_git_guard() {
	while [ $# -gt 0 ]; do
		case "$1" in
		-C|-c|--git-dir|--work-tree|--namespace|--super-prefix)
			[ $# -ge 2 ] || nova_openai_git_refuse; shift 2 ;;
		-C*|-c*|--git-dir=*|--work-tree=*|--namespace=*|--super-prefix=*|--no-pager|--no-optional-locks|--version|--help|-h|-p|-P|--paginate|--no-replace-objects|--literal-pathspecs|--glob-pathspecs|--noglob-pathspecs|--icase-pathspecs) shift ;;
		-*) nova_openai_git_refuse ;;
		push) shift; nova_openai_push_args "$@"; return ;;
		*) return ;;
		esac
	done
}
nova_openai_git_guard "$@"
`

// openaiGhGuard accepts only the pull-request forms the shared gh shim records.
// Unsupported targets and options fail before ghClaude can ignore them (section 5).
const openaiGhGuard = `nova_openai_gh_refuse() {
	echo "gh: REFUSED: use this card's gh pr create --title TITLE --body-file FILE, or gh pr review --approve|--request-changes --body-file FILE; other targets and options are unsupported" >&2
	exit 2
}
nova_openai_gh_need() { [ "$1" -ge 2 ] || nova_openai_gh_refuse; }
nova_openai_gh_guard() {
	[ "$1" = pr ] || nova_openai_gh_refuse
	case "$2" in
	create)
		[ "$NOVA_KIND" = work ] || nova_openai_gh_refuse
		shift 2; nova_title=""; nova_file=""
		while [ $# -gt 0 ]; do
			case "$1" in
			-t|--title) nova_openai_gh_need $#; [ -z "$nova_title" ] && [ -n "$2" ] || nova_openai_gh_refuse; nova_title="$2"; shift 2 ;;
			--title=*) [ -z "$nova_title" ] || nova_openai_gh_refuse; nova_title="${1#--title=}"; [ -n "$nova_title" ] || nova_openai_gh_refuse; shift ;;
			-F|--body-file) nova_openai_gh_need $#; [ -z "$nova_file" ] && [ -n "$2" ] || nova_openai_gh_refuse; nova_file="$2"; shift 2 ;;
			--body-file=*) [ -z "$nova_file" ] || nova_openai_gh_refuse; nova_file="${1#--body-file=}"; [ -n "$nova_file" ] || nova_openai_gh_refuse; shift ;;
			*) nova_openai_gh_refuse ;;
			esac
		done
		[ -n "$nova_title" ] && [ -n "$nova_file" ] || nova_openai_gh_refuse ;;
	review)
		[ "$NOVA_KIND" = read ] || nova_openai_gh_refuse
		shift 2; nova_verdict=""; nova_file=""
		while [ $# -gt 0 ]; do
			case "$1" in
			-a|--approve) [ -z "$nova_verdict" ] || nova_openai_gh_refuse; nova_verdict=ok; shift ;;
			-r|--request-changes) [ -z "$nova_verdict" ] || nova_openai_gh_refuse; nova_verdict=broken; shift ;;
			-F|--body-file) nova_openai_gh_need $#; [ -z "$nova_file" ] && [ -n "$2" ] || nova_openai_gh_refuse; nova_file="$2"; shift 2 ;;
			*) nova_openai_gh_refuse ;;
			esac
		done
		[ -n "$nova_verdict" ] && [ -n "$nova_file" ] || nova_openai_gh_refuse ;;
	diff)
		[ "$NOVA_KIND" = read ] || nova_openai_gh_refuse
		shift 2; [ $# -eq 0 ] || { [ $# -eq 1 ] && [ "$1" = --name-only ]; } || nova_openai_gh_refuse ;;
	view|checks)
		[ "$NOVA_KIND" = read ] && [ $# -eq 2 ] || nova_openai_gh_refuse ;;
	*) nova_openai_gh_refuse ;;
	esac
}
nova_openai_gh_guard "$@"
`

// JobText gives a work or read card its section 2 frame and section 5 finish commands.
func (openai) JobText(f Frame, s Staged) string {
	var b strings.Builder
	if f.Kind == "read" {
		fmt.Fprintf(&b, "%s %s, attempt %d\n\n", ReadTitle, f.Card, f.Attempt)
		fmt.Fprintf(&b, "The staged checkout %s holds %s on branch %s at %s, for review against %s.\n\n", s.Repo, f.Repo, f.Branch, s.Head, orDash(f.ReviewBase))
		b.WriteString("Review the staged change with `gh pr diff`, `gh pr view` and `gh pr checks`, and run " + gateWords(s) + ". These commands read the staged checkout, not a live forge. Change nothing and commit nothing.\n\n")
		writeReadDiff(&b, f, s)
		b.WriteString("Write your review into a file and finish with `gh pr review --approve --body-file <file>` or `gh pr review --request-changes --body-file <file>`. Put each finding at file:line. The review is recorded for this card; a PR number, URL or other target is unsupported.\n\n")
		b.WriteString(BrokenFindingText + "\n\n")
		b.WriteString("If you write " + s.Job + "/RESULT.md yourself, use this read shape and report only a verdict the review supports:\n\n")
		b.WriteString(ShapeText("read") + "\n")
	} else {
		fmt.Fprintf(&b, "# JOB: %s, attempt %d\n\n", f.Card, f.Attempt)
		fmt.Fprintf(&b, "The staged checkout %s holds %s on branch %s at %s, from %s. Work and commit there, or in a linked worktree of that same repository under %s.\n\n", s.Repo, f.Repo, f.Branch, s.Head, orDash(f.BaseRef), s.Job)
		b.WriteString("`git -C` and linked worktrees use real Git. `git push [-u] origin [HEAD|branch]` records the source commit without contacting origin; the sprint pushes it to the card's branch when this card finishes. Branch deletion, force and other push options are unsupported.\n\n")
		b.WriteString("After committing, make a pull request body file with the gate command and output. From the checkout or linked worktree that holds the commit, run `gh pr create --title \"<title>\" --body-file <file>`. Its title is the one-line report. The shim records the finish; the sprint opens the pull request outside the wall. Do not name another base, head or repository, or request a draft.\n\n")
		b.WriteString("A successful work card has a new commit. If there is nothing to do, use a `nothing: <why>` title and a body file with the reason; that records a failed-work judgment, not a successful change. If work cannot be done, write " + s.Job + "/RESULT.md with `verdict: not-done` in this shape:\n\n")
		b.WriteString(ShapeText("work") + "\n")
	}
	writeCommon(&b, f, s)
	return b.String()
}
