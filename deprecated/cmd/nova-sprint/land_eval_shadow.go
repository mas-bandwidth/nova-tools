package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// landEvalStdin is where land eval --shadow reads the SHADOW lines; nil is
// os.Stdin (tests set it).
var landEvalStdin io.Reader

// shadowPR is one PR's tally over a window of SHADOW lines.
type shadowPR struct {
	repo                   string
	n                      int
	agree, disagree, stale int
	heads                  []string // the SHADOW head of each line, in order
	verdicts               []string
}

// runLandEvalShadow is land eval --shadow (nova-tools#3800): it reads the
// SHADOW lines nova-sprint lander --shadow printed over a window (stdin) and
// compares each verdict with what the hand lander did, read from the PR
// record pr:<name>:<n> (its close field is the CLOSE receipt, state parked
// is a PARKED move) in one pipeline. A line agrees when shadow LAND and hand
// landed coincide (both or neither); a line whose head is not the record's
// head is stale and counts neither way. It prints one EVAL line per PR and
// one receipt, and writes nothing.
//
//	nova-sprint lander --shadow --redis <addr> --repo <r> <n>... | nova-sprint land eval --shadow --redis <addr>
func runLandEvalShadow(ctx context.Context, addr string, in io.Reader, out, errOut io.Writer) int {
	byKey := map[string]*shadowPR{}
	var order []*shadowPR
	lines, skipped := 0, 0
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		repo, n, head, verdict, ok := parseShadowLine(sc.Text())
		if !ok {
			if strings.TrimSpace(sc.Text()) != "" {
				skipped++
			}
			continue
		}
		lines++
		k := prkey.Key(repo, n)
		p := byKey[k]
		if p == nil {
			p = &shadowPR{repo: prkey.Name(repo), n: n}
			byKey[k] = p
			order = append(order, p)
		}
		p.heads = append(p.heads, head)
		p.verdicts = append(p.verdicts, verdict)
	}
	if err := sc.Err(); err != nil {
		return refuse(errOut, "land eval", "read SHADOW lines: "+err.Error())
	}
	if lines == 0 {
		return refuse(errOut, "land eval", "no SHADOW lines on stdin; pipe nova-sprint lander --shadow into land eval --shadow")
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].repo != order[j].repo {
			return order[i].repo < order[j].repo
		}
		return order[i].n < order[j].n
	})

	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint land eval: connect redis: %v\n", err)
		return 6
	}
	defer st.Close()
	pipe := st.Client().Pipeline()
	cmds := make([]*redis.SliceCmd, len(order))
	for i, p := range order {
		cmds[i] = pipe.HMGet(ctx, prkey.Key(p.repo, p.n), "head", "state", "close")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		if storeDown(errOut, "land eval", err) {
			return 6
		}
		return refuse(errOut, "land eval", "read PR records: "+err.Error())
	}

	var agree, disagree, stale int
	for i, p := range order {
		v := cmds[i].Val()
		recHead, state, closeLine := hmStr(v, 0), hmStr(v, 1), hmStr(v, 2)
		hand := "none"
		switch {
		case closeLine != "" || state == "landed" || state == "merged":
			hand = "landed"
		case state == "parked":
			hand = "parked"
		case recHead != "" || state != "":
			hand = "open"
		}
		for j, head := range p.heads {
			if head != "" && head != "-" && recHead != "" && !strings.HasPrefix(recHead, head) {
				p.stale++
				continue
			}
			if (p.verdicts[j] == "LAND") == (hand == "landed") {
				p.agree++
			} else {
				p.disagree++
			}
		}
		agree, disagree, stale = agree+p.agree, disagree+p.disagree, stale+p.stale
		fmt.Fprintf(out, "EVAL %s#%d hand=%s agree=%d disagree=%d stale=%d\n",
			oneline.Field(p.repo), p.n, hand, p.agree, p.disagree, p.stale)
	}
	fmt.Fprintf(out, "EVAL shadow prs=%d lines=%d agree=%d disagree=%d stale=%d skipped=%d\n",
		len(order), lines, agree, disagree, stale, skipped)
	return 0
}

// parseShadowLine reads `SHADOW <repo>#<n> head=<h8> verdict=<V> why=<gate>`.
func parseShadowLine(line string) (repo string, n int, head, verdict string, ok bool) {
	f := strings.Fields(line)
	if len(f) < 3 || f[0] != "SHADOW" {
		return "", 0, "", "", false
	}
	r, num, found := strings.Cut(f[1], "#")
	if !found || r == "" {
		return "", 0, "", "", false
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return "", 0, "", "", false
	}
	if _, _, err := prkey.Split(r); err != nil {
		return "", 0, "", "", false
	}
	for _, kv := range f[2:] {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "head":
			head = v
		case "verdict":
			verdict = v
		}
	}
	if verdict == "" {
		return "", 0, "", "", false
	}
	return r, n, head, verdict, true
}

func hmStr(v []interface{}, i int) string {
	if i >= len(v) || v[i] == nil {
		return ""
	}
	s, _ := v[i].(string)
	return s
}

func landEvalInput() io.Reader {
	if landEvalStdin != nil {
		return landEvalStdin
	}
	return os.Stdin
}
