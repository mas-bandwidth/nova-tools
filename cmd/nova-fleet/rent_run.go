package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The production side of rent, return and stop: flags, nova-config, the providers, ssh, gh, the secrets store and
// the Tailscale API. The steps are in rent.go.

// configRecord is the record through nova-config, with the caller's environment (the store's password in it).
type configRecord struct{ bin string }

func (c configRecord) get(name string) (*rentedRow, error) {
	rows, err := record(c.bin, outCmd)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Name == name {
			return &r, nil
		}
	}
	return nil, nil
}

func (c configRecord) run(args ...string) error {
	_, why, code := outCmd(c.bin, args, os.Environ())
	if code != 0 {
		return fmt.Errorf("nova-config %s exit %d: %s", strings.Join(args[:min(3, len(args))], " "), code, oneLine(why))
	}
	return nil
}

func (c configRecord) add(r rentedRow, actor string) error {
	args := []string{"rented", "add", r.Name, "--provider", r.Provider, "--purpose", r.Purpose, "--rented_by", r.RentedBy,
		"--rented_at", r.RentedAt, "--actor", actor, "--reason", "nova-fleet rent"}
	for _, kv := range [][2]string{{"--market", r.Market}, {"--return_by", r.ReturnBy}, {"--creating", r.Creating}} {
		if kv[1] != "" {
			args = append(args, kv[0], kv[1])
		}
	}
	return c.run(args...)
}

func (c configRecord) set(name, actor string, fields ...string) error {
	args := []string{"rented", "set", name, "--actor", actor, "--reason", "nova-fleet rent/return"}
	for i := 0; i+1 < len(fields); i += 2 {
		args = append(args, "--"+fields[i], fields[i+1])
	}
	return c.run(args...)
}

func (c configRecord) remove(name, actor string) error {
	return c.run("rented", "remove", name, "--actor", actor, "--reason", "nova-fleet return: box and spot request gone")
}

// fleetPieces takes a box out of the fleet: runners, seat, tailnet device, machine row, host keys.
type fleetPieces struct {
	cfg, actor string
	out        io.Writer
}

var runnerLine = regexp.MustCompile(`^([0-9]+) (\S+) (true|false)$`)

// runnerScopes are the GitHub scopes a machine row's runner fields name (url=count[:labels], space or comma apart).
func runnerScopes(row map[string]any) []string {
	var out []string
	for _, k := range []string{"runner_scopes", "windows_runners", "windows_admin_runners"} {
		s, _ := row[k].(string)
		for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
			if u, _, _ := strings.Cut(f, "="); runnerScope(u) != "" && !slices.Contains(out, u) {
				out = append(out, u)
			}
		}
	}
	return out
}

func apiPath(url string) string {
	p := strings.TrimPrefix(strings.TrimSuffix(url, "/"), "https://github.com/")
	return map[string]string{"repo": "repos/", "org": "orgs/"}[runnerScope(url)] + p
}

func (f fleetPieces) teardown(r rentedRow, force bool, scopes []string) error {
	var row map[string]any
	if out, why, c := outCmd(f.cfg, []string{"machine", "list", "--json"}, os.Environ()); c != 0 {
		return fmt.Errorf("nova-config machine list exit %d: %s", c, oneLine(why))
	} else {
		var v struct {
			Items []struct{ Fields map[string]any }
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			return fmt.Errorf("nova-config machine list --json printed no JSON (%v)", err)
		}
		for _, it := range v.Items {
			if it.Fields["name"] == r.Name {
				row = it.Fields
			}
		}
	}
	// 0. work on the box: a clone with commits on no remote, a stash or uncommitted changes refuses the whole
	// return unless forced, since the disk goes with the box
	if r.Address != "" && !force {
		if work, err := unpushed(r.User, r.Address); err != nil {
			return fmt.Errorf("the clones on %s were not read (%v); run: nova-fleet return %s --force, once its work is safe", r.Name, err, r.Name)
		} else if work != "" {
			fmt.Fprint(f.out, work)
			return fmt.Errorf("%s holds work on no remote (the CLONE lines above); push it, or run: nova-fleet return %s --force", r.Name, r.Name)
		}
	}
	// 1. runners: a busy one refuses the whole return unless forced
	for _, u := range runnerScopes(row) {
		if !slices.Contains(scopes, u) {
			scopes = append(scopes, u)
		}
	}
	if len(scopes) > 0 {
		gh, err := lookPath("gh")
		if err != nil {
			return fmt.Errorf("gh is not on PATH, and the runners of %s are in %s; run: nova-fleet check --runners %s", r.Name, strings.Join(scopes, " "), scopes[0])
		}
		list := func(url string) ([][3]string, error) {
			out, why, c := outCmd(gh, []string{"api", "--paginate", apiPath(url) + "/actions/runners", "--jq", `.runners[] | "\(.id) \(.name) \(.busy)"`}, os.Environ())
			if c != 0 {
				return nil, fmt.Errorf("gh api %s/actions/runners exit %d: %s", apiPath(url), c, oneLine(why))
			}
			var mine [][3]string
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if m := runnerLine.FindStringSubmatch(line); m != nil && strings.HasPrefix(m[2], r.Name+"-") {
					mine = append(mine, [3]string{m[1], m[2], m[3]})
				}
			}
			return mine, nil
		}
		for _, u := range scopes {
			mine, err := list(u)
			if err != nil {
				return err
			}
			var busy []string
			for _, m := range mine {
				if m[2] == "true" {
					busy = append(busy, m[1])
				}
			}
			if len(busy) > 0 && !force {
				return fmt.Errorf("runners %s of %s are running a job; wait for it, or run: nova-fleet return %s --force", strings.Join(busy, " "), u, r.Name)
			}
			for _, m := range mine {
				if _, why, c := outCmd(gh, []string{"api", "-X", "DELETE", apiPath(u) + "/actions/runners/" + m[0]}, os.Environ()); c != 0 {
					return fmt.Errorf("gh api -X DELETE %s/actions/runners/%s (%s) exit %d: %s", apiPath(u), m[0], m[1], c, oneLine(why))
				}
			}
			if left, err := list(u); err != nil || len(left) > 0 {
				return fmt.Errorf("runners of %s still listed in %s after removal (%v); run: nova-fleet return %s again", r.Name, u, err, r.Name)
			}
			fmt.Fprintf(f.out, "RETURN RUNNERS %s %s removed=%d\n", r.Name, u, len(mine))
		}
	}
	// 2. seat, then the store pushed
	if sec, err := lookPath("nova-secrets"); err == nil {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(cmp.Or(os.Getenv("HOME"), home), ".config", "nova-secrets")
		store, pins := cmp.Or(os.Getenv("NOVA_SECRETS_STORE"), filepath.Join(dir, "store")), cmp.Or(os.Getenv("NOVA_SECRETS_PINS"), filepath.Join(dir, "pins"))
		seats, why, c := outCmd(sec, []string{"seat", "list", "--store", store}, os.Environ())
		if c != 0 {
			return fmt.Errorf("nova-secrets seat list --store %s exit %d: %s", store, c, oneLine(why))
		}
		if slices.ContainsFunc(strings.Split(seats, "\n"), func(l string) bool { f := strings.Fields(l); return len(f) > 0 && f[0] == r.Name }) {
			if _, why, c := outCmd(sec, []string{"seat", "remove", r.Name, "--store", store, "--pins", pins}, os.Environ()); c != 0 {
				return fmt.Errorf("nova-secrets seat remove %s exit %d: %s", r.Name, c, oneLine(why))
			}
			git, _ := lookPath("git")
			if refused, c := pushStore(git, store, false, "RETURN", f.out, f.out); c != 0 {
				return fmt.Errorf("the seat of %s is removed here and the store's push failed; push %s, then run: nova-fleet return %s again", r.Name, store, r.Name)
			} else if refused {
				fmt.Fprintf(f.out, "RETURN NOTE %s: merge the store's PR (the STORE PUSH REFUSED line); the return goes on\n", r.Name)
			}
			fmt.Fprintf(f.out, "RETURN SEAT %s removed\n", r.Name)
		}
		// 3. the tailnet device, if it is on the tailnet
		if r.Provider == "latitude" || row != nil {
			out, why, c := outCmd(sec, []string{"exec", "--only", "TAILSCALE_API_KEY", "--", "sh", "-c", `printf %s "$TAILSCALE_API_KEY"`}, os.Environ())
			key := strings.TrimSpace(out)
			if c != 0 || !strings.HasPrefix(key, "tskey-api-") {
				return fmt.Errorf("TAILSCALE_API_KEY cannot be read (exit %d) %s; %s", c, oneLine(why), newKey)
			}
			if err := dropDevice(newTailnet(key), r.Name, f.out); err != nil {
				return err
			}
		}
	}
	// 4. the machine row and the host keys
	if row != nil {
		if _, why, c := outCmd(f.cfg, []string{"machine", "remove", r.Name, "--actor", f.actor, "--reason", "nova-fleet return"}, os.Environ()); c != 0 {
			return fmt.Errorf("nova-config machine remove %s exit %d: %s", r.Name, c, oneLine(why))
		}
		fmt.Fprintf(f.out, "RETURN ROW %s removed\n", r.Name)
	}
	if kg, err := lookPath("ssh-keygen"); err == nil {
		for _, h := range []string{fmt.Sprint(row["address"]), fmt.Sprint(row["public"]), r.Address} {
			if h != "" && h != "<nil>" {
				_, _, _ = outCmd(kg, []string{"-R", h}, os.Environ()) // ignored: a host with no key there is already as wanted
			}
		}
	}
	return nil
}

// dropDevice deletes the tailnet device whose MagicDNS label is name, if there is one.
func dropDevice(api tsAPI, name string, out io.Writer) error {
	_, _, b, err := apiDo(api, "GET", "/tailnet/-/devices", "application/json", "", nil)
	if err != nil {
		return err
	}
	var v struct{ Devices []struct{ ID, Name string } }
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("GET /tailnet/-/devices: not JSON (%v)", err)
	}
	for _, d := range v.Devices {
		if label, _, _ := strings.Cut(d.Name, "."); label == name {
			if _, _, _, err := apiDo(api, "DELETE", "/device/"+d.ID, "application/json", "", nil); err != nil {
				return err
			}
			fmt.Fprintf(out, "RETURN TAILNET %s device %s deleted\n", name, d.ID)
		}
	}
	return nil
}

var pciID = regexp.MustCompile(`\[([0-9a-f]{4}:[0-9a-f]{4})\]`)

// sshHardware reads nproc and the PCI ids over ssh as login, accepting the box's host key on first use.
// clonesScript prints, for each git clone under the box's home directories (a runner's job checkouts aside), its
// commits on no remote branch, its stashes and its uncommitted changes, as CLONE <dir> and the lines under it;
// then CLONES READ. Read as root, so every user's clones count.
const clonesScript = `set -e; for g in $(sudo -n find /home /root -maxdepth 6 -name .git -not -path '*/_work/*' 2>/dev/null); do
d=${g%/.git}; w=$(sudo -n git -c safe.directory='*' -C "$d" log --branches --not --remotes --oneline; sudo -n git -c safe.directory='*' -C "$d" stash list; sudo -n git -c safe.directory='*' -C "$d" status --porcelain)
if [ -n "$w" ]; then printf 'CLONE %s\n%s\n' "$d" "$w" | head -40; fi; done; sudo -n true; echo CLONES READ`

// unpushed is the work clonesScript finds on the box at address, "" when none.
func unpushed(login, address string) (string, error) {
	ssh, err := lookPath("ssh")
	if err != nil {
		return "", err
	}
	out, why, c := outCmd(ssh, []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", login + "@" + address, clonesScript}, os.Environ())
	work, read := strings.CutSuffix(out, "CLONES READ\n")
	if c != 0 || !read {
		return "", fmt.Errorf("ssh %s@%s exit %d: %s", login, address, c, oneLine(why))
	}
	return work, nil
}

func sshHardware(key string) func(address, login string) (int, []string, error) {
	return func(address, login string) (int, []string, error) {
		ssh, err := lookPath("ssh")
		if err != nil {
			return 0, nil, err
		}
		args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=accept-new"}
		if key != "" {
			args = append(args, "-i", key, "-o", "IdentitiesOnly=yes")
		}
		out, why, c := outCmd(ssh, append(args, login+"@"+address, "nproc; lspci -nn"), os.Environ())
		if c != 0 {
			return 0, nil, fmt.Errorf("ssh %s@%s exit %d: %s", login, address, c, oneLine(why))
		}
		lines := strings.SplitN(out, "\n", 2)
		cores, err := strconv.Atoi(strings.TrimSpace(lines[0]))
		if err != nil {
			return 0, nil, fmt.Errorf("ssh %s@%s: nproc printed %q", login, address, lines[0])
		}
		var pci []string
		for _, m := range pciID.FindAllStringSubmatch(out, -1) {
			pci = append(pci, m[1])
		}
		return cores, pci, nil
	}
}

// newProvider is the provider named, reading its credential through nova-secrets; unit tests replace it.
var newProvider = func(name string) (provider, error) {
	sec, err := lookPath("nova-secrets")
	if err != nil {
		return nil, fmt.Errorf("nova-secrets is not on PATH; run: nova-fleet check")
	}
	switch name {
	case "latitude":
		out, why, c := outCmd(sec, []string{"exec", "--only", "LATITUDE_API_KEY", "--", "sh", "-c", `printf %s "$LATITUDE_API_KEY"`}, os.Environ())
		if key := strings.TrimSpace(out); c == 0 && key != "" && !strings.ContainsAny(key, " \t\n") {
			return latitude{key: key}, nil
		}
		return nil, fmt.Errorf("LATITUDE_API_KEY cannot be read (exit %d) %s; run: nova-secrets set LATITUDE_API_KEY", c, oneLine(why))
	case "aws":
		cli, err := lookPath("aws")
		if err != nil {
			return nil, fmt.Errorf("the aws CLI is not on PATH; run: brew install awscli (or the AWS CLI v2 installer)")
		}
		return aws{cli: awsThrough(sec, cli)}, nil
	}
	return nil, fmt.Errorf("provider %q: want latitude or aws", name)
}

// rentFlags parses the verbs' flags: --name value or --name=value, the up flags after --.
func rentFlags(args []string, takes, bools []string) (pos []string, val map[string][]string, rest []string, err error) {
	val = map[string][]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, v, eq := strings.Cut(a, "=")
		switch {
		case a == "--":
			return pos, val, args[i+1:], nil
		case slices.Contains(bools, a):
			val[a] = append(val[a], "true")
		case slices.Contains(takes, flag):
			if !eq {
				if i+1 >= len(args) {
					return nil, nil, nil, fmt.Errorf("%s wants a value", flag)
				}
				i++
				v = args[i]
			}
			val[flag] = append(val[flag], v)
		case strings.HasPrefix(a, "-"):
			return nil, nil, nil, fmt.Errorf("unknown flag %s", a)
		default:
			pos = append(pos, a)
		}
	}
	return pos, val, nil, nil
}

func last(v map[string][]string, k string) string {
	if s := v[k]; len(s) > 0 {
		return s[len(s)-1]
	}
	return ""
}

func prodDeps(p provider, cfg string, key string, up []string, out io.Writer) rentDeps {
	return rentDeps{prov: p, rec: configRecord{bin: cfg}, hardware: sshHardware(key), sleep: time.Sleep, out: out,
		step: func(string) {},
		join: func(name, address string) error {
			at := len(up)
			if i := slices.Index(up, "--"); i >= 0 {
				at = i
			}
			if c := runUp(slices.Insert(slices.Clone(up), at, name+"="+address), out, os.Stderr); c != 0 {
				return fmt.Errorf("nova-fleet up %s exit %d (its lines above say why); the box is rented and recorded: run the same nova-fleet rent again to join it", name, c)
			}
			return nil
		}}
}

// runRentVerb is rent, return and stop: the parse, then the steps.
func runRentVerb(verb string, args []string, stdout, stderr io.Writer) int {
	help := map[string]string{"rent": rentHelp, "return": returnHelp, "stop": stopHelp}[verb]
	refuse := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "nova-fleet %s: %s; run: nova-fleet %s -h\n", verb, fmt.Sprintf(format, a...), verb)
		return 2
	}
	if slices.Contains(args, "-h") || slices.Contains(args, "--help") {
		fmt.Fprint(stdout, help)
		return 0
	}
	takes := map[string][]string{
		"rent": {"--provider", "--plan", "--site", "--image", "--purpose", "--return-by", "--actor", "--price", "--nic", "--cores",
			"--first-login", "--first-key", "--wait", "--project", "--ssh-key", "--group", "--disk", "--user-data"},
		"return": {"--actor", "--runners"},
		"stop":   {},
	}[verb]
	pos, v, up, err := rentFlags(args, takes, []string{"--spot", "--force"})
	if err != nil {
		return refuse("%v", err)
	}
	if len(pos) != 1 || !nameRE.MatchString(pos[0]) {
		return refuse("want one machine name (lowercase letters, digits and -), got %q", strings.Join(pos, " "))
	}
	if (v["--spot"] != nil && verb != "rent") || (v["--force"] != nil && verb != "return") || (up != nil && verb != "rent") {
		return refuse("--spot is rent's, --force is return's, and only rent passes flags after -- to up")
	}
	name := pos[0]
	cfg, err := lookPath("nova-config")
	if err != nil {
		return refuse("nova-config is not on PATH (%v)", err)
	}
	fail := func(err error) int {
		word := map[bool]string{true: "REFUSED", false: "FAILED"}[errors.Is(err, errRefused) || errors.Is(err, errHardware)]
		fmt.Fprintf(stderr, "%s %s %s: %v\n", strings.ToUpper(verb), word, name, err)
		return 1
	}
	if verb != "rent" {
		r, err := configRecord{bin: cfg}.get(name)
		if err != nil {
			return fail(err)
		}
		actor := last(v, "--actor")
		if verb == "return" && actor == "" {
			return refuse("--actor names who returns it (the record's history)")
		}
		if r == nil {
			if verb == "stop" {
				return fail(fmt.Errorf("%s is not rented (nova-fleet rented lists what is)", name))
			}
			fmt.Fprintf(stdout, "RETURN OK %s: no record, nothing rented by that name\n", name)
			return 0
		}
		p, err := newProvider(r.Provider)
		if err != nil {
			return fail(err)
		}
		d := prodDeps(p, cfg, "", nil, stdout)
		d.fleet = fleetPieces{cfg: cfg, actor: actor, out: stdout}
		if verb == "stop" {
			err = d.stopBox(name)
		} else {
			err = d.giveBack(name, actor, v["--force"] != nil, v["--runners"])
		}
		if err != nil {
			return fail(err)
		}
		return 0
	}
	a := rentArgs{provider: last(v, "--provider"), purpose: last(v, "--purpose"), returnBy: last(v, "--return-by"), actor: last(v, "--actor"),
		price: last(v, "--price"), nic: strings.ToLower(last(v, "--nic")), firstLogin: cmp.Or(last(v, "--first-login"), "root"), firstKey: last(v, "--first-key"),
		spec: rentSpec{Name: name, Plan: last(v, "--plan"), Site: last(v, "--site"), Image: last(v, "--image"), Spot: v["--spot"] != nil,
			Project: last(v, "--project"), Group: last(v, "--group"), UserData: last(v, "--user-data")}}
	for _, k := range v["--ssh-key"] {
		a.spec.SSHKeys = append(a.spec.SSHKeys, strings.Split(k, ",")...)
	}
	need := map[string]string{"--provider": a.provider, "--plan": a.spec.Plan, "--site": a.spec.Site, "--image": a.spec.Image, "--purpose": a.purpose, "--return-by": a.returnBy, "--actor": a.actor}
	switch a.provider {
	case "latitude":
		need["--project"], need["--ssh-key"] = a.spec.Project, strings.Join(a.spec.SSHKeys, ",")
		if a.spec.Spot {
			return refuse("Latitude has no spot market")
		}
	case "aws":
		need["--group"] = a.spec.Group
		if up != nil {
			return refuse("an AWS box gets no tailnet, seat or secrets, so it takes no up flags")
		}
	default:
		return refuse("--provider latitude or aws")
	}
	for _, k := range slices.Sorted(maps.Keys(need)) {
		if need[k] == "" {
			return refuse("%s is required", k)
		}
	}
	if t, err := time.Parse(time.RFC3339, a.returnBy); err != nil || !t.After(now()) {
		return refuse("--return-by %q: want an RFC 3339 UTC time ahead of now, e.g. %s", a.returnBy, now().UTC().Add(4*time.Hour).Format(time.RFC3339))
	}
	if a.price != "" {
		if f, err := strconv.ParseFloat(a.price, 64); err != nil || f <= 0 {
			return refuse("--price %q: want dollars an hour, e.g. 1.38", a.price)
		}
	}
	if a.nic != "" && !regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{4}$`).MatchString(a.nic) {
		return refuse("--nic %q: want the PCI vendor:device lspci -nn prints, e.g. 8086:15ff for an Intel X710", a.nic)
	}
	for k, to := range map[string]*int{"--cores": &a.cores, "--disk": &a.spec.Disk} {
		if s := last(v, k); s != "" {
			if n, err := strconv.Atoi(s); err != nil || n <= 0 {
				return refuse("%s %q: want a whole number", k, s)
			} else {
				*to = n
			}
		}
	}
	a.spec.Disk = cmp.Or(a.spec.Disk, 60)
	if s := last(v, "--wait"); s != "" {
		if a.wait, err = time.ParseDuration(s); err != nil {
			return refuse("--wait %q: want a duration, e.g. 30m", s)
		}
	}
	if a.provider == "latitude" {
		if !slices.Contains(up, "--first-login") {
			up = append([]string{"--first-login", a.firstLogin}, up...)
		}
		if a.firstKey != "" && !slices.Contains(up, "--first-key") {
			up = append([]string{"--first-key", a.firstKey}, up...)
		}
	}
	p, err := newProvider(a.provider)
	if err != nil {
		return fail(err)
	}
	if err := prodDeps(p, cfg, a.firstKey, up, stdout).rent(a); err != nil {
		return fail(err)
	}
	return 0
}
