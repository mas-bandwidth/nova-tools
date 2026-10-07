package docs

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
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

// checkRatingForms walks a docs/ratings tree and checks every rating file of
// a combined-form release. It returns the files checked and one problem per
// missing part, each naming the file and the part.
func checkRatingForms(fsys fs.FS) (checked, problems []string, err error) {
	releases, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, nil, err
	}
	for _, rel := range releases {
		if !rel.IsDir() || !ratingFormRelease(rel.Name()) {
			continue
		}
		entries, err := fs.ReadDir(fsys, rel.Name())
		if err != nil {
			return nil, nil, err
		}
		for _, e := range entries {
			name := path.Join(rel.Name(), e.Name())
			if !e.Type().IsRegular() || !ratingFileRE.MatchString(e.Name()) {
				problems = append(problems, name+": not a <tool>-<friend>.md rating file")
				continue
			}
			body, err := fs.ReadFile(fsys, name)
			if err != nil {
				return nil, nil, err
			}
			checked = append(checked, name)
			for _, part := range ratingFormMissing(string(body)) {
				problems = append(problems, name+": missing "+part)
			}
		}
	}
	return checked, problems, nil
}

// ratingFormRelease reports whether a directory name is a release at or
// after ratingFormFirstRelease.
func ratingFormRelease(name string) bool {
	m := ratingReleaseRE.FindStringSubmatch(name)
	first := ratingReleaseRE.FindStringSubmatch(ratingFormFirstRelease)
	if m == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		a, _ := strconv.Atoi(m[i])
		b, _ := strconv.Atoi(first[i])
		if a != b {
			return a > b
		}
	}
	return true
}

// ratingFormMissing names every part of the form a rating file lacks.
func ratingFormMissing(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var header []string
	sections := map[string][]string{}
	current := ""
	inHeader := true
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.HasPrefix(line, "## ") {
			inHeader = false
			current = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			if _, ok := sections[current]; !ok {
				sections[current] = []string{}
			}
			continue
		}
		if inHeader {
			header = append(header, line)
		} else {
			sections[current] = append(sections[current], line)
		}
	}

	var missing []string
	for _, h := range []struct {
		re   *regexp.Regexp
		part string
	}{
		{ratingReadRE, "header line READ: n/10"},
		{ratingUseRE, "header line USE: n/10"},
		{ratingBuildRE, "header line Build: <commit>"},
	} {
		if !anyLineMatches(header, h.re) {
			missing = append(missing, h.part)
		}
	}
	for _, s := range ratingSections {
		if _, ok := sections[s]; !ok {
			missing = append(missing, "section ## "+s)
		}
	}
	if findings, ok := sections["Findings"]; ok {
		missing = append(missing, ratingFindingsMissing(findings)...)
	}
	return missing
}

func anyLineMatches(lines []string, re *regexp.Regexp) bool {
	for _, l := range lines {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// ratingFindingsMissing checks the Findings section: a table with a where
// column (the place) and a fix column, at least one row, and every row with
// both cells filled.
func ratingFindingsMissing(lines []string) []string {
	where, fix, start := -1, -1, -1
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "|") || !ratingTableSep.MatchString(lines[i+1]) {
			continue
		}
		where, fix = -1, -1
		for j, c := range markdownCells(lines[i]) {
			switch strings.ToLower(c) {
			case "where":
				where = j
			case "fix":
				fix = j
			}
		}
		if where >= 0 && fix >= 0 {
			start = i + 2
			break
		}
	}
	if start < 0 {
		return []string{"Findings table with where and fix columns"}
	}
	var missing []string
	n := 0
	for _, line := range lines[start:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		n++
		cells := markdownCells(line)
		if where >= len(cells) || cells[where] == "" {
			missing = append(missing, fmt.Sprintf("finding %d: a place (where)", n))
		}
		if fix >= len(cells) || cells[fix] == "" {
			missing = append(missing, fmt.Sprintf("finding %d: a fix", n))
		}
	}
	if n == 0 {
		return []string{"Findings table with at least one finding"}
	}
	return missing
}

// markdownCells splits a table row into its trimmed cells. A pipe inside a
// code span or escaped as \| is part of the cell.
func markdownCells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	var cells []string
	var cell strings.Builder
	inCode := false
	for i := 0; i < len(row); i++ {
		c := row[i]
		switch {
		case c == '\\' && i+1 < len(row) && row[i+1] == '|':
			cell.WriteString(`\|`)
			i++
		case c == '`':
			inCode = !inCode
			cell.WriteByte(c)
		case c == '|' && !inCode:
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(c)
		}
	}
	if rest := strings.TrimSpace(cell.String()); rest != "" {
		cells = append(cells, rest)
	}
	return cells
}
