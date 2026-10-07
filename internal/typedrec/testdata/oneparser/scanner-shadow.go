package oneparser

import (
	"bufio"
	"io"
	"strings"
)

func parseScanner(r io.Reader) string {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, _, _ := strings.Cut(sc.Text(), ":")
		switch k {
		case "SUGGEST":
			return k
		}
	}
	return ""
}
