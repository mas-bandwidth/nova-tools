package oneparser

import (
	"fmt"
	"strings"
)

func sameRepo(line, repo string) bool {
	return line == fmt.Sprintf("REPO: %s", repo) || strings.HasPrefix(line, fmt.Sprint("REPO: ", repo))
}
