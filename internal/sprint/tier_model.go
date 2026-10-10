package sprint

import (
	"path"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
)

// A card that writes a TLA+ model is tiered frontier (the owner, 2026-10-04: "When we do
// TLA+ modeling work, I would like that to go to frontier models."; docs/SPEC-SPRINT.md,
// the card decides its model). Model work is read off the card's PATHS, never its prose:
// a .tla module anywhere, an MC config or the directory itself under a tla/ directory.
// The TLC run records (tla/RUNS.tsv, tla/CASES.tsv) alone are no model: running the
// checker is mechanical.

// modelProbes are the file names a PATHS entry's last part is matched against: a module
// and a model-checking config.
var modelProbes = []string{"Model.tla", "MCModel.cfg"}

// ModelPaths is the entries of paths that can name a model: one whose last part is a .tla
// module or a glob of them (*.tla), anywhere; one under a tla/ directory whose last part
// is a config or matches a module or a config (*, **, MC*.cfg); and the tla/ directory
// itself. A bare glob outside tla/ (security/**) names no model.
func ModelPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		p = strings.TrimPrefix(strings.TrimSpace(p), "./")
		dirs := strings.Split(strings.TrimSuffix(p, "/"), "/")
		base := dirs[len(dirs)-1]
		inTLA := slices.Contains(dirs[:len(dirs)-1], "tla")
		module, _ := path.Match(base, modelProbes[0])
		config, _ := path.Match(base, modelProbes[1])
		if base == "tla" || strings.HasSuffix(base, ".tla") || inTLA && (module || config || strings.HasSuffix(base, ".cfg")) {
			out = append(out, p)
		}
	}
	return out
}

// ModelTier is the brief add admits for a card whose PATHS name model work (ModelPaths):
// with tier: frontier on its line 1 when it names no tier, and said the words the add's
// unit carries; the brief as given, said "", when it names frontier or names no model
// work; why the refusal when line 1 names a lower tier. A brief whose model lines do not
// read is the model-lines check's to refuse, and is given back as it is.
func ModelTier(brief string) (out, said, why string) {
	model := ModelPaths(decide.CardPaths(brief))
	if len(model) == 0 {
		return brief, "", ""
	}
	m, bad := cardhdr.ReadModel(brief)
	work := "PATHS name TLA+ model work (" + strings.Join(model, ",") + ")"
	switch {
	case bad != "" || m.Tier == cardhdr.RouteFrontier:
		return brief, "", ""
	case m.Tier != "":
		return brief, "", work + "; a card that writes a model is tiered frontier (docs/SPEC-SPRINT.md, the card decides its model), and line 1 names tier " + m.Tier +
			": write tier: frontier on line 1, or take the model out of PATHS (tla/RUNS.tsv and tla/CASES.tsv alone are run records, any tier)"
	}
	first, rest, nl := strings.Cut(brief, "\n")
	out = strings.TrimRight(first, " \t") + " tier: " + cardhdr.RouteFrontier
	if nl {
		out += "\n" + rest
	}
	return out, "tiered frontier: " + work, ""
}
