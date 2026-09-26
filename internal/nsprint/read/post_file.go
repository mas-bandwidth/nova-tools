package read

// `read post --file scores.tsv` (nova-tools #4335): many typed lines in one
// call, one receipt per row, every refusal printed. A row is
//
//	<repo>\t<n>\t<typed line>
//
// repo is owner/name or name; a literal \n in the line is a newline, so a
// SCORE line's numbered items fit on one row. Blank rows and rows starting
// with # are skipped. Each row is one PostMeasured (the line store's one
// call, gates measured, the comment mirror unless --no-github); its receipt
// or refusal is printed prefixed ROW <i>. The last line is the tally:
//
//	READ POST FILE file=<f> rows=<n> posted=<p> refused=<r> github_calls=<c>
//
// Exit 0 every row posted, 1 a row refused (each printed), 2 a row could not
// run or the file could not be read.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
)

// Row is one parsed row of a scores file.
type Row struct {
	Num         int // 1-based line number in the file
	Owner, Repo string
	N           string
	Line        string
}

// ParseRows reads a scores file. A row that is not three tab-separated
// fields with a repo and a positive PR number is returned in bad with its
// refusal, never dropped.
func ParseRows(r io.Reader) (rows []Row, bad []string, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	num := 0
	for sc.Scan() {
		num++
		text := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(text) == "" || strings.HasPrefix(strings.TrimSpace(text), "#") {
			continue
		}
		f := strings.SplitN(text, "\t", 3)
		if len(f) != 3 {
			bad = append(bad, fmt.Sprintf("ROW %d READ POST REFUSED why=want <repo>\\t<n>\\t<typed line>, got %d tab-separated field(s)", num, len(f)))
			continue
		}
		owner, name, perr := prkey.Split(f[0])
		if perr != nil {
			bad = append(bad, fmt.Sprintf("ROW %d READ POST REFUSED why=%v", num, perr))
			continue
		}
		n := strings.TrimSpace(f[1])
		if v, aerr := strconv.Atoi(n); aerr != nil || v <= 0 {
			bad = append(bad, fmt.Sprintf("ROW %d READ POST REFUSED repo=%s why=n %q is not a positive PR number", num, name, n))
			continue
		}
		line := strings.TrimSpace(strings.ReplaceAll(f[2], `\n`, "\n"))
		if line == "" {
			bad = append(bad, fmt.Sprintf("ROW %d READ POST REFUSED repo=%s n=%s why=empty line", num, name, n))
			continue
		}
		rows = append(rows, Row{Num: num, Owner: owner, Repo: name, N: n, Line: line})
	}
	return rows, bad, sc.Err()
}

// PostRow posts one row and returns PostMeasured's exit code; its receipt
// goes to stdout and any refusal to stderr.
type PostRow func(row Row, stdout, stderr io.Writer) int

// PostFile posts every row of r through post, prefixing each receipt and
// refusal with ROW <i>, then prints the tally. name is the file named on the
// tally line.
func PostFile(r io.Reader, name string, post PostRow, stdout, stderr io.Writer) int {
	rows, bad, err := ParseRows(r)
	if err != nil {
		fmt.Fprintf(stderr, "READ POST FILE REFUSED file=%s why=%v\n", name, err)
		return 2
	}
	code, posted, calls := 0, 0, 0
	for _, b := range bad {
		fmt.Fprintln(stderr, b)
		code = 1
	}
	for _, row := range rows {
		var out, errb bytes.Buffer
		rc := post(row, &out, &errb)
		prefix := "ROW " + strconv.Itoa(row.Num) + " "
		for _, l := range lines(out.String()) {
			fmt.Fprintln(stdout, prefix+l)
			if strings.Contains(l, " github_calls=1") {
				calls++
			}
		}
		for _, l := range lines(errb.String()) {
			fmt.Fprintln(stderr, prefix+l)
		}
		if rc == 0 && out.Len() == 0 {
			// A post that prints no receipt is never counted as posted.
			fmt.Fprintf(stderr, "%sREAD POST REFUSED repo=%s n=%s why=the post printed no receipt\n", prefix, row.Repo, row.N)
			rc = 1
		}
		switch {
		case rc == 0:
			posted++
		case rc > code:
			code = rc
		}
	}
	total := len(rows) + len(bad)
	fmt.Fprintf(stdout, "READ POST FILE file=%s rows=%d posted=%d refused=%d github_calls=%d\n", name, total, posted, total-posted, calls)
	return code
}

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
