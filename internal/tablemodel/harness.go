package tablemodel

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The linear TLC harness. A trace of observed store states becomes a TLA+
// module that extends MCEpochMemberTable and lets the model take exactly the
// recorded action at each step, then requires the model's variables to equal
// the observed state after it. TLC either walks the whole trace or names the
// step where the model and the store part.

func quote(s string) string { return strconv.Quote(s) }

func (p Phys) tla() string { return "<<" + quote(p.Table) + "," + strconv.Itoa(p.Epoch) + ">>" }

func (c Cell) tla() string { return "<<" + c.P.tla() + "," + quote(c.Row) + "," + quote(c.Col) + ">>" }

func set(items []string) string { return "{" + strings.Join(items, ",") + "}" }

// fun renders a function built with :> and @@.
func fun(keys, values []string) string {
	parts := make([]string, len(keys))
	for i := range keys {
		parts[i] = "(" + keys[i] + " :> " + values[i] + ")"
	}
	return "(" + strings.Join(parts, " @@ ") + ")"
}

// tla renders a state as the tuple the harness compares with the model's
// variables: live, rows, binds, data, place, activeEpoch, seenEpoch.
func (s State) tla() string {
	var live, rows, binds, data []string
	for _, p := range s.Live {
		live = append(live, p.tla())
	}
	for _, r := range s.Rows {
		rows = append(rows, "<<"+r.P.tla()+","+quote(r.Row)+">>")
	}
	for _, b := range s.Binds {
		binds = append(binds, "<<"+b.Cell.tla()+","+b.Target.tla()+">>")
	}
	for _, d := range s.Data {
		data = append(data, "<<"+d.Cell.tla()+","+quote(d.Member)+","+strconv.Itoa(d.Score)+">>")
	}
	var placeKeys, placeValues []string
	for _, p := range s.Place {
		var lk, lv []string
		for _, l := range p.Locations {
			lk = append(lk, l.P.tla())
			lv = append(lv, l.Cell.tla())
		}
		placeKeys = append(placeKeys, quote(p.Member))
		placeValues = append(placeValues, fun(lk, lv))
	}
	var seenKeys, seenValues []string
	for _, w := range writers {
		seenKeys = append(seenKeys, quote(w))
		seenValues = append(seenValues, strconv.Itoa(s.Seen[w]))
	}
	return "<<" + strings.Join([]string{set(live), set(rows), set(binds), set(data),
		fun(placeKeys, placeValues), strconv.Itoa(s.Active), fun(seenKeys, seenValues)}, ",") + ">>"
}

var bindTargetRE = regexp.MustCompile(`^table:(t[12]):([12]):cell:(r[12]):(c[123])$`)

// target maps a binding's key to the model's cell: the external set, or a cell
// of another physical table.
func target(key string) (Cell, error) {
	if key == "external" {
		return External, nil
	}
	m := bindTargetRE.FindStringSubmatch(key)
	if m == nil {
		return Cell{}, fmt.Errorf("unmapped binding: %s", key)
	}
	epoch, _ := strconv.Atoi(m[2])
	return mkCell(m[1], epoch, m[3], m[4]), nil
}

// ActionTLA renders a step as the model action that performs it at the given
// epoch of the step's table.
func ActionTLA(a Action, epoch int) (string, error) {
	w := quote(a.Actor)
	switch a.Verb {
	case "advance":
		return "Advance(" + w + ")", nil
	case "read_epoch":
		return "ReadEpoch(" + w + ")", nil
	}
	if len(a.Args) == 0 {
		return "", fmt.Errorf("verb %s has no table argument", a.Verb)
	}
	table := Phys{a.Args[0], epoch}
	at := func(i int) string { return quote(a.Args[i]) }
	switch a.Verb {
	case "clear", "drop", "create":
		return "Epoch" + strings.ToUpper(a.Verb[:1]) + a.Verb[1:] + "(" + w + "," + table.tla() + ")", nil
	case "row_del":
		return "EpochRowDelete(" + w + "," + table.tla() + "," + at(1) + ")", nil
	case "row_add":
		var opts struct {
			Binds map[string]string `json:"binds"`
		}
		if err := json.Unmarshal([]byte(a.Args[2]), &opts); err != nil {
			return "", fmt.Errorf("row_add options: %v", err)
		}
		var nb []string
		for _, col := range sortedKeys(opts.Binds) {
			t, err := target(opts.Binds[col])
			if err != nil {
				return "", err
			}
			nb = append(nb, "<<"+mkCell(a.Args[0], epoch, a.Args[1], col).tla()+","+t.tla()+">>")
		}
		return "EpochRowAdd(" + w + "," + table.tla() + "," + at(1) + "," + set(nb) + ")", nil
	case "bind":
		var spec struct {
			Rows []struct {
				Key   string            `json:"key"`
				Binds map[string]string `json:"binds"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(a.Args[1]), &spec); err != nil {
			return "", fmt.Errorf("bind rows: %v", err)
		}
		var keys, nb []string
		for _, row := range spec.Rows {
			keys = append(keys, quote(row.Key))
			for _, col := range sortedKeys(row.Binds) {
				t, err := target(row.Binds[col])
				if err != nil {
					return "", err
				}
				nb = append(nb, "<<"+mkCell(a.Args[0], epoch, row.Key, col).tla()+","+t.tla()+">>")
			}
		}
		return "EpochBind(" + w + "," + table.tla() + "," + set(keys) + "," + set(nb) + ")", nil
	}
	if len(a.Args) < 4 {
		return "", fmt.Errorf("verb %s has too few arguments", a.Verb)
	}
	c := mkCell(a.Args[0], epoch, a.Args[1], a.Args[2]).tla()
	switch a.Verb {
	case "cell_add":
		return "EpochAdd(" + strings.Join([]string{w, c, at(4), a.Args[3]}, ",") + ")", nil
	case "cell_remove":
		return "EpochRemove(" + strings.Join([]string{w, c, at(3)}, ",") + ")", nil
	case "cell_move":
		dst := mkCell(a.Args[0], epoch, a.Args[1], a.Args[3]).tla()
		return "EpochMove(" + strings.Join([]string{w, c, dst, at(4)}, ",") + ")", nil
	}
	return "", fmt.Errorf("unmapped verb: %s", a.Verb)
}

// HarnessName is the module and configuration the harness is written as.
const HarnessName = "MemberReceiptReplay"

// HarnessModule renders the linear harness. observed holds the initial state
// then the state after each step; steps holds each step's model action.
func HarnessModule(observed []State, steps []string) string {
	var cases []string
	for i, step := range steps {
		lead := "  [] "
		if i == 0 {
			lead = "CASE "
		}
		cases = append(cases, lead+"step="+strconv.Itoa(i)+" -> "+step)
	}
	cases = append(cases, "  [] OTHER -> UNCHANGED xvars")
	var obs []string
	for _, s := range observed {
		obs = append(obs, s.tla())
	}
	return "---------------- MODULE " + HarnessName + " ----------------\n" +
		"EXTENDS MCEpochMemberTable, TLC, Sequences\n" +
		"Observed == <<\n" + strings.Join(obs, ",\n") + "\n>>\n" +
		"ActualState == <<live,rows,binds,data,place,activeEpoch,seenEpoch>>\n" +
		"MatchesExecution == ActualState=Observed[step+1]\n" +
		"ReplayNext ==\n" + strings.Join(cases, "\n") + "\n" +
		"ReplaySpec == EpochInit /\\ [][ReplayNext]_xvars\n" +
		"=================================================================\n"
}

// HarnessConfig derives the harness configuration from the model's own:
// the replay specification, as many steps as the trace has, and the invariant
// that the model matches the observation.
func HarnessConfig(modelConfig string, steps int) string {
	cfg := strings.ReplaceAll(modelConfig, "SPECIFICATION EpochSpec", "SPECIFICATION ReplaySpec")
	cfg = strings.ReplaceAll(cfg, "MaxSteps = 3", "MaxSteps = "+strconv.Itoa(steps))
	return cfg + "\nINVARIANT MatchesExecution\n"
}
