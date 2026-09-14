package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type specRule struct {
	Path              string
	Number, Line, End int
	Text              string
}
type scopedSpec struct {
	Path, Heading string
	Rules         map[int]specRule
}

var numberedRule = regexp.MustCompile(`^(\d+)\. `)

// parseScopedSpec chooses one numbered sequence. A literal H2 title scopes a
// sequence; accepting a partial match would make the packet guess.
func parseScopedSpec(p, source, heading string) (scopedSpec, error) {
	lines := strings.Split(source, "\n")
	var starts []int
	for i, line := range lines {
		if strings.HasPrefix(line, "1. ") {
			starts = append(starts, i)
		}
	}
	start := 0
	if heading != "" {
		start = -1
		for i, line := range lines {
			if strings.HasPrefix(line, "## ") && strings.TrimSpace(strings.TrimPrefix(line, "## ")) == heading {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return scopedSpec{}, fmt.Errorf("--spec %s names no heading %q", p, heading)
		}
	} else if len(starts) > 1 {
		var headings []string
		for _, sequence := range starts {
			for i := sequence - 1; i >= 0; i-- {
				if strings.HasPrefix(lines[i], "## ") {
					headings = append(headings, "#"+strings.TrimSpace(strings.TrimPrefix(lines[i], "## ")))
					break
				}
			}
		}
		return scopedSpec{}, fmt.Errorf("--spec %s has multiple numbered sequences; add one of %s", p, strings.Join(headings, ", "))
	} else if len(starts) == 1 {
		// The unique sequence can follow a title, introduction and H2 heading.
		// Starting at byte zero would stop on that heading before any rule.
		start = starts[0]
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	out := scopedSpec{Path: p, Heading: heading, Rules: map[int]specRule{}}
	for i := start; i < end; i++ {
		match := numberedRule.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		n, _ := strconv.Atoi(match[1])
		j := i + 1
		for ; j < end && !numberedRule.MatchString(lines[j]); j++ {
		}
		out.Rules[n] = specRule{Path: p, Number: n, Line: i + 1, End: j + 1, Text: strings.Join(lines[i:j], "\n")}
	}
	return out, nil
}

func splitSpecFlag(v string) (p, heading string, err error) {
	p, heading, _ = strings.Cut(v, "#")
	if p == "" {
		return "", "", fmt.Errorf("--spec wants <path>[#<heading>]")
	}
	return p, heading, nil
}

// citedRuleNumbers accepts only Rule 2's closed grammar. It deliberately does
// not treat prose such as "rule 3 through 7" as a citation. This ordinary-line
// helper uses the same finite parser as the streaming diff reader.
func citedRuleNumbers(line string) []int {
	s := newCitationNumberSequence()
	for _, r := range strings.ToLower(line) {
		s.Feed(r)
	}
	s.Finish()
	numbers, _ := s.Result()
	return numbers
}

func citationTargets(line string, specs []scopedSpec) []specRule {
	if len(specs) == 0 {
		return nil
	}
	s := newStreamingCitations(specs)
	s.Feed([]byte(line))
	return s.Finish()
}

type diffFile struct {
	Path                  string
	Hunks                 []string
	AddedLines, GoneLines []int
}

// changedFiles keeps git's file order and hunk coordinates for changed-spec rules.
func changedFiles(diff string) []diffFile {
	var files []diffFile
	var current *diffFile
	oldLine, newLine := 0, 0
	hunk := regexp.MustCompile(`^@@ -([0-9]+)(?:,[0-9]+)? \+([0-9]+)(?:,[0-9]+)? @@(.*)$`)
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git a/") {
			p := strings.Split(strings.TrimPrefix(line, "diff --git a/"), " b/")
			if len(p) == 2 {
				files = append(files, diffFile{Path: p[1]})
				current = &files[len(files)-1]
			}
			continue
		}
		if current == nil {
			continue
		}
		if m := hunk.FindStringSubmatch(line); m != nil {
			oldLine, _ = strconv.Atoi(m[1])
			newLine, _ = strconv.Atoi(m[2])
			current.Hunks = append(current.Hunks, strings.TrimSpace(m[3]))
			continue
		}
		if oldLine == 0 && newLine == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			current.AddedLines = append(current.AddedLines, newLine)
			newLine++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			current.GoneLines = append(current.GoneLines, oldLine)
			oldLine++
		default:
			oldLine++
			newLine++
		}
	}
	return files
}
func orderedRules(rules map[string]specRule) []specRule {
	out := make([]specRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Line < out[j].Line
		}
		return out[i].Path < out[j].Path
	})
	return out
}
