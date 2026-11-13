package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"
)

const rentedHelp = `nova-fleet rented [--json]

The machines rented by the hour, from the record (nova-config machine rows whose provider is not
owned), so none bills unseen: one line per machine,
  RENTED <name> provider=<latitude|aws> id=<provider id|-> market=<on-demand|spot|-> age=<h>h<m>m
         rate=<usd an hour|-> price_from=<provider|recalled|-> cost=<usd so far|unknown> return_by=<time|->
         [PAST] purpose="<text>"
then RENTED TOTAL machines=<n> rate=<usd an hour> cost=<usd> unknown=<n> past=<n>. cost is the hourly
price times the hours since rented_at (a stopped box bills less: its disk only); unknown, when either is
missing, and then not in the total, which counts it in unknown; price_from=recalled is a price not read
from the provider; PAST marks a machine kept beyond its return_by. A nova-config on PATH older than the
rented fields is refused, never read as a fleet with none rented. --json prints the same as
one object. Reads the store through nova-config, so run it as the plays run:
  nova-secrets exec --only NOVA_PG_PASSWORD -- nova-fleet rented
Exit: 0 listed (also when none is rented); 2 could not read the record.
`

// now is the clock of the rented verbs; unit tests replace it.
var now = time.Now

// rentedRow is a machine row of the record as nova-config machine list --json prints its fields.
type rentedRow struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	Public      string `json:"public"`
	User        string `json:"user"`
	OS          string `json:"os"`
	Cores       *int   `json:"cores"`
	Scopes      string `json:"runner_scopes"`
	WinScopes   string `json:"windows_runners"`
	Provider    string `json:"provider"`
	ProviderID  string `json:"provider_id"`
	Market      string `json:"market"`
	SpotRequest string `json:"spot_request"`
	Price       string `json:"price"`
	PriceFrom   string `json:"price_from"`
	Purpose     string `json:"purpose"`
	RentedBy    string `json:"rented_by"`
	RentedAt    string `json:"rented_at"`
	ReturnBy    string `json:"return_by"`
}

func (r rentedRow) rented() bool { return r.Provider != "owned" }

// cmdFunc runs a program to its end: its stdout, stderr and exit code (outCmd).
type cmdFunc func(bin string, args, env []string) (string, string, int)

// record reads the machine rows through nova-config at cfg, with the caller's environment. A nova-config that
// prints no provider predates the rented fields: its rows cannot say which machines are rented, so the read is refused
// rather than taken for a fleet with none.
func record(cfg string, run cmdFunc) ([]rentedRow, error) {
	out, why, c := run(cfg, []string{"machine", "list", "--json"}, os.Environ())
	if c != 0 {
		return nil, fmt.Errorf("nova-config machine list --json exit %d: %s", c, cmp.Or(oneLine(why), "(nothing on stderr)"))
	}
	var v struct {
		Items []struct{ Fields json.RawMessage }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("nova-config machine list --json printed no JSON (%v)", err)
	}
	var rows []rentedRow
	for _, it := range v.Items {
		var r rentedRow
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(it.Fields, &r); err != nil {
			return nil, fmt.Errorf("nova-config machine list --json: a row that does not read (%v)", err)
		}
		if json.Unmarshal(it.Fields, &keys) != nil || keys["provider"] == nil {
			return nil, fmt.Errorf("%s predates the rented fields (row %s has no provider), so it cannot say which machines are rented; install this release's nova-config", cfg, r.Name)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// bill is what a rented machine has cost by at: its hourly price times the hours since rented_at. Known is false
// when either is missing, and then the cost is not counted.
type bill struct {
	Name, Provider, ProviderID, Market, Price, PriceFrom, Purpose, ReturnBy string
	Hours, Rate, Cost                                                       float64
	Known, Past                                                             bool
}

func billOf(r rentedRow, at time.Time) bill {
	b := bill{Name: r.Name, Provider: r.Provider, ProviderID: r.ProviderID, Market: r.Market, Price: r.Price, PriceFrom: r.PriceFrom, Purpose: r.Purpose, ReturnBy: r.ReturnBy}
	t, terr := time.Parse(time.RFC3339, r.RentedAt)
	p, perr := strconv.ParseFloat(r.Price, 64)
	if terr == nil {
		b.Hours = max(at.Sub(t).Hours(), 0)
	}
	if perr == nil {
		b.Rate = p
	}
	if b.Known = terr == nil && perr == nil; b.Known {
		b.Cost = p * b.Hours
	}
	if t, err := time.Parse(time.RFC3339, r.ReturnBy); err == nil && at.After(t) {
		b.Past = true
	}
	return b
}

func dash(s string) string { return cmp.Or(s, "-") }

func usd(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

// rate is dollars an hour to the record's four places, without trailing zeros.
func rate(f float64) string { return strconv.FormatFloat(math.Round(f*1e4)/1e4, 'f', -1, 64) }

// listRented prints the rented rows of rows as at, as lines or one JSON object.
func listRented(rows []rentedRow, at time.Time, asJSON bool, w io.Writer) error {
	bills := []bill{}
	var total struct {
		Machines, Unknown, Past int
		Rate, Cost              float64
	}
	for _, r := range rows {
		if !r.rented() {
			continue
		}
		b := billOf(r, at)
		bills = append(bills, b)
		total.Machines++
		total.Rate += b.Rate
		total.Cost += b.Cost
		if !b.Known {
			total.Unknown++
		}
		if b.Past {
			total.Past++
		}
	}
	if asJSON {
		out, err := json.MarshalIndent(map[string]any{"machines": bills, "total": total}, "", "  ")
		if err == nil {
			_, err = fmt.Fprintf(w, "%s\n", out)
		}
		return err
	}
	for _, b := range bills {
		cost, past := "unknown", ""
		if b.Known {
			cost = usd(b.Cost)
		}
		if b.Past {
			past = " PAST"
		}
		h := int(b.Hours * 60)
		purpose, _ := json.Marshal(b.Purpose) // ignored: a string always encodes
		fmt.Fprintf(w, "RENTED %s provider=%s id=%s market=%s age=%dh%02dm rate=%s price_from=%s cost=%s return_by=%s%s purpose=%s\n",
			b.Name, b.Provider, dash(b.ProviderID), dash(b.Market), h/60, h%60, dash(b.Price), dash(b.PriceFrom), cost, dash(b.ReturnBy), past, purpose)
	}
	_, err := fmt.Fprintf(w, "RENTED TOTAL machines=%d rate=%s cost=%s unknown=%d past=%d\n", total.Machines, rate(total.Rate), usd(total.Cost), total.Unknown, total.Past)
	return err
}

func runRented(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	for _, a := range args {
		switch a {
		case "-h", "--help":
			fmt.Fprint(stdout, rentedHelp)
			return 0
		case "--json":
			asJSON = true
		default:
			fmt.Fprintf(stderr, "nova-fleet rented: unexpected argument %s; run: nova-fleet rented -h\n", a)
			return 2
		}
	}
	cfg, err := lookPath("nova-config")
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet rented: nova-config is not on PATH (%v); run: nova-fleet check\n", err)
		return 2
	}
	rows, err := record(cfg, outCmd)
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet rented: the record cannot be read: %v; with the store's password in the environment (nova-secrets exec --only NOVA_PG_PASSWORD -- nova-fleet rented); run: nova-config machine list\n", err)
		return 2
	}
	if err := listRented(rows, now().UTC(), asJSON, stdout); err != nil {
		fmt.Fprintf(stderr, "nova-fleet rented: write the list: %v; run: nova-fleet rented > <a file>\n", err)
		return 2
	}
	return 0
}
