// Package keyshape recognises the FORM of a key in text a worker produced, so nothing
// carrying one is pushed to a forge (nova-tools #1814).
//
// WHY IT EXISTS. `harvest` copies a card's RESULT.md verbatim into the body of the draft
// PR it opens, and pushes the card's commits. A card's model that read the seat's provider
// key out of its shell -- which it could, until the shell shim in cmd/nova-swarm closed
// that channel -- had in those two paths a way to put the key on the forge. The shim is
// the lock; this package is the backstop behind it, and a backstop is worth having because
// the shim is bypassable in ways this is not (an absolute /bin/bash, a harness that spawns
// a shell a third way).
//
// WHAT IT NEVER DOES. It never prints matched text. A finding is a shape NAME, a path and
// a line number, and for the environment check a variable NAME and a LENGTH. The value of
// a key appears in no finding, no log and no error, because a finding that quotes the key
// has copied the key into everything that carries the finding.
//
// TWO KINDS OF CHECK.
//
//   - SHAPES (keyshapes.txt, embedded): the published prefixes of the issuers -- PEM
//     armour, age, the forge's tokens, the provider prefixes, a JWT. The reader holds no
//     key to compare against and needs none.
//   - THE CALLER'S OWN SECRET-NAMED VARIABLES: for every environment name carrying KEY,
//     TOKEN or SECRET, the text is searched for that variable's value. This one does hold
//     a value -- the process's own environment already does -- and it still prints
//     nothing: the finding names the VARIABLE and the LENGTH. It catches the key this
//     fleet actually holds even when its shape is not on the list.
package keyshape

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
)

//go:embed keyshapes.txt
var keyShapeData string

// Shape is one row of keyshapes.txt: a name a finding may print, and a regexp that
// recognises a form. The value a key would have is nowhere in this package.
type Shape struct {
	Name string
	re   *regexp.Regexp
}

var (
	loadOnce sync.Once
	shapes   []Shape
	loadErr  error
)

// Shapes is the parsed list, loaded once. A malformed data file is an error the caller
// reports rather than a check that quietly matches nothing.
func Shapes() ([]Shape, error) {
	loadOnce.Do(func() { shapes, loadErr = parse(keyShapeData) })
	return shapes, loadErr
}

func parse(data string) ([]Shape, error) {
	var out []Shape
	for n, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		name, pattern, ok := strings.Cut(line, "\t")
		name, pattern = strings.TrimSpace(name), strings.TrimSpace(pattern)
		if !ok || name == "" || pattern == "" {
			return nil, fmt.Errorf("keyshapes.txt:%d: a row that is not <name><TAB><regexp>", n+1)
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("keyshapes.txt:%d: %s: %w", n+1, name, err)
		}
		out = append(out, Shape{Name: name, re: re})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("keyshapes.txt holds no shape")
	}
	return out, nil
}

// Finding is one hit: which shape, which file, which line. Name and Len are set only for
// an env-value hit, and Name is a VARIABLE's name, never a value.
type Finding struct {
	Shape string
	Path  string
	Line  int
	Name  string
	Len   int
}

// String is the one-line form a refusal prints. It carries no matched text.
func (f Finding) String() string {
	s := fmt.Sprintf("file=%s line=%d shape=%s", field(f.Path), f.Line, field(f.Shape))
	if f.Name != "" {
		s += fmt.Sprintf(" name=%s len=%d", field(f.Name), f.Len)
	}
	return s
}

func field(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return strings.Join(strings.Fields(s), "_")
}

// SecretName says whether an environment NAME carries a secret: the one predicate
// cmd/nova-swarm redacts its argv log by and the shell shim unsets by. Uppercased, so
// `deepseek_api_key` and `MiXeD_KeY` are both caught.
func SecretName(name string) bool {
	up := strings.ToUpper(name)
	return strings.Contains(up, "KEY") || strings.Contains(up, "TOKEN") || strings.Contains(up, "SECRET")
}

// minSecretValue is the shortest environment value worth searching for. Below it a value
// is a flag, a word or a placeholder, and searching for it would refuse every card.
const minSecretValue = 12

// ScanText returns every shape hit in text, line by line, and every hit of a value held by
// a secret-named variable in env (os.Environ()'s form). A nil env skips the second check.
// The returned findings are ordered by line; the matched text is not returned at all.
func ScanText(path, text string, env []string) ([]Finding, error) {
	list, err := Shapes()
	if err != nil {
		return nil, err
	}
	var values []Finding
	for _, kv := range env {
		name, val, _ := strings.Cut(kv, "=")
		if !SecretName(name) || len(val) < minSecretValue {
			continue
		}
		values = append(values, Finding{Shape: "env-value", Path: path, Name: name, Len: len(val)})
	}
	var out []Finding
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		for _, s := range list {
			if s.re.MatchString(line) {
				out = append(out, Finding{Shape: s.Name, Path: path, Line: n})
			}
		}
		for _, v := range values {
			// The value is held by this process already; it is compared, never printed.
			if strings.Contains(line, envValue(env, v.Name)) {
				f := v
				f.Line = n
				out = append(out, f)
			}
		}
	}
	return out, sc.Err()
}

func envValue(env []string, want string) string {
	for _, kv := range env {
		if name, val, _ := strings.Cut(kv, "="); name == want {
			return val
		}
	}
	return ""
}

// ScanFile reads one file and scans it. A file that is not there is no finding and no
// error: a job that wrote no RESULT.md is an abstain, which the caller classifies its own
// way. A file that cannot be read IS an error, because a check that could not run must not
// read as a check that passed.
func ScanFile(path string, env []string) ([]Finding, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ScanText(path, string(raw), env)
}
