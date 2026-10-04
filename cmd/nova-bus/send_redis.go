package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friendbus"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
)

// cmdSendRedis is the explicitly selected Redis transport. It uses the same
// note shape as Git send but writes only through the durable friend bus. A
// successful publish proves that deliveries were queued; it says nothing
// about a harness wake or business completion.
func cmdSendRedis(addr, op, file string, useStdin bool, preparedFile string, usePreparedStdin bool, as, slug, host string, dryRun bool, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	if preparedFile != "" || usePreparedStdin {
		return refuse(stderr, "send", "--prepared artifacts are Git-only; give --file or --stdin with Redis transport", verbHelp("send"))
	}
	if dryRun {
		return refuse(stderr, "send", "--dry-run is Git-only; Redis send publishes atomically when invoked", verbHelp("send"))
	}
	if as == "" || strings.TrimSpace(as) != as || len(as) > friendbus.MaxIdentityBytes || strings.ContainsAny(as, "\r\n\x00") {
		return refuse(stderr, "send", "--as must be a bounded, trimmed single-line identity with Redis transport", verbHelp("send"))
	}
	if strings.TrimSpace(op) == "" || strings.TrimSpace(op) != op || len(op) > friendbus.MaxIdentityBytes || strings.ContainsAny(op, "\r\n\x00") {
		return refuse(stderr, "send", "--op must be a bounded single-line identity of at most 128 bytes", verbHelp("send"))
	}
	if (file == "") == !useStdin {
		return refuse(stderr, "send", "give exactly one of --file and --stdin", verbHelp("send"))
	}
	var raw []byte
	var err error
	if useStdin {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(file)
	}
	if err != nil {
		fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), verbHelp("send")))
		return 2
	}
	n, problems := bus.ParseNoteAll("", string(raw))
	if len(problems) != 0 {
		for _, problem := range problems {
			fmt.Fprintf(stderr, "SEND FAIL: %s\n", oneline.Err(problem))
		}
		return 1
	}
	if n.Header.ID != "" {
		return refuse(stderr, "send", "the tool assigns the Id; delete the Id header before sending", verbHelp("send"))
	}
	if countBusIDs(n.Header.Re) > 1 {
		return refuse(stderr, "send", "Re names one thread; name one id in the draft", verbHelp("send"))
	}
	if n.Header.From == "" {
		n.Header.From = as
	} else if !strings.EqualFold(strings.TrimSpace(n.Header.From), strings.TrimSpace(as)) {
		return refuse(stderr, "send", "--as does not match the draft's From header", verbHelp("send"))
	}
	if n.Header.To == "" || n.Header.Subject == "" {
		return refuse(stderr, "send", "the draft needs To and Subject headers", verbHelp("send"))
	}
	to, err := redisRecipients(n.Header.To)
	if err != nil {
		return refuse(stderr, "send", err.Error(), verbHelp("send"))
	}
	cc, err := redisRecipients(n.Header.Cc)
	if err != nil {
		return refuse(stderr, "send", err.Error(), verbHelp("send"))
	}
	if len(to)+len(cc) > 256 {
		return refuse(stderr, "send", "Redis transport accepts at most 256 To and Cc names", verbHelp("send"))
	}
	if host != "" {
		if n.Header.Host != "" && n.Header.Host != host {
			return refuse(stderr, "send", "--host does not match the draft's Host header", verbHelp("send"))
		}
		n.Header.Host = host
	}
	date := now.UTC().Format(bus.DateLayout)
	n.Header.Date = ""
	n.Body = bus.NormalizeBody(n.Body)
	if len(n.Body) > 1<<20 {
		return refuse(stderr, "send", "Redis transport accepts note bodies up to 1 MiB", verbHelp("send"))
	}
	if slug != "" {
		return refuse(stderr, "send", "--slug is Git-only; Redis note identity follows its content", verbHelp("send"))
	}
	// The stable operation ID makes a retry idempotent. friendbus fingerprints
	// the canonical note while keeping this attempt's generated Date out of the
	// fingerprint, then retains the first published Date for every redelivery.
	n.Header.ID = friendbus.OperationID(as, op)
	text := redisNoteBody(n)
	user, passwordEnv := "", ""
	if os.Getenv(redisauth.UserEnv) != "" {
		user = os.Getenv(redisauth.UserEnv)
		passwordEnv = os.Getenv(redisauth.PasswordEnvEnv)
		if passwordEnv == "" {
			passwordEnv = redisauth.DefaultPasswordEnv
		}
	}
	conn, err := redisconn.Open(context.Background(), redisconn.Options{
		Addr:        addr,
		User:        user,
		PasswordEnv: passwordEnv,
	}, os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), verbHelp("send")))
		return 2
	}
	defer conn.Close()
	fb, err := friendbus.New(friendbus.Config{Prefix: "nova-bus", Group: "harness", Redis: conn.Client()})
	if err != nil {
		fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), verbHelp("send")))
		return 2
	}
	result, err := fb.Publish(context.Background(), friendbus.Message{
		ID: n.Header.ID, Actor: as, Op: op, From: n.Header.From, To: to, CC: cc,
		Body: []byte(text), Re: strings.Join(n.Header.Re, "; "), Date: date,
	})
	if err != nil {
		fmt.Fprintf(stderr, "SEND FAIL id=%s: %s\n", oneline.Field(n.Header.ID), oneline.WithRemedy(oneline.Err(err), verbHelp("send")))
		return 1
	}
	fmt.Fprintf(stdout, "SEND OK id=%s queued=%d body_bytes=%d\n", oneline.Field(result.ID), result.Queued, len(n.Body))
	return 0
}

func redisRecipients(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	names := bus.AddressNames(raw)
	if len(names) == 0 {
		return nil, fmt.Errorf("recipient list contains no names")
	}
	for _, name := range names {
		if strings.EqualFold(name, "all") || strings.EqualFold(name, "table") {
			return nil, fmt.Errorf("Redis transport has no roster to expand %q; name each recipient explicitly", name)
		}
	}
	return names, nil
}

func redisNoteBody(n bus.Note) string {
	n.Header.Date = ""
	return strings.Replace(n.Render(), "Date: \n", "", 1)
}
