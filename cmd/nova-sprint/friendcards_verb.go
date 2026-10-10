package main

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A friend's held cards, as her daemon writes them (the card daemon-writes-every-taken-card3;
// docs/SPEC-FRIEND.md, the inbox): friend cards <friend> is every card on her row (working,
// then the ready ones dealt behind them; withdrawn ones are not hers to run) with its packet
// and its BRIEF.md whole, rendered as friend sync renders it (friendBrief, friendReadText),
// so the daemon writes the same file friend sync would and never waits on friend sync to
// run. It writes nothing. A worker sends it as `friend cards <friend> [--json]`, and the
// server answers it on POST as any worker's verb and on GET /api/friend/<friend>/cards.
func init() {
	verbClasses["friend cards"] = classRead
}

// friendCardsWords is what friend cards says on -h.
const friendCardsWords = "friend cards lists every card held on the friend's row, working then ready, each with its job (inbox/<job>), its column, branch, tier, attempt and generation, and with --json its BRIEF.md whole as friend sync writes it: her nova-friend daemon reads it every loop and writes each held card's brief that is not in her inbox, and retires a job whose card left her row. A card taken back from her (withdrawn) is not listed. It writes nothing. The server serves it to the friend herself on POST and on GET /api/friend/<friend>/cards.\n"

// friendCardsOf is every card held on the friend's row with its packet and brief, in the
// server's order: her working cards, then the ready ones dealt behind them.
func friendCardsOf(ctx context.Context, st *store.Store, novaRoot, name string) ([]friend.HeldCard, error) {
	cards, err := st.ReadCells(ctx, sprint.Fleet, sprint.FriendRow(name), sprint.Working, sprint.Ready)
	if err != nil || len(cards) == 0 {
		return []friend.HeldCard{}, err
	}
	packets, err := st.Packets(ctx, cards)
	if err != nil {
		return nil, err
	}
	dirs, err := st.FriendDirs(ctx) // her row's dir names her working directory in the brief
	if err != nil {
		return nil, err
	}
	out := make([]friend.HeldCard, 0, len(packets))
	for i, p := range packets {
		h := friend.HeldCard{Card: p.Card, Col: string(cards[i].Col), Kind: cmp.Or(p.Kind, "work"), Branch: p.Branch, Attempt: p.Attempt, Gen: p.Gen, Epoch: p.Epoch}
		m, _ := cardhdr.ReadModel(p.Brief) // ignored: a line 1 that does not read is the default tier
		h.Tier = cmp.Or(p.Tier, m.Tier, cardhdr.RouteFlash)
		if p.Kind == "read" {
			// a read card's job is a card's (friendJobOf), the path friend sync writes (friendReadOf)
			h.Job, h.Branch, h.Brief = friendJobOf(p), cmp.Or(p.WorkBranch, cards[i].F("branch")), friendReadTextAtDir(st, novaRoot, name, dirs[name], p, cards[i])
		} else {
			h.Job, h.Brief = friendJobOf(p), friendBriefAtDir(novaRoot, name, dirs[name], p)
		}
		out = append(out, h)
	}
	return out, nil
}

func (a *app) cmdFriendCards(args []string, stdout, stderr io.Writer) int {
	const name = "friend cards"
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	switch {
	case err != nil:
		return refuse(stderr, name, err.Error())
	case len(pos) != 1:
		return refuse(stderr, name, "wants one friend: friend cards <friend>")
	case !sprint.ValidID(pos[0]):
		return refuse(stderr, name, "a friend name wants letters, digits, _ and -: "+pos[0])
	}
	who := pos[0]
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	names, err := st.FriendNames(ctx)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	if !slices.Contains(names, who) {
		fmt.Fprintf(stderr, "%s %s: no friend %s on the friends table (friends: %s); run: nova-sprint friend sync\n", prog, name, who, orDashStr(strings.Join(names, ","), "none"))
		return 1
	}
	held, err := friendCardsOf(ctx, st, a.machineNovaRoot(), who)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	if c.json {
		b, _ := json.Marshal(friend.HeldAnswer{Friend: who, Cards: held})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, h := range held {
		fmt.Fprintf(stdout, "FRIEND-CARD %s job=%s col=%s kind=%s branch=%s tier=%s attempt=%d gen=%d\n", oneline.Field(h.Card), oneline.Field(h.Job), h.Col, h.Kind, oneline.Field(dashed(h.Branch)), oneline.Field(h.Tier), h.Attempt, h.Gen)
	}
	fmt.Fprintf(stdout, "FRIEND-CARDS OK friend=%s cards=%d\n", who, len(held))
	return 0
}

// friendCardsPath is where the server serves friend cards on GET: /api/friend/<friend>/cards.
const friendCardsPath = "/api/friend/"

// serveFriendCards runs friend cards <friend> --json for a GET, on the line of control as
// serveView does, and answers its JSON, gzipped for a client that takes it. A name of the
// wrong shape is a 400 and nothing is run; a friend not on the friends table is a 404; a
// store that did not answer is a 503; each with the verb's line.
func (a *app) serveFriendCards(w http.ResponseWriter, r *http.Request) {
	who, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, friendCardsPath), "/")
	if rest != "cards" {
		http.Error(w, "a friend's cards are read with GET "+friendCardsPath+"<friend>/cards", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "a friend's cards are read with GET "+friendCardsPath+"<friend>/cards", http.StatusMethodNotAllowed)
		return
	}
	if !sprint.ValidID(who) {
		http.Error(w, "<friend> names one friend (letters, digits, _ and -)", http.StatusBadRequest)
		return
	}
	var stdout, stderr bytes.Buffer
	a.serial.Lock()
	code := a.run([]string{"friend", "cards", "--redis", a.serveAddr, "--actor", "", who, "--json"}, &stdout, &stderr)
	a.serial.Unlock()
	switch code {
	case 0:
	case 1:
		http.Error(w, strings.TrimSpace(stderr.String()), http.StatusNotFound)
		return
	default:
		http.Error(w, strings.TrimSpace(stderr.String()), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	var out io.Writer = w
	if takesGzip(r.Header.Get("Accept-Encoding")) {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer func() { _ = gz.Close() }() // ignored: a reader that has gone reads no answer
		out = gz
	}
	_, _ = out.Write(stdout.Bytes()) // ignored: a reader that has gone reads no answer
}
