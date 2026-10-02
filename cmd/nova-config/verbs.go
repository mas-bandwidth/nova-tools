package main

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// What each verb's -h adds above its flags (verbflag.RecoverWith): its effect
// (an inspection, a store write, an external delivery; docs/STANDARD.md,
// section 2, "Its effects are explicit") and a worked example that runs as
// printed against a --file store, so the banner stays short and every verb
// still shows its first use.

// The effects, one sentence each.
const (
	effectInspect   = "inspection: reads the store (PostgreSQL, or the --file) and writes nothing"
	effectStoreRow  = "store write: one row and its history row, in PostgreSQL or the --file; --dry-run prints the change (CONFIG DRY-RUN) and writes nothing"
	effectBinary    = "inspection: reads nothing but this binary"
	effectMigrate   = "store write: makes or upgrades schema config (or makes the --file), each migration above the greatest recorded once; --print and --dry-run write nothing (--dry-run reads the ledger)"
	effectStatus    = "inspection: reads the store and Redis, writes nothing"
	effectApply     = "external delivery: writes Redis, the copy of the rows the fleet reads, through its own Redis Functions; --dry-run prints the lines and writes nothing"
	effectInventory = "inspection: reads Redis (the state apply wrote) or the --fixture file, never PostgreSQL, and writes nothing"
)

// kindExamples is each kind's worked example per verb, in the order a first
// try runs them on one --file store (TestEveryKindsExamplesRunInOrder runs
// them all, in this order, on one file). A verb with none here shows none.
var kindExamples = []struct{ kind, verb, line string }{
	{"machine", "add", "nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as a1 --file try.json"},
	{"machine", "set", "nova-config machine set m1 --width 6 --as a1 --file try.json"},
	{"machine", "list", "nova-config machine list --file try.json"},
	{"machine", "show", "nova-config machine show m1 --file try.json"},
	{"machine", "history", "nova-config machine history m1 --file try.json"},
	{"machine", "width", "nova-config machine width m1 --file try.json"},
	{"fleet", "set", "nova-config fleet set --coordinator m1 --store m1 --as a1 --file try.json"},
	{"fleet", "show", "nova-config fleet show --file try.json"},
	{"fleet", "history", "nova-config fleet history --file try.json"},
	{"friend", "add", "nova-config friend add f1 --slots 4 --tiers flash,pro --roles builder --as a1 --file try.json"},
	{"friend", "set", "nova-config friend set f1 --slots 8 --as a1 --file try.json"},
	{"friend", "list", "nova-config friend list --file try.json"},
	{"friend", "show", "nova-config friend show f1 --file try.json"},
	{"friend", "history", "nova-config friend history f1 --file try.json"},
	{"sprint", "set", "nova-config sprint set --coordinator f1 --as a1 --file try.json"},
	{"sprint", "show", "nova-config sprint show --file try.json"},
	{"sprint", "history", "nova-config sprint history --file try.json"},
	{"loop", "add", `nova-config loop add reader-m1 --machine m1 --argv '["nova-swarm","member","--as","reader-m1","--reader"]' --keepalive true --as a1 --file try.json`},
	{"loop", "set", "nova-config loop set reader-m1 --enabled false --as a1 --file try.json"},
	{"loop", "list", "nova-config loop list --file try.json"},
	{"loop", "show", "nova-config loop show reader-m1 --file try.json"},
	{"loop", "history", "nova-config loop history reader-m1 --file try.json"},
	{"route", "add", "nova-config route add flash-a --tier flash --provider p1 --model small-1 --deadline 900 --tokens 200000 --as a1 --file try.json"},
	{"route", "set", "nova-config route set flash-a --price_input 0.30 --price_output 1.20 --as a1 --file try.json"},
	{"route", "list", "nova-config route list --file try.json"},
	{"route", "show", "nova-config route show flash-a --file try.json"},
	{"route", "history", "nova-config route history flash-a --file try.json"},
	{"tier", "set", "nova-config tier set flash --routes flash-a --as a1 --file try.json"},
	{"tier", "list", "nova-config tier list --file try.json"},
	{"tier", "show", "nova-config tier show flash --file try.json"},
	{"tier", "history", "nova-config tier history flash --file try.json"},
	// a row another names is held (the sprint names f1, the fleet m1, the tier
	// flash-a): the run test clears those fields before these lines
	{"loop", "remove", "nova-config loop remove reader-m1 --as a1 --file try.json"},
	{"friend", "remove", "nova-config friend remove f1 --as a1 --file try.json"},
	{"route", "remove", "nova-config route remove flash-a --as a1 --file try.json"},
	{"machine", "remove", "nova-config machine remove m1 --as a1 --file try.json"},
}

// toolExamples is the worked example of each verb that is not a kind's.
var toolExamples = map[string]string{
	"kinds":     "nova-config kinds",
	"migrate":   "nova-config migrate --file try.json",
	"status":    "nova-config status --file try.json",
	"apply":     "nova-config apply --dry-run --redis 127.0.0.1:6379 --file try.json",
	"inventory": "nova-config inventory --fixture fleet/testdata/inventory-fixture.yml",
	"version":   "nova-config version",
}

// verbExtra is the lines a verb's -h prints above its flags: its effect and
// its worked example.
func verbExtra(verb string) string {
	var effect, example, more string
	words := strings.Fields(verb)
	switch {
	case verb == "version" || verb == "kinds":
		effect = effectBinary
	case verb == "migrate":
		effect = effectMigrate
	case verb == "status":
		effect = effectStatus
	case verb == "apply":
		effect = effectApply
	case verb == "inventory":
		effect = effectInventory
		more = inventoryMore
	case verb == "machine self":
		effect = "inspection: prints this machine's name and opens no store; --check reads the machine rows"
	case len(words) == 1:
		if k, ok := config.Lookup(verb); ok {
			// a kind alone is the group of its verbs: its help, and nothing run
			effect = "inspection: this help of the " + k.Name + " verbs; each verb's own -h has its flags and an example"
		}
	case len(words) == 2:
		switch words[1] {
		case "add", "set", "remove":
			effect = effectStoreRow
		default:
			effect = effectInspect
		}
		if k, ok := config.Lookup(words[0]); ok && words[1] == "add" {
			more = requiredLine(k)
		}
		if words[0] == config.KindTier && words[1] == "remove" {
			more = "a tier row is made by migrate and never removed: set its --routes instead\n"
		}
		for _, e := range kindExamples {
			if e.kind == words[0] && e.verb == words[1] {
				example = e.line
			}
		}
	}
	if example == "" {
		example = toolExamples[verb]
	}
	out := ""
	if effect != "" {
		out += "effect: " + effect + "\n"
	}
	out += more
	if example != "" {
		out += "example: " + example + "\n"
	}
	return out
}

// requiredLine names the fields add refuses a row without.
func requiredLine(k *config.Kind) string {
	var req []string
	for _, f := range k.Fields {
		if f.Required {
			req = append(req, "--"+f.Name)
		}
	}
	if len(req) == 0 {
		return ""
	}
	return "required: " + strings.Join(req, " ") + " (and --as); every other field takes its default\n"
}

// inventoryMore is inventory's own help: what it prints and how ansible
// reads it.
const inventoryMore = `prints an Ansible dynamic JSON inventory of the applied state (the Redis view apply writes, never Postgres): groups all and benches are every machine; coordinator, store and store_deployer (the coordinator machine) come from the fleet row; runners is every machine with at least one runner; every host's variables are under _meta.hostvars: ansible_user, nova_seat, slots, runners, nova_os and nova_arch from the machine's beat when it has one, and nova_loops, its loop records (each argv as the loop was set, without a width: a member's width, and a reader's, is its machine's), once the loop kind has been applied; all.vars holds nova_store and nova_config_rev
first run, with no store: nova-config inventory --fixture fleet/testdata/inventory-fixture.yml
against the store: export NOVA_SPRINT_REDIS=127.0.0.1:6379; nova-config inventory
ansible's -i wants an executable file whose first line is #!/bin/sh at column one; write it with these two commands, then run ansible with ANSIBLE_INVENTORY_UNPARSED_FAILED=true, because without it a failed inventory is an empty inventory and the play does nothing (ansible.cfg: [inventory] unparsed_is_failed = True):
  printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > nova-inventory
  chmod +x nova-inventory
  ANSIBLE_INVENTORY_UNPARSED_FAILED=true ansible-inventory -i ./nova-inventory --list
env: NOVA_SPRINT_REDIS (then NOVA_REDIS_ADDR, then the seat's address) names the store; NOVA_MACHINE names the machine row this process runs on (an empty value counts as unset), matched by exact machine name and refused with the known names when it names no row; when it is unset the lower-cased first label of the hostname is matched, and nothing is marked local when that matches no row
this verb exits 0 when it printed, 1 when the applied state or an unknown machine refused it, 2 when it could not run (usage, connection, timeout)
`
