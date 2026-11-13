package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	config "github.com/mas-bandwidth/nova-tools/v2/modules/config"
)

// field is one column of a kind's table. typ is text, int, bool, list (a
// sorted comma list of words matching word), argv (a JSON array of strings),
// env (a JSON object of NAME to value), path (an absolute or ~/ path), ref
// (the name of a machine row, NULL when empty), public (a public IP literal
// or a DNS name), scopes (GitHub Actions runners per repository or organization,
// see canonicalScopes) or admins (Windows runners run as a local administrator, see canonicalAdmins).
type field struct {
	name, typ string
	required  bool
	def       string
	null      bool // an int that may be unset: the value "default" unsets it
	cut       bool // cut to 60 characters in list lines
	free      bool // free text: always printed as a JSON string
	word      *regexp.Regexp
}

type kind struct {
	name, table string
	fields      []field
	check       func(Row) error
}

// Row is one row: its name, its fields as text, and its stamps (RFC 3339).
type Row = config.Row

var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	roleWord    = regexp.MustCompile(`^(bench|runner|services)$`)
	keyWord     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tierWord    = regexp.MustCompile(`^(flash|pro|heavy|frontier)$`)
	// a machine is owned or rented by the hour from a provider; a rented one at AWS is on demand or spot
	providerWord  = regexp.MustCompile(`^(owned|latitude|aws)$`)
	marketWord    = regexp.MustCompile(`^(on-demand|spot)$`)
	priceFromWord = regexp.MustCompile(`^(provider|recalled)$`)
	priceText     = regexp.MustCompile(`^[0-9]{1,6}(\.[0-9]{1,4})?$`)
)

var kinds = map[string]*kind{
	"machine": {name: "machine", table: "machines", check: checkMachine,
		fields: []field{
			{name: "address", typ: "text"},
			{name: "public", typ: "public", null: true},
			{name: "user", typ: "text", required: true},
			{name: "os", typ: "text"},
			{name: "arch", typ: "text"},
			{name: "cores", typ: "int", null: true},
			{name: "roles", typ: "list", def: "bench", word: roleWord},
			{name: "runners", typ: "int"},
			{name: "runner_scopes", typ: "scopes"},
			{name: "windows_runners", typ: "scopes"},
			{name: "windows_admin_runners", typ: "admins"},
			{name: "runner_env", typ: "env", def: "{}"},
			{name: "note", typ: "text", cut: true, free: true},
			{name: "provider", typ: "word", def: "owned", word: providerWord},
			{name: "provider_id", typ: "text"},
			{name: "market", typ: "word", word: marketWord},
			{name: "spot_request", typ: "text"},
			{name: "price", typ: "price"},
			{name: "price_from", typ: "word", word: priceFromWord},
			{name: "purpose", typ: "text", cut: true, free: true},
			{name: "rented_by", typ: "text"},
			{name: "rented_at", typ: "time"},
			{name: "return_by", typ: "time"},
		}},
	"service": {name: "service", table: "services", check: checkService,
		fields: []field{
			{name: "machine", typ: "ref", required: true},
			{name: "argv", typ: "argv", required: true},
			{name: "seat", typ: "text"},
			{name: "keys", typ: "list", word: keyWord},
			{name: "every", typ: "int"},
			{name: "keepalive", typ: "bool"},
			{name: "enabled", typ: "bool", def: "true"},
			{name: "env", typ: "env", def: "{}"},
			{name: "log", typ: "path"},
		}},
	"friend": {name: "friend", table: "friends", check: checkFriend,
		fields: []field{
			{name: "harness", typ: "text", required: true},
			{name: "model", typ: "text"},
			{name: "width", typ: "int", def: "1"},
			{name: "tiers", typ: "list", def: "flash,frontier,heavy,pro", word: tierWord},
			{name: "pronouns", typ: "text", null: true},
			{name: "note", typ: "text", cut: true, free: true},
			{name: "enabled", typ: "bool", def: "true"},
		}},
}

// fieldsLine names a kind's fields on one line, for a usage error and for help.
func (k *kind) fieldsLine() string {
	names := []string{"name"}
	for _, f := range k.fields {
		names = append(names, f.name)
	}
	return k.name + " fields: " + strings.Join(names, ", ")
}

func (f field) canonical(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch f.typ {
	case "int":
		if f.null && raw == "default" {
			return "", nil
		}
		n, err := strconv.ParseInt(raw, 10, 32) // the columns are integer (int4)
		if err != nil || n < 0 {
			return "", fmt.Errorf("--%s %q: want a non-negative integer", f.name, raw)
		}
		return strconv.FormatInt(n, 10), nil
	case "bool":
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return "", fmt.Errorf("--%s %q: want true or false", f.name, raw)
		}
		return strconv.FormatBool(b), nil
	case "list":
		words := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
		sort.Strings(words)
		words = slices.Compact(words)
		for _, w := range words {
			if !f.word.MatchString(w) {
				return "", fmt.Errorf("--%s %q: want a comma list of words matching %s", f.name, w, f.word)
			}
		}
		return strings.Join(words, ","), nil
	case "argv":
		var words []string
		dec := json.NewDecoder(strings.NewReader(raw))
		if !strings.HasPrefix(raw, "[") || dec.Decode(&words) != nil || dec.More() || len(words) == 0 || words[0] == "" {
			return "", fmt.Errorf(`--%s: want the command as a JSON array of strings, the program first: '["/path/prog","--flag","v"]'`, f.name)
		}
		return marshal(words), nil
	case "env":
		return canonicalEnv(f, raw)
	case "path":
		if raw != "" && !strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "~/") {
			return "", fmt.Errorf("--%s %q: want an absolute path or one starting ~/", f.name, raw)
		}
	case "ref":
		if raw != "" && !namePattern.MatchString(raw) {
			return "", fmt.Errorf("--%s %q: want the name of a machine row", f.name, raw)
		}
	case "public":
		if raw != "" {
			return canonicalPublic(f, raw)
		}
	case "scopes":
		return canonicalScopes(f, raw)
	case "admins":
		return canonicalAdmins(f, raw)
	case "word":
		if raw != "" && !f.word.MatchString(raw) {
			return "", fmt.Errorf("--%s %q: want one of %s", f.name, raw, strings.Trim(f.word.String(), "^()$"))
		}
	case "price":
		if raw != "" && !priceText.MatchString(raw) {
			return "", fmt.Errorf("--%s %q: want US dollars per hour, such as 4.06", f.name, raw)
		}
	case "time":
		if raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return "", fmt.Errorf("--%s %q: want an RFC 3339 time, such as 2026-11-13T04:00:00Z", f.name, raw)
			}
			return t.UTC().Format(time.RFC3339), nil
		}
	}
	if strings.ContainsAny(raw, "\r\n") {
		return "", fmt.Errorf("--%s: want one line", f.name)
	}
	if raw == "" && f.required {
		return "", fmt.Errorf("--%s: want a value", f.name)
	}
	return raw, nil
}

// canonicalEnv is an env object as it is stored: keys sorted, one line; the
// names are POSIX names (envName), and every value must be a string.
func canonicalEnv(f field, raw string) (string, error) {
	if raw == "" {
		return "{}", nil
	}
	var m map[string]string
	dec := json.NewDecoder(strings.NewReader(raw))
	if !strings.HasPrefix(raw, "{") || dec.Decode(&m) != nil || dec.More() || m == nil {
		return "", fmt.Errorf(`--%s: want a JSON object of NAME to value strings: '{"NAME":"value"}'`, f.name)
	}
	for _, n := range slices.Sorted(maps.Keys(m)) {
		if !envName.MatchString(n) {
			return "", fmt.Errorf("--%s name %q: want letters, digits and underscores, not starting with a digit (%s)", f.name, n, envName)
		}
	}
	return marshal(m), nil
}

// scopeTarget is where a runner registers (a repository or an organization on github.com); scopeLabel one label.
var (
	scopeTarget = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)?$`)
	scopeLabel  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// runnerScope is one target of runner_scopes or windows_runners: count runners on url (a repository or an
// organization), and the labels added to the ones the role always gives. Its json is the inventory's
// nova_runner_scopes or nova_windows_runners entry.
type runnerScope struct {
	URL    string   `json:"url"`
	Count  int      `json:"count"`
	Labels []string `json:"labels"`
}

// parseScopes reads `<url>=<count>[:<label>+<label>...]`, comma-separated: the targets sorted by url, each url
// once, labels sorted and unique; empty is none.
func parseScopes(raw string) ([]runnerScope, error) {
	out := []runnerScope{}
	for _, e := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		rest, https := strings.CutPrefix(e, "https://")
		target, labels, colon := strings.Cut(rest, ":")
		u, n, eq := strings.Cut(target, "=")
		w := runnerScope{URL: "https://" + strings.TrimSuffix(u, "/"), Labels: []string{}}
		count, err := strconv.Atoi(n)
		if !https || !eq || !scopeTarget.MatchString(w.URL) || err != nil || count < 1 || count > 99 {
			return nil, fmt.Errorf("%q: want <url>=<count>[:<label>+<label>], the url https://github.com/<owner>[/<repo>], the count 1 to 99", e)
		}
		if w.Count = count; colon {
			w.Labels = strings.Split(labels, "+")
		}
		for _, l := range w.Labels {
			if !scopeLabel.MatchString(l) {
				return nil, fmt.Errorf("%q: label %q: want letters, digits, dot, dash and underscore", e, l)
			}
		}
		slices.Sort(w.Labels)
		w.Labels = slices.Compact(w.Labels)
		if slices.ContainsFunc(out, func(o runnerScope) bool { return strings.EqualFold(o.URL, w.URL) }) {
			return nil, fmt.Errorf("%s is named twice; give each url once", w.URL)
		}
		out = append(out, w)
	}
	slices.SortFunc(out, func(a, b runnerScope) int { return strings.Compare(a.URL, b.URL) })
	return out, nil
}

// canonicalScopes is a runner_scopes or windows_runners value as it is stored: parseScopes' targets, written back.
func canonicalScopes(f field, raw string) (string, error) {
	ws, err := parseScopes(raw)
	parts := []string{}
	for _, w := range ws {
		parts = append(parts, w.URL+"="+strconv.Itoa(w.Count)+strings.TrimSuffix(":"+strings.Join(w.Labels, "+"), ":"))
	}
	if err != nil {
		return "", fmt.Errorf("--%s %v", f.name, err)
	}
	return strings.Join(parts, ","), nil
}

// adminAccount is a Windows local user name an admin runner runs as (at most 20 characters, Windows' limit).
// builtinAccounts are Windows' own local users, never an admin runner's: the play would take one over.
// reservedLabels are labels GitHub gives runners for their OS and kind: a job selecting one would reach the admin runner.
var (
	adminAccount    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
	builtinAccounts = []string{"administrator", "guest", "defaultaccount", "wdagutilityaccount"}
	reservedLabels  = []string{"self-hosted", "windows", "x64", "linux", "macos"}
)

// adminRunner is one entry of windows_admin_runners: one Windows runner on url (a repository) run as the local
// administrator account, carrying only labels (GitHub adds self-hosted, Windows and X64), that runs only the one
// workflow File of the repository's main branch. Secret is the nova-secrets name of the account's password; Workflow
// is the GITHUB_WORKFLOW_REF its jobs must carry. Its json is the inventory's nova_windows_admin_runners entry.
type adminRunner struct {
	URL      string   `json:"url"`
	Account  string   `json:"account"`
	Labels   []string `json:"labels"`
	Secret   string   `json:"secret"`
	File     string   `json:"-"`
	Workflow string   `json:"workflow"`
}

// adminWorkflow is a workflow file name in the repository's .github/workflows.
var adminWorkflow = regexp.MustCompile(`^[A-Za-z0-9._-]+\.ya?ml$`)

// parseAdmins reads `<url>=<account>:<label>[+<label>...]@<workflow>`, comma-separated: sorted by url then account,
// each account and each runner directory (<owner>-<repo>-<account>) once, labels sorted and unique; empty is none. The
// url is a repository: an organization's runner is reachable from every repository in it, and this one runs jobs as an
// administrator. The workflow is the one file of .github/workflows whose jobs it runs, from main only.
func parseAdmins(raw string) ([]adminRunner, error) {
	out := []adminRunner{}
	for _, e := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		rest, https := strings.CutPrefix(e, "https://")
		u, tail, eq := strings.Cut(rest, "=")
		account, tail, colon := strings.Cut(tail, ":")
		labels, file, _ := strings.Cut(tail, "@") // no "@" leaves file empty, which adminWorkflow refuses
		w := adminRunner{URL: "https://" + strings.TrimSuffix(u, "/"), Account: account, Labels: strings.Split(labels, "+"), File: file}
		if !https || !eq || !colon || !scopeTarget.MatchString(w.URL) || strings.Count(w.URL, "/") != 4 || !adminAccount.MatchString(account) ||
			!adminWorkflow.MatchString(file) {
			return nil, fmt.Errorf("%q: want <url>=<account>:<label>[+<label>]@<workflow>, the url https://github.com/<owner>/<repo> (a repository, never an organization), the account a lower-case letter then at most 19 lower-case letters, digits and dashes, the workflow a file name in .github/workflows (<name>.yml)", e)
		}
		w.Workflow = strings.TrimPrefix(w.URL, "https://github.com/") + "/.github/workflows/" + file + "@refs/heads/main"
		if slices.Contains(builtinAccounts, account) {
			return nil, fmt.Errorf("%q: account %s is a Windows built-in account; give the runner its own account", e, account)
		}
		for _, l := range w.Labels {
			if !scopeLabel.MatchString(l) {
				return nil, fmt.Errorf("%q: label %q: want letters, digits, dot, dash and underscore", e, l)
			}
			if slices.Contains(reservedLabels, strings.ToLower(l)) {
				return nil, fmt.Errorf("%q: label %q is one GitHub gives runners (%s); give a label of the runner's own", e, l, strings.Join(reservedLabels, ", "))
			}
		}
		slices.Sort(w.Labels)
		w.Labels = slices.Compact(w.Labels)
		if slices.ContainsFunc(out, func(o adminRunner) bool { return o.Account == w.Account }) {
			return nil, fmt.Errorf("account %s is named twice; give each account one runner", w.Account)
		}
		if o := slices.IndexFunc(out, func(o adminRunner) bool { return strings.EqualFold(adminDir(o), adminDir(w)) }); o >= 0 {
			return nil, fmt.Errorf("%s and %s would share the runner directory %s; give one of them another account", out[o].URL+"="+out[o].Account, w.URL+"="+w.Account, adminDir(w))
		}
		w.Secret = "NOVA_WINRUNNER_" + strings.ToUpper(strings.ReplaceAll(account, "-", "_")) + "_PASSWORD"
		out = append(out, w)
	}
	slices.SortFunc(out, func(a, b adminRunner) int {
		return cmp.Or(strings.Compare(a.URL, b.URL), strings.Compare(a.Account, b.Account))
	})
	return out, nil
}

// adminDir is the name of an admin runner's directory on the machine (Windows compares it without case):
// <owner>-<repo>-<account>, which two entries can share (o/r-x=a and o/r=x-a).
func adminDir(w adminRunner) string {
	return strings.ReplaceAll(strings.TrimPrefix(w.URL, "https://github.com/"), "/", "-") + "-" + w.Account
}

// canonicalAdmins is a windows_admin_runners value as it is stored: parseAdmins' entries, written back.
func canonicalAdmins(f field, raw string) (string, error) {
	ws, err := parseAdmins(raw)
	if err != nil {
		return "", fmt.Errorf("--%s %v", f.name, err)
	}
	parts := []string{}
	for _, w := range ws {
		parts = append(parts, w.URL+"="+w.Account+":"+strings.Join(w.Labels, "+")+"@"+w.File)
	}
	return strings.Join(parts, ","), nil
}

// dnsLabel is one lower-case label of a DNS name (RFC 1123).
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// notPublic are the ranges a public address is never in (they are disjoint), each with its name.
var notPublic = map[string]string{"100.64.0.0/10": "the tailnet's CGNAT range", "10.0.0.0/8": "a private range",
	"172.16.0.0/12": "a private range", "192.168.0.0/16": "a private range", "fc00::/7": "a private range (unique local)",
	"127.0.0.0/8": "the loopback range", "::1/128": "the loopback address", "169.254.0.0/16": "the link-local range",
	"fe80::/10": "the link-local range", "0.0.0.0/8": "the unspecified range", "::/128": "the unspecified address"}

// canonicalPublic is a public address as it is stored: an IP literal in its
// canonical form, or a DNS name of lower-case labels (RFC 1123). An address in
// the tailnet's CGNAT range or a private range is refused as not public.
func canonicalPublic(f field, raw string) (string, error) {
	if a, err := netip.ParseAddr(raw); err == nil {
		if a.Zone() != "" {
			return "", fmt.Errorf("--%s %q: want an address without a zone", f.name, raw)
		}
		for p, what := range notPublic {
			if netip.MustParsePrefix(p).Contains(a.Unmap()) {
				return "", fmt.Errorf("--%s %q: not public: in %s, %s; want the address the internet reaches the machine at", f.name, raw, p, what)
			}
		}
		return a.String(), nil
	}
	labels := strings.Split(raw, ".")
	ok := len(raw) <= 253 && strings.Trim(labels[len(labels)-1], "0123456789") != ""
	for _, l := range labels {
		ok = ok && dnsLabel.MatchString(l)
	}
	if !ok {
		return "", fmt.Errorf("--%s %q: want an IP address or a DNS name of lower-case labels (RFC 1123)", f.name, raw)
	}
	return raw, nil
}

// marshal is v as compact JSON, with < > & left alone.
func marshal(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // ignored: a []string or map of strings always encodes
	return strings.TrimSuffix(b.String(), "\n")
}

func (f field) zero() string {
	switch {
	case f.null:
		return ""
	case f.def != "":
		return f.def
	case f.typ == "int":
		return "0"
	case f.typ == "bool":
		return "false"
	}
	return ""
}

// valueError is a flag value the row refuses to take: a malformed invocation.
type valueError struct{ msg string }

func (v valueError) Error() string { return v.msg }

// newRow is the row add writes: the given fields, the rest at their zero.
func (k *kind) newRow(name string, raw map[string]string) (Row, error) {
	empty := Row{Name: name, F: map[string]string{}}
	for _, f := range k.fields {
		empty.F[f.name] = f.zero()
	}
	r, err := k.merge(empty, raw)
	if len(name) > 128 || !namePattern.MatchString(name) {
		return Row{}, valueError{strings.TrimSuffix(fmt.Sprintf("name %q: want lower-case letters, digits and dashes, starting with a letter or digit; %v", name, err), "; <nil>")}
	}
	return r, err
}

// merge is the row a set leaves: cur with the given fields, checked whole.
func (k *kind) merge(cur Row, raw map[string]string) (Row, error) {
	next := Row{Name: cur.Name, F: maps.Clone(cur.F)}
	var problems []string // of the values given
	for _, f := range k.fields {
		if v, ok := raw[f.name]; ok {
			if c, err := f.canonical(v); err != nil {
				problems = append(problems, err.Error())
			} else {
				next.F[f.name] = c
			}
		}
	}
	if k.check != nil {
		if err := k.check(next); err != nil {
			if len(problems) == 0 {
				return Row{}, err
			}
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return Row{}, valueError{strings.Join(problems, "; ")}
	}
	return next, nil
}

// clockFetch is the rule of D8 (2026-11-07): two mirror-refresh --loop 60 records on every bench fetched two GitHub
// organisations every minute through one home connection (1.08 TB sent) and nothing refused it. A service whose
// argv names a fetcher and a loop, or a fetcher with --every above 0, is refused: fetch on an event (a push) or once
// per job, never on a clock. This table is the whole rule, read over every word of argv (its base name, any =value
// cut off, a shell string split into its words at white space and ; & | ( )): a fetcher; a git verb that fetches,
// counted after a git word; a loop. Only the words listed are in scope; `for` is not one, since a for over a list of
// repositories runs once.
var clockFetch = map[string]string{
	"rsync": "fetcher", "curl": "fetcher", "wget": "fetcher", "mirror-refresh": "fetcher",
	"fetch": "git", "pull": "git", "clone": "git",
	"--loop": "loop", "--every": "loop", "watch": "loop", "while": "loop", "until": "loop", "--interval": "loop",
}

// fetchOnClock is the first fetcher and the first loop of argv under clockFetch, each "" when there is none.
func fetchOnClock(argv []string) (fetcher, loop string) {
	git := false
	for _, w := range argv {
		for _, tok := range strings.FieldsFunc(w, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(";&|()", r) }) {
			tok, _, _ = strings.Cut(tok, "=")
			tok = tok[strings.LastIndex(tok, "/")+1:]
			switch clockFetch[tok] {
			case "fetcher":
				fetcher = cmp.Or(fetcher, tok)
			case "git":
				if git {
					fetcher = cmp.Or(fetcher, "git "+tok)
				}
			case "loop":
				loop = cmp.Or(loop, tok)
			}
			git = git || tok == "git" // once a git word is seen it stays seen for the rest of argv: a later pull is counted
		}
	}
	return fetcher, loop
}

func checkService(r Row) error {
	var p []string
	var argv []string
	_ = json.Unmarshal([]byte(r.F["argv"]), &argv) // ignored: canonical checked the field; a bad value has no words
	every, _ := strconv.Atoi(r.F["every"])
	if f, loop := fetchOnClock(argv); f != "" && (loop != "" || every > 0) {
		how := cmp.Or(loop, "--every "+r.F["every"])
		p = append(p, fmt.Sprintf("service %s fetches on a clock (%s with %s); fetch on an event (a push) or once per job, never on a clock: drop %s and run it from the job or the hook that needs it", r.Name, f, how, how))
	}
	if r.F["every"] != "0" && r.F["keepalive"] == "true" {
		p = append(p, fmt.Sprintf("service %s has --every %s and --keepalive true; a service runs every n seconds or is kept alive, so set one: --every 0 or --keepalive false", r.Name, r.F["every"]))
	} else if r.F["every"] == "0" && r.F["keepalive"] != "true" {
		p = append(p, fmt.Sprintf("service %s has neither --every nor --keepalive; want --every <seconds> or --keepalive true", r.Name))
	}
	if r.F["keys"] != "" && r.F["seat"] == "" {
		p = append(p, fmt.Sprintf("service %s names secrets (--keys %s) and no --seat to open them from", r.Name, r.F["keys"]))
	}
	if len(p) > 0 {
		return fmt.Errorf("%s", strings.Join(p, "; "))
	}
	return nil
}

// rentedFields are the fields of a machine rented by the hour; an owned machine has them all empty.
var rentedFields = []string{"provider_id", "market", "spot_request", "price", "price_from", "purpose", "rented_by", "rented_at", "return_by"}

// checkRented refuses a rented machine without the purpose, who rented it and when (so an idle one is never a
// mystery), a price without where it came from, a spot field on a machine that is not spot at AWS, and any rented
// field on an owned machine.
func checkRented(r Row) []string {
	var p []string
	v := r.F
	if v["provider"] == "owned" {
		for _, f := range rentedFields {
			if v[f] != "" {
				p = append(p, fmt.Sprintf("machine %s: --%s is a rented machine's, and its --provider is owned; set --provider latitude|aws, or --%s ''", r.Name, f, f))
			}
		}
		return p
	}
	for _, f := range []string{"purpose", "rented_by", "rented_at"} {
		if v[f] == "" {
			p = append(p, fmt.Sprintf("machine %s is rented (--provider %s) and has no --%s; give it", r.Name, v["provider"], f))
		}
	}
	if (v["price"] == "") != (v["price_from"] == "") {
		p = append(p, fmt.Sprintf("machine %s: --price and --price_from go together (provider: read from the provider; recalled: not read)", r.Name))
	}
	if v["market"] != "" && v["provider"] != "aws" {
		p = append(p, fmt.Sprintf("machine %s: --market is an aws machine's, and its --provider is %s; set --market ''", r.Name, v["provider"]))
	}
	if v["spot_request"] != "" && v["market"] != "spot" {
		p = append(p, fmt.Sprintf("machine %s: --spot_request is a spot machine's, and its --market is %q; set --market spot or --spot_request ''", r.Name, v["market"]))
	}
	return p
}

// checkMachine refuses an admin runner whose account is the machine's login, or whose label another runner of the
// machine carries (adminClashes), and a rented machine checkRented refuses. A field that does not parse is left to
// its own refusal.
func checkMachine(r Row) error {
	p := checkRented(r)
	admins, err := parseAdmins(r.F["windows_admin_runners"]) // nil when it does not parse
	for _, a := range admins {
		if strings.EqualFold(a.Account, r.F["user"]) {
			p = append(p, fmt.Sprintf("machine %s: admin runner account %s is the machine's user; give the runner its own account", r.Name, a.Account))
		}
	}
	if err == nil {
		for _, c := range adminClashes([]Row{r}, "") {
			p = append(p, c.msg)
		}
	}
	if len(p) > 0 {
		return fmt.Errorf("%s", strings.Join(p, "; "))
	}
	return nil
}

// adminClash is one admin runner label another runner carries: machine is the admin runner's.
type adminClash struct{ machine, msg string }

// adminClashes are the admin runner labels that another runner a job of the same repository could select carries:
// that job could then land on the admin runner. Taken are, on the admin runner's own machine, every label of its
// windows_runners and runner_scopes (whatever their url), its name, and where it has runner_scopes its roles, os and
// arch (the Linux runners' labels); on every machine with a runner of the repository or its owner, those labels of that
// runner and the machine's name; and the labels of every other admin runner of the repository, on any machine. With
// only, just the clashes that involve that machine. A field that does not parse has no labels.
func adminClashes(rows []Row, only string) []adminClash {
	var out []adminClash
	for _, r := range rows {
		admins, _ := parseAdmins(r.F["windows_admin_runners"]) // ignored: a bad value is refused by its own check
		for _, a := range admins {
			owner := a.URL[:strings.LastIndex(a.URL, "/")]
			for _, o := range rows {
				if only != "" && r.Name != only && o.Name != only {
					continue
				}
				self, on := o.Name == r.Name, " on machine "+o.Name
				if self {
					on = ""
				}
				taken := map[string]string{}
				reached := false
				for _, f := range []string{"windows_runners", "runner_scopes"} {
					ws, _ := parseScopes(o.F[f]) // ignored: a bad value is refused by its own check
					for _, w := range ws {
						if !self && !strings.EqualFold(w.URL, a.URL) && !strings.EqualFold(w.URL, owner) {
							continue
						}
						reached = true
						for _, l := range w.Labels {
							taken[strings.ToLower(l)] = f + " of " + w.URL + on
						}
						if f == "runner_scopes" {
							for _, l := range append(strings.Split(o.F["roles"], ","), o.F["os"], o.F["arch"]) {
								if l != "" {
									taken[strings.ToLower(l)] = "a role, os or arch label of the runner_scopes runners" + on
								}
							}
						}
					}
				}
				if self {
					taken[strings.ToLower(o.Name)] = "the machine's name"
				} else if reached {
					taken[strings.ToLower(o.Name)] = "the name of machine " + o.Name + ", a label of its runners of " + a.URL
				}
				others, _ := parseAdmins(o.F["windows_admin_runners"]) // ignored: as above
				for _, b := range others {
					if strings.EqualFold(b.URL, a.URL) && !(self && b.Account == a.Account) {
						for _, l := range b.Labels {
							taken[strings.ToLower(l)] = "a label of the admin runner " + b.Account + " of " + b.URL + on
						}
					}
				}
				for _, l := range a.Labels {
					if by, ok := taken[strings.ToLower(l)]; ok {
						out = append(out, adminClash{r.Name, fmt.Sprintf("machine %s: admin runner label %s is also %s; give the admin runner a label of its own", r.Name, l, by)})
					}
				}
			}
		}
	}
	return out
}

func checkFriend(r Row) error {
	var p []string
	if r.F["width"] == "0" {
		p = append(p, fmt.Sprintf("friend %s has --width 0; a friend works at least one card at once: --width 1 or more", r.Name))
	}
	if r.F["tiers"] == "" {
		p = append(p, fmt.Sprintf("friend %s has no --tiers; want a comma list of flash, pro, heavy, frontier", r.Name))
	}
	if len(p) > 0 {
		return fmt.Errorf("%s", strings.Join(p, "; "))
	}
	return nil
}

// dsnPassword says a postgres URI carries a password, in its user part or its query.
func dsnPassword(dsn string) bool {
	u, err := url.Parse(dsn)
	if err != nil {
		return strings.Contains(strings.ToLower(dsn), "password")
	}
	_, has := u.User.Password()
	q, _ := url.ParseQuery(u.RawQuery)
	for k := range q {
		has = has || strings.EqualFold(k, "password")
	}
	return has
}
