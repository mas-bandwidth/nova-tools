package verbflag

import (
	"slices"
	"strings"
)

// Verbs is the verbs a tool's help text names, in the order it first names them:
// the first word after prog on each line that begins with prog, and the pair when
// a second word that names a verb follows it (`fn load`, `seat add`; `set|show`
// is both). `help` and `version` take no verb after them, so a word that follows
// one is prose. A flag or a placeholder is never a verb.
func Verbs(banner, prog string) []string {
	var verbs []string
	add := func(v string) {
		if v != "help" && !slices.Contains(verbs, v) {
			verbs = append(verbs, v)
		}
	}
	for _, line := range strings.Split(banner, "\n") {
		words := strings.Fields(line)
		if len(words) < 2 || words[0] != prog || !subverbRe.MatchString(words[1]) {
			continue
		}
		for _, first := range strings.Split(words[1], "|") {
			add(first)
			if len(words) > 2 && first != "help" && first != "version" && subverbRe.MatchString(words[2]) {
				for _, second := range strings.Split(words[2], "|") {
					add(first + " " + second)
				}
			}
		}
	}
	return verbs
}

// HelpArgs is the argv `<tool> help <verb> ...` stands for: args is what follows
// `help`, and the result is the verb's own words, then --help, then whatever else
// was typed. So a word, a `--` or a flag after the verb never stands between the
// verb and its help, and never turns the request into a run or a refusal. The verb
// is the longest of the verbs the help text names, and of extra, that args begin
// with ("fn load" over "fn"); with none, the first word. args is not empty.
func HelpArgs(args []string, banner, prog string, extra ...string) []string {
	n := 1
	for _, v := range slices.Concat(Verbs(banner, prog), extra) {
		if w := strings.Fields(v); len(w) > n && len(args) >= len(w) && slices.Equal(args[:len(w)], w) {
			n = len(w)
		}
	}
	return slices.Concat(args[:n], []string{"--help"}, args[n:])
}
