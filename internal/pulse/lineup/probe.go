package lineup

import (
	"fmt"
	"strings"
)

// ProbeOptions is what the remote script is told. Every path is optional: empty is the
// bench standard's own default under the bench's HOME, which the script expands there.
type ProbeOptions struct {
	Home         string   // the bench's home (the benches file's third column); empty keeps the login's
	Results      string   // the results root; default $HOME/nova-bench/results
	Roots        []string // the job roots; default $HOME/rowan-swarm-root and $HOME/rowan-working/tmp
	StageReceipt string   // the stage receipt; default $HOME/nova-bench/STAGE-RECEIPT
	StageCmd     string   // when set, the probe RUNS the stage and times it instead of reading a receipt
	GHConfig     string   // GH_CONFIG_DIR for the `gh auth token` probe; empty keeps the login's
	Tools        []string // the nova tools whose version line is read from $HOME/.local/bin
	JobGraceMin  int      // a finished job younger than this many minutes is still being released
}

// quote single-quotes a value for the remote shell.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ProbeScript is the remote side: one bash script, run as `ssh <bench> bash -s`, that
// prints one FACT<TAB>name<TAB>value line per fact and a closing FACT<TAB>end. It never
// prints a credential: the push probe prints which source answered, not what it said.
func ProbeScript(o ProbeOptions) string {
	tools := o.Tools
	if len(tools) == 0 {
		tools = DefaultTools
	}
	grace := o.JobGraceMin
	if grace <= 0 {
		grace = 10
	}
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	if o.Home != "" {
		w("HOME=%s; export HOME", quote(o.Home))
	}
	w(`LC_ALL=C; export LC_ALL`)
	w(`PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"; export PATH`)
	w(`fact() { printf 'FACT\t%%s\t%%s\n' "$1" "$(printf '%%s' "$2" | tr '\t\r\n' '   ')"; }`)
	w(`mtime() { stat -c %%Y "$1" 2>/dev/null || stat -f %%m "$1" 2>/dev/null; }`)

	// stage: run it and time it, or read the receipt the last stage wrote.
	if o.StageCmd != "" {
		w(`s0=$(date +%%s); out=$(bash -c %s 2>&1); s1=$(date +%%s)`, quote(o.StageCmd))
		w(`fact stage "$(printf '%%s\n' "$out" | grep '^STAGE ' | tail -n 1)"`)
		w(`fact stage_elapsed_s "$((s1 - s0))"`)
	} else {
		if o.StageReceipt != "" {
			w(`r=%s`, quote(o.StageReceipt))
		} else {
			w(`r="$HOME/nova-bench/STAGE-RECEIPT"`)
		}
		w(`fact stage_receipt "$r"`)
		w(`if [ -f "$r" ]; then fact stage "$(grep '^STAGE ' "$r" | tail -n 1)"; m=$(mtime "$r"); [ -n "$m" ] && fact stage_age_s "$(( $(date +%%s) - m ))"; else fact stage ""; fi`)
	}

	// push-credential: env, then gh, then git's own credential helpers; never prompt.
	gh := ""
	if o.GHConfig != "" {
		gh = "GH_CONFIG_DIR=" + quote(o.GHConfig) + " "
	}
	w(`if [ -n "${GH_TOKEN:-}${GITHUB_TOKEN:-}" ]; then fact push_credential yes:env`)
	w(`elif command -v gh >/dev/null 2>&1 && t=$(%sgh auth token --hostname github.com 2>/dev/null) && [ -n "$t" ]; then fact push_credential yes:gh`, gh)
	w(`elif printf 'protocol=https\nhost=github.com\n\n' | GIT_TERMINAL_PROMPT=0 GIT_ASKPASS=/bin/false SSH_ASKPASS=/bin/false git credential fill 2>/dev/null | grep -q '^password=.'; then fact push_credential yes:git-credential`)
	w(`else fact push_credential no; fi; t=`)

	// results-root: there, a directory, and writable.
	if o.Results != "" {
		w(`r=%s`, quote(o.Results))
	} else {
		w(`r="$HOME/nova-bench/results"`)
	}
	w(`if [ ! -e "$r" ]; then fact results_root "missing $r"`)
	w(`elif [ ! -d "$r" ]; then fact results_root "notdir $r"`)
	w(`else p="$r/.lineup-probe.$$"; if ( : > "$p" ) 2>/dev/null; then rm -f "$p"; fact results_root "ok $r"; else fact results_root "unwritable $r"; fi; fi`)

	// finished-jobs: <root>/<slot>/jobs/<job> with RESULT.md or .harvested, quiet past the grace.
	if len(o.Roots) > 0 {
		q := make([]string, len(o.Roots))
		for i, r := range o.Roots {
			q[i] = quote(r)
		}
		w(`set -- %s`, strings.Join(q, " "))
	} else {
		w(`set -- "$HOME/rowan-swarm-root" "$HOME/rowan-working/tmp"`)
	}
	w(`n=0; first=`)
	w(`for root in "$@"; do for j in "$root"/*/jobs/*/; do [ -d "$j" ] || continue`)
	w(`  if [ -e "$j/RESULT.md" ] || [ -e "$j/.harvested" ]; then`)
	w(`    [ -n "$(find "$j" -maxdepth 0 -mmin -%d 2>/dev/null)" ] && continue`, grace)
	w(`    n=$((n + 1)); [ -z "$first" ] && first=${j%%/}`)
	w(`  fi; done; done`)
	w(`fact finished_jobs "$n $first"`)

	// version-<tool>: the first line of the tool's own version verb.
	for _, t := range tools {
		w(`fact %s "$("$HOME/.local/bin/"%s version 2>/dev/null | head -n 1)"`, quote("version:"+t), quote(t))
	}
	w(`fact end 1`)
	return b.String()
}
