package request

import (
	"errors"
	"strings"
	"testing"
)

const admitDoc = `{
  "schema": 1,
  "operation": "admit",
  "table": "work",
  "epoch": "3",
  "expected_table_revision": "12",
  "operation_id": "op-17",
  "actor": "coordinator",
  "admissions": [
    {"id":"c1","digest":"` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `","object_id":"cccccccccccccccccccccccccccccccccccccccc","commit":"cccccccccccccccccccccccccccccccccccccccc","repository":"example.org/team/repo","path":"cards/c1.md","row":"build"}
  ]
}`

const eventsDoc = `{"schema":1,"operation":"apply_events","table":"work","epoch":"3","expected_table_revision":"12","operation_id":"op-17","actor":"coordinator",
"events":[{"id":"c1","type":"start","expect":{"revision":"2","place":{"row":"build","col":"ready"}},"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","issuer":"coord","source":"start/1"}]}`

func TestParseValidDocument(t *testing.T) {
	t.Parallel()
	r, err := Parse([]byte(admitDoc))
	if err != nil {
		t.Fatal(err)
	}
	if r.Operation != OpAdmit || len(r.Admissions) != 1 || r.Admissions[0].ID != "c1" || r.Epoch != "3" {
		t.Fatalf("request = %+v", r)
	}
	if _, err := Parse([]byte(admitDoc + "\n\n  \n")); err != nil {
		t.Fatalf("trailing whitespace refused: %v", err)
	}
}

// parseRefusals parses doc and returns the refusal triples.
func parseRefusals(t *testing.T, doc string) []string {
	t.Helper()
	r, err := Parse([]byte(doc))
	if err == nil {
		t.Fatalf("document accepted: %+v", r)
	}
	if r != nil {
		t.Fatalf("a refused document returned a request: %+v", r)
	}
	var rs *Refusals
	if !errors.As(err, &rs) {
		t.Fatalf("error %T is not *Refusals", err)
	}
	return triples(err)
}

func TestParseRefusals(t *testing.T) {
	t.Parallel()
	events := func(mid string) string {
		return `{"schema":1,"operation":"apply_events","table":"work","epoch":"3","expected_table_revision":"12","operation_id":"op-17","actor":"coordinator","events":[` + mid + `]}`
	}
	ev := `"id":"c1","type":"start","expect":{"revision":"2","place":{"row":"build","col":"ready"}},"digest":"` + dig + `","issuer":"coord","source":"start/1"`
	cases := []struct {
		name string
		doc  string
		want []string
	}{
		{"duplicate top-level key", strings.Replace(admitDoc, `"table": "work",`, `"table": "work", "table": "other",`, 1), wantTriples("-1|table|duplicate-key")},
		{"duplicate schema", strings.Replace(admitDoc, `"schema": 1,`, `"schema": 1, "schema": 1,`, 1), wantTriples("-1|schema|duplicate-key")},
		{"duplicate key in entry", events(`{` + ev + `,"id":"c2"}`), wantTriples("0|id|duplicate-key")},
		{"duplicate key deep in entry", events(`{"id":"c1","type":"start","expect":{"revision":"2","place":{"row":"a","row":"b","col":"ready"}},"digest":"` + dig + `","issuer":"coord","source":"s"}`), wantTriples("0|expect.place.row|duplicate-key")},
		{"duplicate key with different case is unknown", events(`{` + ev + `,"ID":"c2"}`), wantTriples("0|ID|unknown-field")},
		{"trailing object", admitDoc + ` {}`, wantTriples("-1||trailing-data")},
		{"trailing garbage", admitDoc + ` x`, wantTriples("-1||trailing-data")},
		{"trailing second document", admitDoc + admitDoc, wantTriples("-1||trailing-data")},
		{"trailing bracket", admitDoc + `]`, wantTriples("-1||trailing-data")},
		{"unknown envelope field", strings.Replace(admitDoc, `"actor": "coordinator",`, `"actor": "coordinator", "extra": 1,`, 1), wantTriples("-1|extra|unknown-field")},
		{"destination is not a field", events(`{` + ev + `,"destination":"working"}`), wantTriples("0|destination|unknown-field")},
		{"to is not a field", events(`{` + ev + `,"to":"working"}`), wantTriples("0|to|unknown-field")},
		{"unknown nested field", events(`{"id":"c1","type":"start","expect":{"revision":"2","place":{"row":"a","col":"ready","x":1}},"digest":"` + dig + `","issuer":"coord","source":"s"}`), wantTriples("0|expect.place.x|unknown-field")},
		{"several unknown fields sorted", events(`{` + ev + `,"zeta":1,"alpha":2}`), wantTriples("0|alpha|unknown-field", "0|zeta|unknown-field")},
		{"float schema", strings.Replace(admitDoc, `"schema": 1,`, `"schema": 1.0,`, 1), wantTriples("-1|schema|wrong-type")},
		{"exponent schema", strings.Replace(admitDoc, `"schema": 1,`, `"schema": 1e0,`, 1), wantTriples("-1|schema|wrong-type")},
		{"string schema", strings.Replace(admitDoc, `"schema": 1,`, `"schema": "1",`, 1), wantTriples("-1|schema|wrong-type")},
		{"number epoch", strings.Replace(admitDoc, `"epoch": "3",`, `"epoch": 3,`, 1), wantTriples("-1|epoch|wrong-type")},
		{"float epoch", strings.Replace(admitDoc, `"epoch": "3",`, `"epoch": 3.5,`, 1), wantTriples("-1|epoch|wrong-type")},
		{"null revision", strings.Replace(admitDoc, `"expected_table_revision": "12",`, `"expected_table_revision": null,`, 1), wantTriples("-1|expected_table_revision|wrong-type")},
		{"bool table", strings.Replace(admitDoc, `"table": "work",`, `"table": true,`, 1), wantTriples("-1|table|wrong-type")},
		{"object actor", strings.Replace(admitDoc, `"actor": "coordinator",`, `"actor": {},`, 1), wantTriples("-1|actor|wrong-type")},
		{"entry is a string", events(`"c1"`), wantTriples("0||wrong-type")},
		{"root is an array", `[1,2]`, wantTriples("-1||wrong-type")},
		{"root is a string", `"x"`, wantTriples("-1||wrong-type")},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := parseRefusals(t, c.doc)
			if !equalStrings(got, c.want) {
				t.Fatalf("refusals\n got  %v\n want %v", got, c.want)
			}
		})
	}
}

func TestParseRefusalDetails(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, doc string
		want      []string
	}{
		{"expect not an object", strings.Replace(eventsDoc, `"expect":{"revision":"2","place":{"row":"build","col":"ready"}}`, `"expect":"x"`, 1),
			wantTriples("0|expect|wrong-type")},
		{"bound as float", `{"schema":1,"operation":"resolve","table":"w","epoch":"1","expected_table_revision":"1","operation_id":"o","actor":"a","scope":{"row":"r","bound":1e2}}`,
			wantTriples("-1|scope.bound|wrong-type")},
		{"missing operation", `{"schema":1,"table":"w"}`, wantTriples("-1|operation|required", "-1|epoch|required", "-1|expected_table_revision|required", "-1|operation_id|required", "-1|actor|required")},
		{"operation not a string", `{"schema":1,"operation":7,"table":"w"}`, wantTriples("-1|operation|wrong-type", "-1|epoch|required", "-1|expected_table_revision|required", "-1|operation_id|required", "-1|actor|required")},
		{"payload of the wrong operation", strings.Replace(admitDoc, `"admissions"`, `"events"`, 1), wantTriples("-1|events|not-applicable", "-1|admissions|required")},
		{"admissions is an object", `{"schema":1,"operation":"admit","table":"w","epoch":"1","expected_table_revision":"1","operation_id":"o","actor":"a","admissions":{}}`, wantTriples("-1|admissions|wrong-type")},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := parseRefusals(t, c.doc); !equalStrings(got, c.want) {
				t.Fatalf("refusals\n got  %v\n want %v", got, c.want)
			}
		})
	}
}

func TestParseReportsEveryFaultTogether(t *testing.T) {
	t.Parallel()
	doc := `{"schema":1,"schema":1,"operation":"apply_events","table":"work","epoch":3,"expected_table_revision":"12","operation_id":"op-17","actor":"c","extra":true,
"events":[{"id":"c1","type":"start","expect":{"revision":"2","place":{"row":"build","col":"working"}},"digest":"` + dig + `","issuer":"i","source":"s","to":"x"},
{"id":"c1","type":"start","expect":{"revision":"2","place":{"row":"build","col":"ready"}},"digest":"` + dig + `","issuer":"i","source":"s"}]} x`
	got := parseRefusals(t, doc)
	want := wantTriples("-1|schema|duplicate-key", "-1|epoch|wrong-type", "-1|extra|unknown-field", "0|to|unknown-field",
		"0|expect.place.col|no-transition", "1|expect.place.col|event-chain", "-1||trailing-data")
	if !equalStrings(got, want) {
		t.Fatalf("refusals\n got  %v\n want %v", got, want)
	}
}

func TestParseSyntaxAndBoundsRefuse(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat("[", 20) + strings.Repeat("]", 20)
	for _, c := range []struct {
		name  string
		input []byte
		cause Cause
	}{
		{"nil", nil, CauseSyntax},
		{"empty", []byte(""), CauseSyntax},
		{"spaces", []byte("   "), CauseSyntax},
		{"garbage", []byte("not json"), CauseSyntax},
		{"truncated", []byte(admitDoc[:len(admitDoc)/2]), CauseSyntax},
		{"missing colon", []byte(`{"a" 1}`), CauseSyntax},
		{"trailing comma", []byte(`{"a":1,}`), CauseSyntax},
		{"single quotes", []byte(`{'a':1}`), CauseSyntax},
		{"raw newline in string", []byte("{\"schema\":1,\"table\":\"a\nb\"}"), CauseSyntax},
		{"raw control in string", []byte("{\"schema\":1,\"table\":\"a\x01b\"}"), CauseSyntax},
		{"bom", []byte("\xef\xbb\xbf" + admitDoc), CauseSyntax},
		{"invalid utf8 in string", []byte("{\"table\":\"a\xffb\"}"), CauseInvalidUTF8},
		{"invalid utf8 outside string", []byte("\xff{}"), CauseInvalidUTF8},
		{"overlong utf8", []byte("{\"table\":\"\xc0\xaf\"}"), CauseInvalidUTF8},
		{"lone surrogate bytes", []byte("{\"table\":\"\xed\xa0\x80\"}"), CauseInvalidUTF8},
		{"too deep", []byte(`{"schema":` + deep + `}`), CauseTooDeep},
		{"too deep root", []byte(strings.Repeat("[", 100000)), CauseTooDeep},
		{"over the input bound", []byte(`{"a":"` + strings.Repeat("x", MaxInputBytes) + `"}`), CauseTooLarge},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r, err := Parse(c.input)
			if r != nil || err == nil {
				t.Fatalf("accepted: %v %v", r, err)
			}
			rs := err.(*Refusals)
			if rs.List[0].Cause != c.cause {
				t.Fatalf("first refusal %+v, want cause %s", rs.List[0], c.cause)
			}
		})
	}
}

func TestParseRefusesControlAndReplacementCharactersInValues(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, table string
		cause       Cause
	}{
		{"escaped newline", `wo\nrk`, CauseControlChar},
		{"escaped nul", `wo\u0000rk`, CauseControlChar},
		{"escaped delete", `wo\u007frk`, CauseControlChar},
		{"escaped C1", `wo\u0085rk`, CauseControlChar},
		{"lone high surrogate escape", `wo\ud800rk`, CauseInvalidUTF8},
		{"lone low surrogate escape", `wo\udc00rk`, CauseInvalidUTF8},
		{"escaped replacement char", `wo�rk`, CauseInvalidUTF8},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			doc := strings.Replace(admitDoc, `"table": "work"`, `"table": "`+c.table+`"`, 1)
			got := parseRefusals(t, doc)
			if want := wantTriples("-1|table|" + string(c.cause)); !equalStrings(got, want) {
				t.Fatalf("refusals %v, want %v", got, want)
			}
		})
	}
}

func TestParseKeepsHTMLCharactersUnescaped(t *testing.T) {
	t.Parallel()
	doc := strings.Replace(eventsDoc, `"type":"start"`, `"type":"verdict-retry"`, 1)
	doc = strings.Replace(doc, `"col":"ready"`, `"col":"review"`, 1)
	doc = strings.Replace(doc, `"source":"start/1"`, `"source":"start/1","reason":"a<b> & \"c\" <"`, 1)
	r := mustParse(t, doc)
	if r.Events[0].Reason != `a<b> & "c" <` {
		t.Fatalf("reason %q", r.Events[0].Reason)
	}
	b := string(Canonical(r))
	if !strings.Contains(b, `"reason":"a<b> & \"c\" <"`) || strings.Contains(b, `\u003`) {
		t.Fatalf("canonical escapes HTML: %s", b)
	}
}

func TestParseErrorRendersOneLinePerRefusal(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(strings.Replace(admitDoc, `"table": "work",`, `"table": "work", "table": "x", "zz": 1,`, 1)))
	rs := err.(*Refusals)
	for _, l := range rs.Lines() {
		if strings.Contains(l, "\n") || !strings.HasPrefix(l, "refused admit") {
			t.Fatalf("line %q", l)
		}
	}
	if !strings.Contains(err.Error(), "and 1 more") {
		t.Fatalf("error %q", err.Error())
	}
	if (&Refusals{}).Error() == "" || (*Refusals)(nil).Error() == "" {
		t.Fatal("empty Refusals renders nothing")
	}
	if (*Refusals)(nil).Has(0, "", CauseSyntax) {
		t.Fatal("nil Refusals has a refusal")
	}
}
