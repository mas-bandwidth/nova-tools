package docs

import (
	"regexp"
)

// The rating form docs/ratings/README.md defines, checked by
// TestRatingFileIsInForm. A release from ratingFormFirstRelease on holds one
// combined READ and USE file per tool and friend; the releases before it are
// the earlier split form (one READ and one USE file) and are records, kept as
// written.
const ratingFormFirstRelease = "1.2.0"

var (
	ratingReleaseRE = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)$`)
	ratingFileRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*-[a-z0-9]+\.md$`)
	ratingReadRE    = regexp.MustCompile(`^READ: (10|[0-9](\.[0-9])?)/10$`)
	ratingUseRE     = regexp.MustCompile(`^USE: (10|[0-9](\.[0-9])?)/10$`)
	ratingBuildRE   = regexp.MustCompile(`^Build: [0-9a-f]{7,40}(\s.*)?$`)
	ratingTableSep  = regexp.MustCompile(`^\|[\s:|-]+$`)
)

// ratingSections are the sections every rating file carries, by heading.
var ratingSections = []string{"Reasons", "Findings", "Good, keep", "Compared with earlier ratings"}

// ratingFormParts are the words of the form the README must state as the
// test checks them.
var ratingFormParts = []string{
	"READ: n/10", "USE: n/10", "Build: <commit>",
	"## Reasons", "## Findings", "## Good, keep", "## Compared with earlier ratings",
	"`where`", "`fix`",
}
