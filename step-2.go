// docnames: go run docnames.go [-n] <file.md>... (fleet names in code spans and fences into example names)
package main

import ("flag";"fmt";"os";"regexp";"strings")

var names = map[string]string{"rowan": "ada", "stella": "lin", "emma": "kai", "johnny": "sam", "freddy": "max", "glenn": "eve", "studio": "bench1", "hulk": "bench2", "vision": "bench3", "superman": "bench4", "batman": "bench5"}
var home = regexp.MustCompile(`/(Users|home)/glenn\b`)
var word = regexp.MustCompile(`(?i)\b(rowan|stella|emma|johnny|freddy|glenn|studio|hulk|vision|superman|batman)\b`)
var span = regexp.MustCompile("`[^`\n]+`")
var keep = strings.NewReplacer("rowan-new", "\x00rn\x00", "rowan-tools", "\x00rt\x00")
var unkeep = strings.NewReplacer("\x00rn\x00", "rowan-new", "\x00rt\x00", "rowan-tools")
var cardArgs = []string{"docs/SPEC-SECRETS.md", "docs/CLI.md", "docs/SPEC.md", "docs/SPEC-TOKENS.md", "docs/SPEC-CI.md", "docs/SPEC-CONFIG.md", "docs/SPEC-SANDBOX.md", "docs/SPEC-UPDATE.md", "docs/SPEC-SWARM.md", "docs/MODELS.md", "docs/TESTING.md", "docs/SPEC-RELEASE.md", "docs/SPEC-BUS-REPLY.md"} // the step's arguments, set on the card: the program runs with none
func main() {
    if len(os.Args) == 1 { os.Args = append(os.Args, cardArgs...) }
    dry := flag.Bool("n", false, "print only"); flag.Parse(); tot := 0
    for _, name := range flag.Args() {
        b, err := os.ReadFile(name); if err != nil { continue }
        n := 0
        fix := func(s string) string {
            s = keep.Replace(s) // repository names (rowan-new, rowan-tools) are citations, never fleet names
            s = home.ReplaceAllStringFunc(s, func(string) string { n++; return "$HOME" })
            return unkeep.Replace(word.ReplaceAllStringFunc(s, func(w string) string { n++; r := names[strings.ToLower(w)]; if w[0] >= 'A' && w[0] <= 'Z' { r = strings.ToUpper(r[:1]) + r[1:] }; return r }))
        }
        lines := strings.Split(string(b), "\n"); fence := ""
        for i, l := range lines {
            t := strings.TrimLeft(l, " >")
            if fence == "" && (strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")) { fence = t[:3]; continue }
            if fence != "" { if strings.HasPrefix(t, fence) { fence = "" } else { lines[i] = fix(l) }; continue }
            lines[i] = span.ReplaceAllStringFunc(l, fix)
        }
        fmt.Printf("DOCNAMES %s replaced=%d\n", name, n); tot += n
        if n > 0 && !*dry { if err := os.WriteFile(name, []byte(strings.Join(lines, "\n")), 0o644); err != nil { fmt.Println("DOCNAMES REFUSED:", err); os.Exit(2) } }
    }
    fmt.Printf("DOCNAMES replaced=%d\n", tot)
}
