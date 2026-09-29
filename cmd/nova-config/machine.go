package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// TailscaleStatus represents the relevant identity fields from `tailscale status --json`.
type TailscaleStatus struct {
	Self struct {
		HostName string `json:"HostName"`
		DNSName  string `json:"DNSName"`
	} `json:"Self"`
}

// ExtractTailnetName extracts and validates the tailnet machine name from `tailscale status --json` output.
func ExtractTailnetName(data []byte) (string, error) {
	var status TailscaleStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return "", fmt.Errorf("tailscale status: invalid json: %w", err)
	}
	name := status.Self.HostName
	if status.Self.DNSName != "" {
		trimmed := strings.TrimSuffix(status.Self.DNSName, ".")
		parts := strings.Split(trimmed, ".")
		if len(parts) > 0 && parts[0] != "" {
			name = parts[0]
		}
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", fmt.Errorf("tailscale status reported no machine name")
	}
	if !config.NamePattern.MatchString(name) {
		return "", fmt.Errorf("tailscale machine name %q is invalid: must match %s", name, config.NamePattern)
	}
	return name, nil
}

// runMachineSync implements `nova-config machine sync [<name>] [--tailscale]`.
func runMachineSync(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	verb := "machine sync"
	fs := verbflag.New(verb)
	tailscale := fs.Bool("tailscale", false, "resolve machine identity from tailnet name")
	check := fs.Bool("check", false, "print the plan and write nothing")
	pg, redisAddr, as := connFlags(fs, true, true)

	name, rest := nameAndRest(&config.Kind{Name: config.KindMachine}, args)
	if err := fs.Parse(rest); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if name == "" && fs.NArg() == 1 {
		name = fs.Arg(0)
	} else if fs.NArg() > 1 || (name != "" && fs.NArg() > 0) {
		return refuse(stderr, verb, "takes at most one machine name; flags follow the name")
	}

	if name == "" {
		if *tailscale {
			if d.tailscaleStatus == nil {
				return refuse(stderr, verb, "tailscale status runner not configured")
			}
			data, err := d.tailscaleStatus(ctx)
			if err != nil {
				return refuse(stderr, verb, fmt.Sprintf("tailscale status: %v", err))
			}
			tsName, err := ExtractTailnetName(data)
			if err != nil {
				return refuse(stderr, verb, err.Error())
			}
			name = tsName
		} else {
			return refuse(stderr, verb, "want <name> or --tailscale; run: nova-config help")
		}
	}

	if !config.NamePattern.MatchString(name) {
		return refuse(stderr, verb, fmt.Sprintf("machine name %q is invalid: must match %s", name, config.NamePattern))
	}

	dsn, err := pgDSN(*pg, d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()

	row, exists, err := st.Get(ctx, config.KindMachine, name)
	if err != nil {
		return storeErr(stderr, verb, err, "nova-config machine list")
	}
	if !exists {
		return refused(stderr, verb, fmt.Sprintf("machine %s does not exist", name), fmt.Sprintf("nova-config machine add %s --user <user> --seat <seat> --slots <n>", name))
	}

	rAddr, err := redisAddress(*redisAddr, d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	ap, err := d.openRedis(ctx, rAddr)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer ap.Close()

	if err := ap.Prepare(ctx); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	views, _, err := ap.Read(ctx, config.KindMachine)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	rev, err := st.Rev(ctx, config.KindMachine)
	if err != nil {
		return storeErr(stderr, verb, err, "nova-config machine list")
	}

	actor := *as
	if actor == "" {
		actor = d.getenv(envActor)
	}
	if actor == "" {
		actor = name
	}

	word := "APPLY"
	if *check {
		word = "CHECK"
	}

	k, _ := config.Lookup(config.KindMachine)
	prev, ok := views[name]
	if !ok {
		op := config.Op{Op: config.OpAdd, Name: name, Row: row}
		if !*check {
			idem := fmt.Sprintf("config:%s:%d", config.KindMachine, rev+1)
			if err := ap.Write(ctx, config.KindMachine, row, nil, actor, idem); err != nil {
				return storeErr(stderr, verb, err, "nova-config apply")
			}
		}
		fmt.Fprintln(stdout, config.OpLine(word, config.KindMachine, op))
	} else {
		var changed []string
		for _, f := range k.Fields {
			if row.Fields[f.Name] != prev[f.Name] {
				changed = append(changed, f.Name)
			}
		}
		if len(changed) > 0 {
			op := config.Op{Op: config.OpSet, Name: name, Changed: changed, Row: row, Prev: prev}
			if !*check {
				idem := fmt.Sprintf("config:%s:%d", config.KindMachine, rev+1)
				if err := ap.Write(ctx, config.KindMachine, row, prev, actor, idem); err != nil {
					return storeErr(stderr, verb, err, "nova-config apply")
				}
			}
			fmt.Fprintln(stdout, config.OpLine(word, config.KindMachine, op))
		} else {
			fmt.Fprintf(stdout, "%s SAME kind=%s name=%s\n", word, config.KindMachine, config.Value(name))
		}
	}

	fmt.Fprintf(stdout, "CONFIG SYNC kind=machine name=%s rev=%d\n", config.Value(name), rev)
	return 0
}
