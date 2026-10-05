package main

import (
	"slices"
)

// view cards is a verb of the view group, registered here so its words stay in the view's
// files. Go runs a package's init functions in the order of its files' names, and this
// file's name sorts after verbs.go, so the verb table it joins is already built; it goes in
// after view worker, keeping coordinator the table's last verb (verbs.go). Serving it
// through nova-sprint serve is blocked by serve.go being outside PATHS.
func init() {
	v := verb{"view cards", "[--col <c>] [--stream <s>] [--holder <member>] [--by tier|stream|col|holder] [--json]", "view cards --col review --by tier", (*app).cmdViewCards}
	at := slices.IndexFunc(verbs, func(w verb) bool { return w.name == "view worker" }) + 1
	if at == 0 {
		at = len(verbs)
	}
	verbs = slices.Insert(verbs, at, v)
	verbEffect["view cards"] = "inspection: counts or lists the primaries, kept to --col, --stream and --holder, counted by tier, stream, col or holder with --by, writes nothing"
}
