package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func init() {
	register(verb{
		name:    "certified",
		summary: "ask whether a green certification run vouches for a commit",
		help: `usage: go run ./tools/ghrelease certified

Does a green certification.yml run vouch for this exact commit? Exit 0 vouched;
1 refused, with the remedy; 2 the environment is incomplete.

Environment: GITHUB_REPOSITORY, SHA (the commit being built), REF (the ref, for
the remedy line), GITHUB_RUN_ID (for the rerun line), GH_TOKEN.

Asked twice by release.yml: once as the whole certified job, and again at the
start of the release job, because one snapshot cannot see a run created after it
was taken: a red that starts after the first ask and finishes before the
binaries are built would otherwise release.

One asker, and it reads the status rather than the exit code: gh api exits 1 on
an empty answer and on no answer alike, and -i puts the status line first and
the body after the blank line. One snapshot, no status filter: two asks
(completed, then in flight) are two snapshots, and a run that finishes red
between them is in neither answer. So every run on the commit is fetched once
and the decision is made from that one list. A run not yet completed (queued,
waiting, in progress) refuses with a wait line. Otherwise the runs carrying the
latest update decide, not the newest by creation or by array position, because
a rerun of an older run id is newer evidence than a later run that was never
rerun. The latest-update group must be uniformly green: two runs can share the
maximal updated_at to the second with opposite conclusions, the stamp supplies
no order between them, and a run id must not invent one. If any member of that
group is not success, the verb refuses naming it; otherwise the group vouches
and one member is printed as the receipt.
`,
		do: doCertified,
	})
}

func doCertified(e env, args []string) int {
	if len(args) != 0 {
		fmt.Fprintf(e.stderr, "usage: %s certified   (the environment carries the rest; run: go run ./tools/ghrelease help certified)\n", tool)
		return 2
	}
	v, ok := e.requireEnv("certified", "GITHUB_REPOSITORY", "SHA", "REF", "GITHUB_RUN_ID", "GH_TOKEN")
	if !ok {
		return 2
	}
	repo, sha, ref, runID := v["GITHUB_REPOSITORY"], v["SHA"], v["REF"], v["GITHUB_RUN_ID"]

	out, rc := e.client().API("-i", fmt.Sprintf("repos/%s/actions/workflows/certification.yml/runs?head_sha=%s&per_page=100", repo, sha))
	if !hasStatus(out, "200") {
		fmt.Fprintf(e.stdout, "asking GitHub for certification runs on %s did not answer 200 (gh exit %d):\n", sha, rc)
		fmt.Fprintln(e.stdout, chomp(out))
		return 1
	}

	var doc struct {
		TotalCount json.RawMessage   `json:"total_count"`
		Runs       []json.RawMessage `json:"workflow_runs"`
	}
	if err := json.Unmarshal([]byte(answerBody(out)), &doc); err != nil {
		fmt.Fprintf(e.stdout, "refusing: the certification runs answer on %s is not the list GitHub documents: %v\n", sha, err)
		return 1
	}
	total := jqText(doc.TotalCount)
	listed := strconv.Itoa(len(doc.Runs))
	if total != listed {
		fmt.Fprintf(e.stdout, "refusing: %s certification runs on %s but only %s listed; the selection would be ambiguous\n", total, sha, listed)
		return 1
	}

	runs := make([]map[string]any, len(doc.Runs))
	for i, raw := range doc.Runs {
		if err := decodeUseNumber(raw, &runs[i]); err != nil {
			fmt.Fprintf(e.stdout, "refusing: a certification run on %s is not an object: %v\n", sha, err)
			return 1
		}
	}
	for _, r := range runs {
		fmt.Fprintf(e.stdout, "  run %s attempt %s %s %s updated %s %s\n",
			text(r["id"]), text(r["run_attempt"]), text(r["status"]), orText(r["conclusion"], "-"), text(r["updated_at"]), text(r["html_url"]))
	}

	inflight := 0
	for _, r := range runs {
		if r["status"] != "completed" {
			inflight++
		}
	}
	if inflight != 0 {
		fmt.Fprintf(e.stdout, "refusing: %d certification run(s) on %s not yet completed; wait for certification-ok, then re-run this workflow: gh run rerun %s -R %s\n", inflight, sha, runID, repo)
		return 1
	}

	// The latest-update group decides, and it must be uniformly green.
	maxu := "-"
	var stamps []string
	for _, r := range runs {
		if s, ok := r["updated_at"].(string); ok {
			stamps = append(stamps, s)
		}
	}
	for i, s := range stamps {
		if i == 0 || s > maxu {
			maxu = s
		}
	}
	var group []map[string]any
	for _, r := range runs {
		if s, ok := r["updated_at"].(string); ok && s == maxu {
			group = append(group, r)
		}
	}
	red := 0
	for _, r := range group {
		if r["conclusion"] != "success" {
			red++
		}
	}
	// The receipt is the first member that is not success, else the first member.
	newest := map[string]any{}
	if len(group) > 0 {
		newest = group[0]
		for _, r := range group {
			if r["conclusion"] != "success" {
				newest = r
				break
			}
		}
	}
	fmt.Fprintf(e.stdout, "certification runs on %s: %s completed; latest update %s shared by %d run(s), %d not green; receipt run %s attempt %s, concluded %s\n",
		sha, total, maxu, len(group), red, orText(newest["id"], "none"), orText(newest["run_attempt"], "0"), orText(newest["conclusion"], "none"))

	if total == "0" || red != 0 {
		if total == "0" {
			fmt.Fprintf(e.stdout, "refusing: no completed certification run on %s, so nothing vouches for this tree\n", sha)
		} else {
			fmt.Fprintf(e.stdout, "refusing: the latest certification evidence on %s is not uniformly green (%d of %d in the latest-stamp group not success; see the receipt line), so nothing vouches for this tree\n", sha, red, len(group))
		}
		fmt.Fprintln(e.stdout, "certify it first (a run still in progress does not count; wait for certification-ok):")
		fmt.Fprintf(e.stdout, "  gh workflow run certification.yml -R %s --ref %s\n", repo, ref)
		fmt.Fprintf(e.stdout, "then re-run this workflow: gh run rerun %s -R %s\n", runID, repo)
		return 1
	}
	return 0
}

// decodeUseNumber decodes one JSON value, keeping numbers as written so an id
// prints as GitHub sent it.
func decodeUseNumber(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(into)
}

// text renders a decoded JSON value the way a person reads it in a log line: a
// string as itself, a number as written, null as null.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// orText is text, except that an absent, null or false value renders as def.
func orText(v any, def string) string {
	if v == nil || v == false {
		return def
	}
	return text(v)
}

// jqText renders a raw JSON value the same way; an absent value is null.
func jqText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	var v any
	if err := decodeUseNumber(raw, &v); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return text(v)
}
