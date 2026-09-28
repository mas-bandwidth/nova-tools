package launch

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ReadEnvFile reads a shell environment file of the shape the fleet play
// writes (the card.env template in rowan-tools): one `export KEY=value` or
// `KEY=value` per line, blank lines and # comments skipped, a value optionally
// in single or double quotes. It returns KEY=value pairs for exec.Cmd.Env and
// refuses a line it cannot read, naming it: a card.env with a typo is a
// launch that fails at once, never a card that runs with half an environment.
func ReadEnvFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return nil, fmt.Errorf("%s:%d: not KEY=value: %q", path, n, sc.Text())
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
		out = append(out, key+"="+val)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
