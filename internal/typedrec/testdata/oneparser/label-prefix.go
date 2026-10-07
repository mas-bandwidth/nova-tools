package oneparser

import "strings"

func parseRepo(line string) (string, bool) {
	if strings.HasPrefix(line, "REPO: ") {
		return line[len("REPO: "):], true
	}
	return "", false
}
