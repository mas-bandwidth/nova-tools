package oneparser

import (
	"fmt"
	"io"
)

func parseClean(c map[string]string, w io.Writer, v string) {
	if c["outcome"] == "DONE" {
		fmt.Fprintf(w, "CHECK: %s\n", v)
	}
}
