package main

import unusedverbs "github.com/mas-bandwidth/nova-tools/internal/nsprint/verbs"

// verbs is `nova-sprint verbs unused` (nova-tools #3160): the verbs the dev
// build ships that nobody but their author ran in the window, appended to
// verbs:unused:log; `--check` says whether that count fell.
func init() {
	register(Verb{Name: "verbs", Summary: unusedverbs.VerbSummary, Run: unusedverbs.Main})
}
