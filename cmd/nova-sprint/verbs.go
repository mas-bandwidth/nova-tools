package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/cardlimits"
	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

type flagSet = *flag.FlagSet

type verb struct {
	name, syntax, example string
	run                   func(*app, []string, io.Writer, io.Writer) int
}

var verbs []verb

func init() {
	verbs = []verb{
		{"init", "[--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--owner <name>] [--rules <file>]", "init --readers reader-a,reader-b,reader-c --members m1:64,m2:64", (*app).cmdInit},
		{"add", "--stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> [--brief-file <f2>...]: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>] [--held] [--allow-shared-paths] [--one: a single card is meant] [--replaces <old-id>[,<old-id>]: the one card is their twin] [--allow-personal-base]", "add --stream s1 --count 100", (*app).cmdAdd},
		{"quack", "--streams <a,b,...> --count <n> --repo <clone url> [--tiers <t,...>] [--base <branch>]", "quack --streams a,b --count 2 --repo https://example.com/quack.git", (*app).cmdQuack},
		{"preflight", "--brief-dir <dir> [--repo-dir <dir>]", "preflight --brief-dir .", (*app).cmdPreflight},
		{"release check", "[--json] [--streams <glob>] [--window <duration>] [--merge-p90 <duration>] [--check <name>]...", "release check", (*app).cmdReleaseCheck},
		{"release", "(<sentinel or held card>... | <selector> [--dry-run]) --reason <text> [--answers <note>]", "release s1-stop --reason 'the layer is green and read'", (*app).cmdReleaseSel},
		{"resolve", "[<id>...] [--stream <s>] [--max <n>]", "resolve", (*app).cmdResolve},
		{"friends watch", "[--actor <seat>] [--state <file>]", "friends watch --actor seat-a --state friends-push.json", (*app).cmdFriendsWatch},
		{"status watch", "[--actor <seat>] [--state <file>]", "status watch --actor seat-a --state status-push.json", (*app).cmdStatusWatch},
		{"start", "", "start", (*app).cmdMachineStart},
		{"stop", "--reason <text> --until <time or duration>", "stop --reason 'the bench is rebooting' --until 30m", (*app).cmdMachineStop},
		{"stop-return", "--as <owner-row> <card>@<gen>... --epoch <n> --reason <cancel acknowledgement> [--dry-run]", "stop-return --as friend-a s1-1.w1@1 --epoch 15 --reason 'owned process stopped'", (*app).cmdStopReturn},
		{"run", "[--answer-rules=false] [--idle-alarm=false] [--listen <address:port>] [--land] [--decide <dir>]", "run", (*app).cmdRunGC},
		{"tick", "[--answer-rules] [--idle-alarm] [--shadow]", "tick", (*app).cmdTick},
		{"selftest land", "[--binary <path>] [--scratch-dir <dir>]", "selftest land", (*app).cmdSelftestLand},
		{"selftest", "[--dir <d>] [--keep]", "selftest", (*app).cmdSelftest},
		{"goal set", "<name> [--file <path>] [--to file:<path>]", "goal set friend-a --file goal-a.txt --to file:/tmp/reminder-a.txt", (*app).cmdGoalSet},
		{"goal show", "[<name>]", "goal show friend-a", (*app).cmdGoalShow},
		{"goal drop", "<name>", "goal drop friend-a", (*app).cmdGoalDrop},
		{"take", "--as <member> [<card>@<gen>...] [--epoch <n>] [--max <n>]", "take --as m1 s1-1.w1@1 --epoch 0", (*app).cmdTake},
		{"finish", "--as <member> <card>@<gen>... --epoch <n> (--head <commit> | --failed) [--report <text>] [--usage <text>]", "finish --as m1 s1-1.w1@1 --epoch 0 --head 9f3c2e1 --report 'tests green'", (*app).cmdFinish},
		{"progress", "--as <worker> <card>[@<gen>]... --epoch <n>", "progress --as m1 s1-1.w1@1 --epoch 0", (*app).cmdProgress},
		{"ask", "[<id>... | --group <id> [--expect <n>]] [--stream <s>] [--max <n>] [--another] [--answers <note>]", "ask", (*app).cmdAsk},
		{"queue", "--as <reader|member> | --stream <s>", "queue --as reader-a", (*app).cmdQueue},
		{"read", "--as <reader> (--begin | --ok | --broken) [<card>[@<gen>]...] --epoch <n> [--max <n>] [--finding <text>] [--usage <text>] | --as <reader> --return <card> --reason <text> --epoch <n> [--usage <text>]", "read --as reader-a --ok --max 5 --epoch 0", (*app).cmdRead},
		{"accept", "(<id>... [--heavy --evidence <path> --reason <text>] | --stream <s> | --read-ok | --group <id> [--expect <n>]) [--answers <note>]", "accept --read-ok", (*app).cmdAccept},
		{"rework", "(<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--fix <text>] [--tier <tier>] [--answers <note>] [--one]", "rework s1-4 --fix 'handle the empty case'", (*app).cmdReworkSel},
		{"return", "(<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--reason <text>] [--answers <note>]", "return s1-7 --reason 'suspect of the red batch'", (*app).cmdReturnSel},
		{"redo", "<card>... [--stream <s>] [--answers <note>]", "redo s1-2", (*app).cmdRedo},
		{"drop", "(<id>... | --stream <s> --col <state> | --group <id> [--expect <n>] | <selector> [--dry-run]) --reason <text> [--answers <note>] [--one]", "drop s1-9 --reason obsolete", (*app).cmdDropSel},
		{"priority", "<id>... | (<id>... | --stream <s>) (--blocker | --critical | --fix | --high | --normal | --low) --reason <text>", "priority s1-4 --high --reason 'the release waits on it'", (*app).cmdPriority},
		{"unpin", "(<id>... | --stream <s>) --reason <text> [--dry-run]", "unpin s1-1 --reason available", (*app).cmdUnpin},
		{"rebase", "--from <branch> --to <branch> [--repo-dir <clone>] [--dry-run]", "rebase --from dev --to sprint/s1 --repo-dir ../name --dry-run", (*app).cmdRebase},
		{"rank", "(<id>... | <selector> [--dry-run]) (--score <n> | --first | --before <id>) [--answers <note>]", "rank s2-3 --first", (*app).cmdRankSel},
		{"relink", "<old-id>[,<old-id>...] <new-id> [--reason <text>]", "relink lint-pkg-cairn-t lint-pkg-cairn-tb --reason 're-cut as its twin'", (*app).cmdRelink},
		{"recut", "<id> (--tier <flash|pro|heavy|frontier> | --brief-file <path> [--rules <file>]) [--new <id>] | <selector> (--tier <t> | --set-base <branch> | --drop-who)... [--dry-run]", "recut lint-pkg-cairn-t --tier heavy", (*app).cmdRecutSel},
		{"twin", "<card> [--paths <extra,...>] [--needs <card,...>] [--before <card>] [--tier <t>] [--instruction <text>] [--carry]", "twin cards3 --paths internal/b/** --instruction 'widen the fix'", (*app).cmdTwin},
		{"brief", "<id> (--brief <text> | --brief-file <path>) [--rules <file>] [--answers <note>] | --dir <dir> [--rules <file>] | --group <id> [--expect <n>] (--brief-file <path> | --dir <dir>) [--answers <note>] | <id> --widen [--repo-dir <clone>] | <id> --tier <flash|pro|heavy|frontier> | <selector> (--set-base <branch> | --drop-who | --tier <t>)... [--dry-run]", "brief s1-4 --brief-file s1-4.md", (*app).cmdBriefSel},
		{"move", "<id>... --stream <s> [--before <id> | --after <id> | --score <n>]", "move s1-4 s1-5 --stream s2", (*app).cmdMove},
		{"merge", "--stream <s> [--batch <n>] [--conflict <id> [--conflict-kind file|ledger] [--conflict-path <p>...] | --cross <id>=<other> | --red [--suspect <id>...] | --rejected | --base-red <error>] [--note <text>]", "merge --stream s1 --batch 100", (*app).cmdMerge},
		{"land", "[--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]", "land --stream s1 --dry-run", (*app).cmdLand},
		{"verify-landed", "[--stream <s>...] [--repo-dir <clone>] [--base <branch>]", "verify-landed --stream s1 --base main", (*app).cmdVerifyLanded},
		{"landed", "<id>... --sha <commit> --reason <text> [--repo-dir <clone>] [--base <branch>]", "landed s1-1 --sha 0123abc --reason pushed-unreported --base main", (*app).cmdLanded},
		{"snapshot", "(--dir <dir> [--keep <n>] [--every <duration>] | --restore-drill <file>)", "snapshot --dir /tmp/nova-sprint-snapshots --keep 7", (*app).cmdSnapshot},
		{"backup", "(--out <dir> [--part-bytes <n>] [--secrets-store <dir> --secrets-as <seat> --secrets-key <path> --sops <path>] | --file <path> [--dry-run])", "backup --file /tmp/nova-sprint-backup.rdb", (*app).cmdBackup},
		{"demo load", "<backup.xz part>... [--sha256 <hex>] [--dir <dir>] [--xz <path>] [--redis-server <path>]", "demo load sprint-store-2026-10-04-2336.redis.txt.xz.part-aa sprint-store-2026-10-04-2336.redis.txt.xz.part-ab", (*app).cmdDemoLoad},
		{"demo stop", "[--dir <dir>]", "demo stop --dir ./no-demo-here", (*app).cmdDemoStop},
		{"promote", "[--every <duration>] [--landings <n>] [--branch <name>] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]", "promote --dry-run", (*app).cmdPromote},
		{"resume", "--stream <s> [--did <text>] [--answers <note>]", "resume --stream s1 --did 'land merges s1-4 again'", (*app).cmdResume},
		{"hold", "<member|reader|friend|stream>... --reason <text> [--return] [--dry-run]", "hold m1 --reason 'the build cache cleaner deletes live entries'", func(a *app, args []string, o, e io.Writer) int { return a.cmdHold(false, args, o, e) }},
		{"unhold", "<member|reader|friend|stream>... [--reason <text>] [--dry-run]", "unhold m1 --reason 'the cleaner is fixed'", func(a *app, args []string, o, e io.Writer) int { return a.cmdHold(true, args, o, e) }},
		{"fleet beat", "<member> [--load <percent>]", "fleet beat m1", (*app).cmdFleetBeat},
		{"fleet up", "<member> [--width <n> | --width 0]", "fleet up m1 --width 64", func(a *app, args []string, o, e io.Writer) int { return a.cmdFleet("up", args, o, e) }},
		{"fleet down", "<member>", "fleet down m1", (*app).cmdFleetDown},
		{"fleet sync", "[--check] [--pg <dsn>]", "fleet sync --check", (*app).cmdFleetSync},
		{"fleet level", "", "fleet level", func(a *app, args []string, o, e io.Writer) int { return a.cmdFleet("level", args, o, e) }},
		{"fleet quiet", "<member> (--for <duration> | --until <RFC3339>) --reason <text> | <member> --end [--dry-run]", "fleet quiet m1 --for 11m --reason 'load 64: the macOS CI legs time out'", (*app).cmdFleetQuiet},
		{"friend sync", "[--pg <dsn>] [--root <dir>]", "friend sync", (*app).cmdFriendSync},
		{"collect", "[<friend>...] [--dead-lanes] [--pg <dsn>] [--root <dir>] [--dry-run]", "collect --dead-lanes", (*app).cmdCollect},
		{"friend beat", "<friend> [--working <n>] [--queue <n>] [--width <n>] [--running <id>,...] [--load <percent>] [--active <RFC3339>] [--check <nonce>] [--pong <nonce>] [--run <id>]", "friend beat friend-a --working 2 --queue 3 --load 40", (*app).cmdFriendBeat},
		{"friend down", "<friend> [--reason <text>] [--until <RFC3339>]", "friend down friend-a --reason 'opus rate limited'", func(a *app, args []string, o, e io.Writer) int { return a.cmdFriendHold(true, args, o, e) }},
		{"friend up", "<friend> [--width <n>]", "friend up friend-a --width 4", func(a *app, args []string, o, e io.Writer) int { return a.cmdFriendHold(false, args, o, e) }},
		{"friend cards", "<friend> [--json]", "friend cards friend-a --json", (*app).cmdFriendCards},
		{"friend take", "<friend> (<id>... | --all-unstarted) [--reason <text>]", "friend take friend-a s1-4 --reason 'she is on another job'", (*app).cmdFriendTake},
		{"friend give", "<friend> <id>... [--reason <text>]", "friend give friend-a s1-4 --reason 'the take was the harness, not hers'", (*app).cmdFriendGive},
		{"friend level", "", "friend level", (*app).cmdFriendLevel},
		{"friend health", "<friend> (--state up|asleep|down --seen <RFC3339> --generation <n> [--queue <n>] [--working <n>] [--width <n>] [--reason <text>] [--until <RFC3339>] | --clear)", "friend health friend-a --state up --seen 2026-10-04T15:00:00Z --generation 1", (*app).cmdFriendHealth},
		{"friend clean", "[--pg <dsn> | --file <path>] [--root <dir>] [--days <n>] [--dry-run]", "friend clean --dry-run", (*app).cmdFriendClean},
		{"gc", "[--machine <m>] [--dry-run] [--max-age <d>]", "gc --dry-run", (*app).cmdGC},
		{"friend reconcile", "<friend> [--root <dir>] [--dry-run]", "friend reconcile friend-a --dry-run", (*app).cmdFriendReconcile},
		{"lane take", "<kind> --machine <m> --as <worker> [--wait <duration>] [--dry-run]", "lane take go --machine m1 --as m1", (*app).cmdLaneTake},
		{"lane give", "<kind> --machine <m> --as <worker> [--dry-run]", "lane give go --machine m1 --as m1", (*app).cmdLaneGive},
		{"lane list", "", "lane list", (*app).cmdLaneList},
		{"reader add", "<reader>... [--tiers <flash[,pro,heavy,frontier]|all|default>]", "reader add reader-d", (*app).cmdReaderAdd},
		{"reader set", "<reader>... --tiers <flash[,pro,heavy,frontier]|all|default>", "reader set reader-a --tiers flash", (*app).cmdReaderSet},
		{"reader away", "<reader>...", "reader away reader-d", func(a *app, args []string, o, e io.Writer) int { return a.cmdReaderHold(true, args, o, e) }},
		{"reader up", "<reader>...", "reader up reader-d", func(a *app, args []string, o, e io.Writer) int { return a.cmdReaderHold(false, args, o, e) }},
		{"reader remove", "<reader>...", "reader remove reader-d", (*app).cmdReaderRemove},
		{"reader retire", "<reader>...", "reader retire reader-d", (*app).cmdReaderRetire},
		{"stream remove", "<stream>...", "stream remove a b c", (*app).cmdStreamRemove},
		{"stream archive", "<stream>...", "stream archive a b c", func(a *app, args []string, o, e io.Writer) int { return a.cmdStreamArchive(true, args, o, e) }},
		{"stream unarchive", "<stream>...", "stream unarchive a", func(a *app, args []string, o, e io.Writer) int { return a.cmdStreamArchive(false, args, o, e) }},
		{"stream set", "<stream>... [--read-tier <flash|pro|heavy|default>] [--land-protected <owner/name,...|any|default>] [--promotion[=false]] [--release <name>] [--prose <glob,...|default>] [--attempts <n|default>] [--base <branch>] [--reason <text>] [--answers <notes>]", "stream set skips --read-tier pro", (*app).cmdStreamSet},
		{"set", "[--rework-priority <fix|high|keep>] [--read-tier <flash|pro|default>] [--read-cards <on|off|default>] [--dealt-max <duration|default>] [--go-lanes <n|default>] [--alarm-review <n|off>] [--alarm-merging <n|off>] [--alarm-fleet <percent|off>] [--alarm-ready <on|off>] [--review-starved <duration|off>] [--attempts <n|default>] [--friend-idle <duration|default>] [--pin-wait <duration|default>] [--friend-finish <duration|default>] [--fleet-tiers <tiers|all>] [--friends-tiers <tiers|all>] [--reads <0|1|2|default>]", "set --read-tier pro", (*app).cmdSet},
		{"promoted", "--sha <merge sha> [--answers <note>]", "promoted --sha 0123abc", (*app).cmdPromoted},
		{"merge-window open", "--for <duration> --reason <text>", "merge-window open --for 10m --reason 'the release merges by hand'", (*app).cmdMergeWindowOpen},
		{"funded", "<provider> --reason <text>", "funded opencode --reason 'paid $100 in the console'", (*app).cmdFunded},
		{"cost reconcile", "[--dry-run] [--json]", "cost reconcile", (*app).cmdCostReconcile},
		{"cost reprice", "[--route <r>]... [--since <RFC3339>] [--dry-run] [--json]", "cost reprice --since 2026-10-01T00:00:00Z --dry-run", (*app).cmdCostReprice},
		{"ci", "<id>... (--red | --green) --epoch <n> [--head <h>] [--run <id>] [--source <s>] [--note <text>]", "ci s1-3 --red --run 812 --source ci --epoch 0", (*app).cmdCI},
		{"wait", "(<note>[,<note>]... | --group <id> [--expect <n>]) (--for <duration> | --until <RFC3339>)", "wait n1,n2 --for 3h", (*app).cmdWait},
		{"remind", "(--in <duration> | --at <time>) --note <text> [--for <actor>] | --list | --cancel <id>", "remind --in 30m --note window-closes", (*app).cmdRemind},
		{"ack", "<note>[,<note>]... --reason <text>", "ack ci-x-1.1 --reason 'a flaky runner; the rerun is green'", (*app).cmdAck},
		{"answer", "[--dry-run] [--bar <p>] [--every <duration>] [--timeout <duration>] [--backend jev|fixed] [--answers <file>] [--record <file>]", "answer --dry-run", (*app).cmdAnswer},
		{"inbox", "[--open <group>] [--read] [--wait [--timeout <duration>] [--push <dir> | --push seat]] [--deadline <duration>] [--stale <duration>]", "inbox --wait", (*app).cmdInbox},
		{"card base", "<id> <branch> [--repo-dir <clone>]", "card base s1-4 main", (*app).cmdCardBase},
		{"card", "<id> [--brief | --fields] [--at-epoch <n>] | (--all | --stream <s>) --json: every card, one JSON object a line", "card s1-4", (*app).cmdCard},
		{"needs", "[--stream <s>] [--roots]", "needs --stream s1", (*app).cmdNeeds},
		{"streams", "[--repo <owner/name>] [--release <name>] [--cards]", "streams --cards", (*app).cmdStreams},
		{"held", "[--stream <s>]", "held", (*app).cmdHeld},
		{"sentinels", "[--stream <s>]", "sentinels", (*app).cmdSentinels},
		{"sentinel set", "<id> --needs <a,b>", "sentinel set s1-stop --needs s1-2", (*app).cmdSentinelSet},
		{"bases", "", "bases", (*app).cmdBases},
		{"log", "[--card <id>] [--stream <s>] [--member <m>] [--since <10m|RFC3339>] [--at-epoch <n>]", "log --card s1-4", (*app).cmdLog},
		{"check", "", "check", (*app).cmdCheck},
		{"repair", "", "repair", (*app).cmdRepair},
		{"watch", "--wake [--every <duration>] [--state <file>] [--check <duration>] [--judgment-every <duration>] [--merge-every <duration>] [--backlog-every <duration>] [--land-after <duration>] [--merge-over <n>] [--merging-over <n>] [--review-over <n>]", "watch --wake --state wake.json", (*app).cmdWatch},
		{"seat check", "", "seat check", (*app).cmdSeatCheck},
		{"machinery", "", "machinery", (*app).cmdMachinery},
		{"where", "[--watch] [--every <duration>] [--all] [--json [--cards] [--rows]] [--release [<name>]]", "where", (*app).cmdWhere},
		{"dashboard", "[--listen <address:port>[,<address:port>...] | none] [--pull <address:port>[,<address:port>...] | none] [--logo <file>] [--every <duration>]", "dashboard --listen 127.0.0.1:7390 --pull 127.0.0.1:7395", (*app).cmdDashboard},
		{"handover", "", "handover", (*app).cmdHandover},
		{"view coordinator", "[--all] [--since <cursor>] [--json]", "view coordinator --json", (*app).cmdViewCoordinator},
		{"view cards", "[--col <c>] [--stream <s>] [--holder <member>] [--by tier|stream|col|holder] [--json]", "view cards --col review --by tier --json", (*app).cmdViewCards},
		{"view worker", "--as <member|friend> [--since <cursor>] [--json]", "view worker --as m1 --json", (*app).cmdViewWorker},
		{"seat install", "--harness <name> --target <dir> [--session <id>] [--dir <dir>] [--log <file>] [--server <host:port>] [--config-seat <name> --config-dsn <dsn> --config-password-env <NAME>] [--dry-run]", "seat install --dry-run --redis 127.0.0.1:6381", (*app).cmdSeatInstall},
		{"seat watch", "<dir> [--json]", "seat watch ./inbox", (*app).cmdSeatWatch},
		{"seat uninstall", "[--dir <dir>]", "seat uninstall --dir ./no-unit-here", (*app).cmdSeatUninstall},
		{"seat deliver", "[--text <message>] --actor <seat>", "seat deliver --actor coordinator --text hello", (*app).cmdSeatDeliver},
		{"seat push", "[--harness <name> --target <dir> [--session <id>]] [--sent <nonce> [--failed <why>]] [--beat bus|friends|transitions [--failed <why>]] [--observe friends|transitions --json] [--dry-run]", "seat push", (*app).cmdSeatPush},
		{"seat pong", "<nonce> [--dry-run]", "seat pong received-nonce", (*app).cmdSeatPong},
		{"seat", "[--repair --reason <text>] | push [--harness <name> --target <dir> [--session <id>]] | pong <nonce>", "seat", (*app).cmdSeat},
		{"fsck seat", "[--pg <host:port or postgres:// URI>]", "fsck seat", (*app).cmdFsckSeat},
		{"routes", "", "routes", (*app).cmdRoutes},
		{"rules", "", "rules", (*app).cmdRules},
		{"stats tidy", "(--friends | --fleet | --routes | --streams | --all)... --reason <text> [--dry-run]", "stats tidy --all --reason 'a fresh start' --dry-run", (*app).cmdStatsTidy},
		{"stats", "[--routes [--since <10m|RFC3339>]]", "stats", (*app).cmdStats},
		{"play", "[--simulation] [--seed <n>] [--every <duration>] [--broken <p>] [--fail <p>] [--stuck <p>] [--cross <p>] [--down <p>] [--up <p>] [--red <p>] [--flap <p>] [--batch <n>] [--hold] [--silent <member>@<from>+<for>]... [--ticks <n>]", "play --seed 7 --every 1s", (*app).cmdPlay},
		{"clear", "--confirm sprint", "clear --confirm sprint", (*app).cmdClear},
		{"teardown", "--confirm sprint", "teardown --confirm sprint", (*app).cmdTeardown},
		{"live", "[--bin-dir <dir>] [--dashboard <link>]... [--json]", "live --json", (*app).cmdLive},
		{"adopt", "<version|path> --source <checkout> --inventory <file> --reason <text> [--limit <host>] [--receipts <dir>] [--dry-run]", "adopt v1.2.0-dev.0123abc --source . --inventory ./nova-inventory --reason 'the dashboard fix' --dry-run", (*app).cmdAdoptPlay},
		{"server switch", "[<binary>] [--rollback] [--window <duration>] [--target <path>] [--tick-deadline <duration>]", "server switch /path/to/binary --rollback", (*app).cmdServerSwitch},
		// last: its example moves the seat, and every coordinator verb's example before it is the holder's
		{"coordinator", "<name> --reason <text> | <name> --take --approved-by <owner> --reason <text>", "coordinator friend-b --reason 'friend-a is out of credits; friend-b holds the seat'", (*app).cmdCoordinator},
	}
	// install, uninstall and units stay before coordinator, whose example moves the seat.
	verbs = slices.Insert(verbs, len(verbs)-1, installVerbs...)
	installVerbMeta()
	notServed = append(notServed, "friends watch", "status watch", "seat deliver")
	for i := range verbs {
		switch verbs[i].name {
		case "start", "stop", "resume", "hold", "unhold", "coordinator", "friend health", "fleet up":
			name, run := verbs[i].name, verbs[i].run
			verbs[i].run = func(a *app, args []string, o, e io.Writer) int { return a.withSeatPushLines(name, args, o, e, run) }
		case "where":
			verbs[i].run = (*app).cmdWhere
		}
	}

}

func verbNames() []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range verbs {
		n, _, _ := strings.Cut(v.name, " ")
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return append(out, "help", "version")
}

// groupVerbs is the verbs of the group word names (fleet, friend, reader, goal, stream, merge-window):
// every verb whose name is that word and more; nil for a word that is no group.
func groupVerbs(word string) []string {
	var out []string
	for _, v := range verbs {
		if strings.HasPrefix(v.name, word+" ") {
			out = append(out, v.name)
		}
	}
	return out
}

// opening is the banner's first three answers: what the tool does (line 1,
// the README's sentence), how it works, and the first run (ONBOARDING.md
// point 6).
const opening = `nova-sprint: a sprint of work cards, dealt to a fleet of workers and read before they land

how it works: one store (Redis or a twin file) holds the work, readers, merge
and fleet tables and the sprint view. A card is one unit of work in a stream.
Each tick deals ready cards to members (machines with a width), sends finished
work to readers and queues passed work for merging by stream. Decisions it
cannot make go to the coordinator's inbox.
first run: no Redis needed; the store is the file sprint.twin:
  export NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss
Follow the card flow under "trying it without a Redis", ticking by hand.
For a real fleet, "A real fleet" explains the server and clients; the example:
block shows the coordinator's day on that store.
For one verb's usage, examples, flags and exit codes:
  nova-sprint help <verb> (or <verb> -h)
For one group's help: nova-sprint help <group> (fleet, friend, reader, goal,
stream, lane, merge-window).`

func banner() string {
	var b strings.Builder
	b.WriteString(opening + "\n\nusage:\n")
	for _, v := range verbs {
		b.WriteString("  nova-sprint " + strings.TrimSpace(v.name+" "+v.syntax) + "\n")
	}
	b.WriteString(`
Every store verb takes --redis <addr> (else NOVA_SPRINT_REDIS, then
NOVA_REDIS_ADDR), --actor <name> (else NOVA_SPRINT_ACTOR; no
default: a verb that writes wants one), --op <id> (the same id again returns
the recorded result), --json and --max <n> (listed items, and the count of a
set when the verb takes one; 0 is all listed; --limit is --max for one
release). The
coordinator's verbs are the coordinator's alone (the first init names it:
--coordinator, else the actor); take, finish, read, fleet beat, friend
beat, lane take and lane give are the workers', whose actor is the member, reader or friend named; merge and ci are
reports; tick, run, friend clean and promote are the machine's; the reads need no actor (inbox
--read, which moves the coordinator's cursor, is the coordinator's). The seat
moves by coordinator <name> --reason <text>: given by its holder or the owner
(init --owner), or taken by <name> itself with --take --approved-by <owner>,
each in the log; handover prints what the next seat needs. A set is
ids, a stream, a column, --max n, or an inbox group: --group <id>, the id
inbox prints, which does not move, with --expect <n>, the size it printed,
which refuses a group that has changed. Each verb prints what moved (MOVED),
what did not and why (REFUSED, on stderr), its summary line, and the sprint's
line: landed/all percent -> ETA <estimate> (the streams on the table: an
archived stream's cards leave it; every card left, held ones too, at
the cards landed an hour: where's over the last hour of running time, the
whole sprint's average with fewer than five there and on this line; in minutes
rounded up, days and hours from a day; where shows the largest
of the last 10 s, and held=N, the cards behind a sentinel not released or
admitted held; the word alone until one has landed; a stopped
machine has no ETA: STOPPED, then
landed/all and the percent when there are cards; every card landed, no ETA:
done in <time from the first start> while it runs, and STOPPED ... done once
the machine has stopped itself).

The tables are work, merge, readers and fleet, and the view is sprint; a store
holds one sprint (a second sprint is a second store). The work table's cost
column is, per stream, the sum of its landed cards' total cost in US dollars
(each consumer's actual cost, else its predicted one; - when none was priced),
with the sum over the streams at the bottom; card <id> shows the detail. clear and teardown want
--confirm sprint, the name of the view, and refuse anything else.

A work card is named with its generation, <card>@<gen>: the generation the
worker holds, from queue --as <member> (--json: "gen"). take by id and finish
name it for every card; a card named without one is refused, naming the live
generation, and a generation that is not the live one is refused as stale.
take with no card takes the member's oldest ready cards (--max n, default 1)
and prints each one's generation.

` + inboxExample + `
` + machineWords() + `
` + serverWords() + `
` + holdWords() + `
` + fleetWords() + `
` + friendWords() + `
` + readerWords() + `
` + streamWords() + `
` + laneWords() + `
` + goalWords() + `
` + twinWords() + `
` + landWords() + `
` + wordsSection() + `
` + exitLine + `

the coordinator's day, in five lines (NOVA_SPRINT_REDIS and NOVA_SPRINT_ACTOR set; nova-sprint run ticking in a shell of its own; brief.txt is a card that passes the lint, from nova-swarm template --name card, its REPO: and BASE: filled in):

example:
`)
	for _, l := range dayLines {
		b.WriteString("  " + l + "\n")
	}
	return b.String()
}

// dayLines is the coordinator's day in five lines, the banner's example: block.
var dayLines = []string{
	"nova-sprint init --readers reader-a,reader-b --members m1:8",
	"nova-sprint add --stream s1 --count 3 --brief-file brief.txt",
	"nova-sprint start",
	"nova-sprint inbox --wait",
	"nova-sprint land --stream s1 --check 'make test'",
}

// inboxExample is the worked example of reading the inbox and answering it,
// in nova-sprint help, nova-sprint help inbox, and inbox -h.
const inboxExample = `reading the inbox and answering a judgment:

  $ nova-sprint inbox
  JUDGMENT finish-0314a1b2-1.1   work came back failed  stream=s1  size=2  waited=4m0s  due=10:14:00  (s1-3,s1-7)  the tests went red
    rework with a fix:
      nova-sprint rework --group finish-0314a1b2-1.1 --expect 2 --answers finish-0314a1b2-1.1
    drop:
      nova-sprint drop --group finish-0314a1b2-1.1 --expect 2 --reason '<why>' --answers finish-0314a1b2-1.1
  JUDGMENT merge-0315c3d4-1.1   stream stopped: stream branch red  stream=s2  size=10  waited=1m0s  due=10:25:00  (s2-1,s2-2,s2-3,s2-4,s2-5,s2-6,s2-7,s2-8,... all: nova-sprint inbox --open merge-0315c3d4-1.1)  suspects: s2-4 (of the batch of 10)
    take the suspect off and resume:
      nova-sprint return s2-4 --reason 'suspect of the red batch' --answers merge-0315c3d4-1.1
      nova-sprint resume --stream s2 --did 'returned s2-4' --answers merge-0315c3d4-1.1
    rework the suspect:
      nova-sprint return s2-4 --reason 'suspect of the red batch' --answers merge-0315c3d4-1.1
      nova-sprint rework s2-4 --fix '<fix>'
      nova-sprint resume --stream s2 --did 'returned s2-4 for rework' --answers merge-0315c3d4-1.1
    resume with what you did:
      nova-sprint resume --stream s2 --did '<what you did>' --answers merge-0315c3d4-1.1
  HAPPENED finish-0316e5f6-1.1   work came back ok  stream=s1  size=5  (s1-1,s1-2,s1-4,s1-5,s1-6)
  INBOX OK judgments=2 happened=1 cursor=-

A group is named by its id (its oldest notification's), which does not move
as groups come and go; a group number is refused. size is what --expect
takes: when the group has another size now the verb is refused, names what
was added or is gone, and changes nothing. Each decision is its commands, one
per line, in order: copy them, filling in a '<...>' first. inbox --open <id>
lists every member of a group, and every need a blocked group names; card <id> is everything about one primary.

The sprint done is no judgment: the tick that finds nothing open says it, one
HAPPENED line addressed to the coordinator and shown first, and stops the
machine (DONE):
  HAPPENED tick-done-0317a1b2-1.1   the sprint is done  x1  for=coordinator  9 landed, 0 dropped, took 1h2m0s from the first start
    to continue: add work, then nova-sprint start

one answer to each judgment (every one prints its own, filled in):
  ready to accept             accept --group <id> --expect <n> --answers <notes>
  work came back failed       rework --group <id> --expect <n> --answers <notes>  (each fix is the work's report; --fix for all; a harness fault or a HOLD with findings is reworked by rule failed, a friend's card too, so only a failure no class names waits here)
  a brief defect              drop <primary> --reason 'a brief defect: re-cut', then add --stream <s> '<new id>' --brief-file '<the re-cut brief>'  (never a redeal)
  a reader found it broken    rework --group <id> --expect <n> --answers <notes>  (each fix is the reader's finding; a read with a finding is reworked by rule read-broken, a friend's card too, so only one with no finding or at its brief's bound waits here)
  read's branch not on origin push the branch, then ack <note> --reason '<pushed>' (the tick asks the read again), or rework --fix '<fix>' or drop <id> --answers <note>
  the brief is wrong          brief <id> --brief-file <path> (in place: its next attempt, from its last pushed head), or brief --group <id> --expect <n> --dir <dir> --answers <notes>, or drop <id>; never rework (the same finding twice, or over 5 attempts on one brief)
  conflict on a card          resume --stream <s> --did '<what you did>' --answers <note>  (land merges again, regenerating the ledgers; a conflict outside them: rework or drop)
  stream branch red           return <suspect> --answers <note>, then resume --stream <s> --did 'returned <suspect>' --answers <note>
  needs another stream first  rank <other> --first, then resume --stream <s> once <other> has landed
  merge queue rejected        resume --stream <s> --did '<what you did>' --answers <note>
  the base fails its gate     resume --stream <s> --did '<the base is green again>' --answers <note>  (land gated it three times: at 0, 2 and 7 minutes)
  the base branch is gone     rebase --from <the base> --to '<the branch that replaces it>'  (every unlanded card on it moves, dealt cards included)
  a merging card names a base not on origin    card base <id> <a branch on origin> (answers it; the next land tries the card once), or ack <note>
  ci red                      rework --group <id> --expect <n> --fix '<fix>' --answers <notes>
  blocked on a dropped card   drop --group <id> --expect <n> --reason '<why>' --answers <notes>
  blocked on a missing card   drop <ids> --reason '<why>' or ack <notes> --reason '<why the named missing needs can be waived>'
  reads exhausted             ask --group <id> --expect <n> --another --answers <notes>
  repair skipped changes      card <primary>, then rework, return or drop --group <id> --expect <n> --answers <notes>
  an operation was stuck      check, then ack <note> --reason '<what you found>'
  a repeat: stop and look     card <primary>
  overdue: act                a decision above, or wait <note>[,<note>]... --for 30m, or wait --group <id> --expect <n> --for 30m
  a stream not moving: look   where, then queue --stream <s>
  sentinel reached            release <sentinel> --reason '<what you found>' --answers <note>
  returned to review          rework, accept (its reads standing) or drop --group <id> --expect <n> --answers <notes>
  stranded in review          rework or drop (or ask, if never asked) --group <id> --expect <n> --answers <notes>
  stalled                     card <primary> (HELD says what holds it), then the decision it prints, or ack <note> --reason '<why>'
  landed work scored low      add --stream <s> '<fix id>' --brief '<the finding>', then ack <note>; or ack <note> --reason '<why it stands>'
  timer                       ack <note> --reason '<what you did>'  (a timer remind set: it woke its actor, there is nothing to decide)

the mechanical judgments the run loop answers by rule, recorded "answered by rule <name>"
(failed, bound, late, conflict, brief-defect, base-gate, read-broken); nova-sprint rules prints what
they would answer now, and run --answer-rules=false turns them off

the routine judgments answered by nova-decide (broken, failed, blocked, stalled, conflict,
deadline, cannot ask, ready to accept, a card at its bound), card by card:
  nova-secrets exec --only JEV_API_KEY -- nova-sprint answer [--dry-run] [--bar <p>] [--every 60s] [--timeout 60s]
the verb the judgment decision chose is applied at or above decide_judgment_bar (nova-config's
sprint row; the bar ships empty, so nothing is applied and what a bar would apply is listed, until
it is set or --bar is given; 0.8 is a starting point measured on 100 of the coordinator's own judgments, not an independent calibration)
by the line the inbox prints for that card; every drop, everything
under the bar and a provider's refusal for want of payment (never asked: a payment is the
owner's) are listed for you; every decision is recorded with its outcome (--record), and
each verb applied carries the decision's --op, recorded as applying before it runs and
applied after, so nothing is applied twice
`

// verbExamples holds one more worked example per form a verb's -h shows,
// beyond its table's example, each without the tool's name.
var verbExamples = map[string][]string{
	"add": {
		"add --stream s1 --brief-dir briefs",
		"add --stream s1 --brief-file a.md --brief-file b.md",
	},
	"fsck seat": {},
}

// verbExample is the lines a verb's -h shows above its flags: its examples,
// one runnable line per form from the verb table, for verbflag.RecoverWith.
func verbExample(name string) string {
	for _, v := range verbs {
		if v.name != name {
			continue
		}
		lines := append([]string{v.example}, verbExamples[name]...)
		var b strings.Builder
		b.WriteString("example:\n")
		for _, line := range lines {
			if line != "" {
				fmt.Fprintf(&b, "  %s %s\n", prog, line)
			}
		}
		return b.String()
	}
	return ""
}

func versionLine() string { return buildinfo.Line(prog, version) }

func helpCommand(path []string, stdout, stderr io.Writer) int {
	if len(path) == 0 {
		fmt.Fprint(stdout, banner())
		return 0
	}
	name := strings.Join(path, " ")
	// a verb named whole is the exact one even when the word also opens a group
	// (seat is the seat's read, seat install and seat uninstall are its verbs)
	for _, v := range verbs {
		if v.name == name {
			code := func() (code int) {
				defer recoverHelp(stdout, &code)
				return v.run(newApp(func(string) string { return "" }), []string{"--help"}, stdout, stderr)
			}()
			return code
		}
	}
	if len(groupVerbs(name)) > 0 {
		fmt.Fprintln(stdout, "usage:")
		for _, v := range verbs {
			if strings.HasPrefix(v.name, name+" ") {
				fmt.Fprintln(stdout, "  nova-sprint "+strings.TrimSpace(v.name+" "+v.syntax))
			}
		}
		if name == "goal" {
			fmt.Fprint(stdout, "\n"+goalWords())
		}
		if name == "fleet" {
			fmt.Fprint(stdout, "\n"+fleetWords())
		}
		if name == "friend" {
			fmt.Fprint(stdout, "\n"+friendWords())
		}
		if name == "reader" {
			fmt.Fprint(stdout, "\n"+readerWords())
		}
		if name == "stream" {
			fmt.Fprint(stdout, "\n"+streamWords())
		}
		if name == "lane" {
			fmt.Fprint(stdout, "\n"+laneWords())
		}
		fmt.Fprintf(stdout, "\nnova-sprint help %s <verb> (or nova-sprint %s <verb> -h) prints a verb's flags, examples and exit codes.\n", name, name)
		return 0
	}
	return refuse(stderr, "help", "unknown verb "+oneline.Escape(name)+"; run: nova-sprint help")
}

// parse is the verb's flags anywhere among its words; words after -- are
// taken as they are.
func parse(fs *flag.FlagSet, args []string) ([]string, error) { return parseEach(fs, args, nil) }

// parseEach is parse, telling each (when set) of each flag the words give: its
// name, where it begins and how many words it is (1, or 2 with its value). It hands the flag
// package one flag at a time, its value with it, so the flag package never sees
// the -- that ends the flags (it would end its parse there, and the words after
// it would be read as flags again): every word after a -- is taken as it is, and
// a -- that is a flag's value is that value.
func parseEach(fs *flag.FlagSet, args []string, each func(name string, at, n int)) ([]string, error) {
	var pos []string
	for i := 0; i < len(args); {
		w := args[i]
		switch {
		case w == "--":
			return append(pos, args[i+1:]...), nil
		case len(w) < 2 || w[0] != '-':
			pos = append(pos, w)
			i++
			continue
		}
		n := 1
		name, _, inline := strings.Cut(strings.TrimPrefix(w[1:], "-"), "=")
		if f := fs.Lookup(name); f != nil && !inline && i+1 < len(args) {
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
				n = 2
			}
		}
		if err := verbflag.Parse(fs, args[i:i+n]); err != nil {
			if strings.Contains(err.Error(), "flag provided but not defined: -prefix") {
				return nil, errNoPrefix
			}
			return nil, flagRefusal(fs, err)
		}
		if each != nil {
			each(name, i, n)
		}
		i += n
	}
	return pos, nil
}

// flagError is a flag-parse refusal already worded as the verb's whole line, `unknown flag
// --x; run: nova-sprint help <verb>`: a caller that wraps an error in its own words
// (argErr) and refuse, which appends the verb's -h pointer, leave it as it is.
type flagError struct{ msg string }

func (e *flagError) Error() string { return e.msg }

// flagRefusal words a flag package's parse error once: a flag the verb does not define
// is `unknown flag --x`, a flag missing its value is `--x wants a value`, each with the
// verb's help to run; a value that does not parse names the flag and what it wants
// (verbflag.Explain).
func flagRefusal(fs *flag.FlagSet, err error) error {
	name := fs.Name()
	if w := strings.Fields(name); len(w) > 0 && !slices.ContainsFunc(verbs, func(v verb) bool { return v.name == name }) {
		name = w[0]
	}
	help := "; run: " + prog + " help " + name
	const undefined, needs = "flag provided but not defined: ", "flag needs an argument: "
	switch msg := err.Error(); {
	case strings.HasPrefix(msg, undefined):
		// the nearest flag and the flags the verb takes, never the flag package's line
		// (tool ledger X2, the tool-answers rule)
		return &flagError{verbflag.Explain(fs, err) + help}
	case strings.HasPrefix(msg, needs):
		return &flagError{"-" + strings.TrimPrefix(msg, needs) + " wants a value" + help}
	}
	// a value that does not parse names the flag and what it wants, as its own whole
	// line: a verb's words are never glued in front of it ("takes no words invalid value")
	return &flagError{verbflag.Explain(fs, err) + help}
}

// bindCapAlias keeps --limit as the name of --max for one release (docs/STANDARD.md
// section 2, one cap flag). It sets the same value; applyCapAlias prints the note.
func bindCapAlias(fs *flag.FlagSet, usage string) {
	if f := fs.Lookup("max"); f != nil {
		f.Usage = usage
	}
	fs.Func("limit", "alias of --max, a whole number, accepted for one release", func(s string) error {
		return fs.Set("max", s)
	})
}

// applyCapAlias copies a given --max into the selection count, and when the old
// name was the one given prints NOTE --limit is --max on stderr.
func applyCapAlias(fs *flag.FlagSet, stderr io.Writer, sel *int, list int) {
	var limitSet, maxSet bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "limit":
			limitSet = true
		case "max":
			maxSet = true
		}
	})
	if maxSet || limitSet {
		*sel = list
	}
	if limitSet {
		fmt.Fprintln(stderr, "NOTE --limit is --max")
	}
}

// sel is the set flags of a verb.
type sel struct {
	stream, col string
	limit       int
	group       string // an inbox group's id
	expect      int    // the group's size when it was printed; 0 is not given
	one         bool   // rework and drop: the one card named is meant, though the inbox holds a group naming it
	repo        listFlag
}

func (s *sel) register(fs flagSet, withCol bool) {
	fs.StringVar(&s.stream, "stream", "", "the cards of one stream")
	if withCol {
		fs.StringVar(&s.col, "col", "", "the cards in one column (a state)")
	}
	bindCapAlias(fs, "listed items of each kind (0 is all); when given, at most n cards, in work order; accept and ask take them in stream turns from the work table's stream index")
	fs.StringVar(&s.group, "group", "", "the members of the inbox group of this id (the id inbox prints; a group number is refused)")
	fs.IntVar(&s.expect, "expect", 0, "with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes")
	fs.BoolVar(&s.one, "one", false, "rework and drop: act on the one card named though the inbox holds a judgment group of several naming it (refused without it: the group is answered whole)")
}

func (s *sel) sel(ids []string) sprint.Sel {
	return sprint.Sel{IDs: ids, Stream: s.stream, Col: s.col, Limit: s.limit}
}

func answers(s string) []string { return sprint.Split(s) }

// listFlag is a flag given again or comma separated: every value, in order.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

func (l *listFlag) Set(v string) error {
	*l = append(*l, sprint.Split(v)...)
	return nil
}

// stringList is a flag given again: every value, in order, as given (no comma
// split: a file name may hold one).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// verbSetup is the flag set of a store verb with the common flags.
func (a *app) verbSetup(name string) (flagSet, *common) {
	fs := verbflag.New(name)
	c := &common{verb: name}
	c.register(fs, a.getenv)
	if stepDryRun[name] {
		fs.BoolVar(&c.dry, "dry-run", false, stepDryWords)
	}
	return fs, c
}

// groupIDs is the members of the inbox group of the id, with the inbox read.
func groupIDs(ctx context.Context, st *store.Store, id string) (store.InboxView, sprint.Group, error) {
	v, err := st.Inbox(ctx, defaultDeadline, defaultStale, 10000)
	if err != nil {
		return v, sprint.Group{}, err
	}
	if isNumber(id) {
		return v, sprint.Group{}, fmt.Errorf("group numbers are not accepted: a group is named by its id, which does not move; %s", groupList(v.Groups))
	}
	g, ok := sprint.FindGroup(v.Groups, id)
	if !ok && sprint.IDEpoch(id) != st.PinnedEpoch() {
		return v, g, errors.New(sprint.OtherEpoch(id, sprint.IDEpoch(id), st.PinnedEpoch()))
	}
	if !ok {
		return v, g, fmt.Errorf("no inbox group %s now (answered, or its oldest notification closed); %s", id, groupList(v.Groups))
	}
	return v, g, nil
}

func isNumber(s string) bool {
	_, err := strconv.Atoi(strings.TrimSpace(s))
	return err == nil
}

// groupList is the inbox's groups by id, for a refusal.
func groupList(groups []sprint.Group) string {
	if len(groups) == 0 {
		return "the inbox is empty; run: nova-sprint inbox"
	}
	var ids []string
	for _, g := range groups {
		ids = append(ids, fmt.Sprintf("%s (%s, %s, size %d)", g.ID, g.Type, dashed(g.Stream), g.Size))
	}
	return "the groups now: " + strings.Join(ids, "; ") + "; run: nova-sprint inbox"
}

// groupChange is how a group differs from what the coordinator saw, told by
// the notifications the verb answers: added is members of notifications it
// does not name, gone is subjects of the ones it names that are no longer open.
func groupChange(v store.InboxView, g sprint.Group, answers []string) (added, gone []string) {
	named := map[string]bool{}
	for _, a := range answers {
		named[a] = true
	}
	in := map[string]bool{}
	for _, id := range g.Notes {
		in[id] = true
	}
	open := map[string]bool{}
	byNote := map[string]sprint.Note{}
	for _, o := range v.Open {
		if in[o.Note.ID] || named[o.Note.ID] {
			open[o.Note.ID+"|"+o.Subject()] = true
			byNote[o.Note.ID] = o.Note
		}
	}
	for _, m := range g.Members {
		old := false
		for _, o := range v.Open {
			if named[o.Note.ID] && in[o.Note.ID] && (o.Subject() == m || o.Note.StreamLevel && slices.Contains(o.Note.Primaries, m)) {
				old = true
			}
		}
		if !old {
			added = append(added, m)
		}
	}
	for _, a := range answers {
		n, ok := byNote[a]
		if !ok {
			gone = append(gone, "notification "+a+" (closed)")
			continue
		}
		for _, sub := range n.Subjects() {
			if !open[a+"|"+sub] {
				gone = append(gone, sub)
			}
		}
	}
	return added, gone
}

const (
	defaultDeadline = 10 * time.Minute
	defaultStale    = 30 * time.Minute
)

// epochVerbs are the verbs that act on cards handed to an actor outside the
// sprint: a worker's take by id, finish and progress, a reader's read, a merger's merge
// and a CI observation. Each names the epoch it was handed its cards at
// (--epoch, from queue), so a worker, reader or merger from before a clear
// never reports on the new epoch's card of the same name: a clear moves the
// epoch, and a card of the same name in the new epoch is another card.
// Every other verb is the coordinator's, which acts on the cards it reads in
// the step's own fenced read of the epoch: with no --epoch the step runs at
// the epoch it finds (a clear between the read and the write is read again),
// so the coordinator needs no epoch to name.
var epochVerbs = map[string]bool{"finish": true, "progress": true, "read": true, "stop-return": true, "merge": true, "ci": true, "take by id": true}

// needsEpoch is whether the verb must be given --epoch: the verbs of
// epochVerbs, except a merge run by the sprint's coordinator, which merges
// the cards of its own read of the merge queue and names no handed card.
func needsEpoch(verbName string, coordinator bool) bool {
	return epochVerbs[verbName] && (verbName != "merge" || !coordinator)
}

// runStep runs a step and reports it: exit 0 when everything named moved, 1
// when a card was refused or the step was cut, 2 when the store did not
// confirm.
func (a *app) runStep(verbName string, c common, st *store.Store, step store.Step, stdout, stderr io.Writer) int {
	ctx := context.Background()
	if a.serving && c.epoch < 0 {
		// a worker's write through the server runs at the epoch its worker holds, as the
		// verb parsed it: with none (or one a later word undid) the step could run in a
		// sprint the worker has not read, a clear later (serve.go)
		return refuse(stderr, strings.TrimSuffix(verbName, " by id"), "a worker's verb sent to the server names the epoch its worker holds, --epoch <n> (queue prints it), and this one runs at none; nothing was changed")
	}
	if epochVerbs[verbName] && c.epoch < 0 {
		coordinator := false
		if verbName == "merge" {
			if name, err := st.B.Coordinator(ctx); err == nil {
				coordinator = name != "" && name == c.actor
			}
		}
		if needsEpoch(verbName, coordinator) {
			now := "the sprint's epoch"
			if es, err := st.EpochNow(ctx); err == nil {
				now = fmt.Sprintf("the sprint's epoch is %d", es.N)
			}
			name := strings.TrimSuffix(verbName, " by id")
			return refuse(stderr, name, fmt.Sprintf("a report names the epoch its cards were handed at: --epoch <n> (queue and card print it); %s; nothing was changed", now))
		}
	}
	step.CallerOp = c.op
	if c.epoch >= 0 {
		e := uint64(c.epoch)
		step.Epoch = &e
	}
	if c.dry {
		return a.planDry(verbName, c, st, step, stdout, stderr)
	}
	res, err := st.Run(ctx, step)
	c.says = append(c.says, res.Said...) // what the step said beside its moves (sprint.Plan.Said)
	if c.packets != nil && err == nil {
		c.handed = c.packets(ctx, st, res)
	}
	if c.after != nil && err == nil {
		c.says = append(c.says, c.after(ctx, st, res)...)
	}
	if err != nil || (len(res.Refused) > 0 && len(res.Moved) == 0) {
		c.says = nil // what it would have said is about moves that did not happen
		if err == nil {
			c.says = res.Said
		} // the planner's per-card no-op answers still hold
	}
	return a.report(ctx, verbName, c, st, res, err, stdout, stderr)
}

func token(verbName string) string {
	return strings.ToUpper(strings.ReplaceAll(verbName, " ", "-"))
}

// output is a step's report for a program.
type output struct {
	store.Result
	Error   string `json:"error,omitempty"`
	Unknown bool   `json:"unknown,omitempty"`
	Sprint  string `json:"sprint,omitempty"`
	// Group, with --group: the group's id, how many it acted on, and the
	// size --expect said it had when printed.
	Group    string `json:"group,omitempty"`
	ActedOn  int    `json:"acted_on,omitempty"`
	Expected int    `json:"expected,omitempty"`
	// Packets is what the step hands its actor: take's cards' packets.
	Packets []sprint.Packet `json:"packets,omitempty"`
	// Says is the verb's NOTE lines: what it did that the moves do not say.
	Says []string `json:"says,omitempty"`
	// Brief is add's BRIEF and NOTE brief lines, as the text form prints them.
	Brief []string `json:"brief,omitempty"`
}

// groupReport is what a verb given --group says about the group.
type groupReport struct {
	ID       string
	ActedOn  int
	Expected int
}

// line is the group's line: the count acted on, and the size when printed
// when --expect said it.
func (g groupReport) line() string {
	l := fmt.Sprintf("GROUP %s acted on %d", oneline.Escape(g.ID), g.ActedOn)
	if g.Expected > 0 {
		l += fmt.Sprintf(", the group had %d when printed", g.Expected)
	}
	return l
}

// stepExit is a step's exit code: 0 when everything named moved, 1 when a card
// was refused or the step was cut, 2 when the store did not confirm.
func stepExit(res store.Result, err error) int {
	code := 0
	if len(res.Refused) > 0 {
		code = 1
	}
	var pe *store.PendingError
	var cut *store.CutError
	var cleared *store.ClearedError
	var synced *store.SyncError
	switch {
	case err == nil:
	case errors.As(err, &synced):
		// the write committed: its cards are in the table, so the step is not a failure
	case errors.Is(err, store.ErrUnknown):
		code = 2
	case errors.As(err, &pe), errors.As(err, &cut), errors.As(err, &cleared):
		code = 1
	default:
		code = 2
	}
	return code
}

func (a *app) report(ctx context.Context, verbName string, c common, st *store.Store, res store.Result, err error, stdout, stderr io.Writer) int {
	err = noSprintYet(err)
	code := stepExit(res, err)
	var synced *store.SyncError
	line := sprintLine(ctx, st)
	if c.json {
		o := output{Result: res, Sprint: line, Unknown: errors.Is(err, store.ErrUnknown), Group: c.group.ID, ActedOn: c.group.ActedOn, Expected: c.group.Expected, Packets: c.handed, Says: c.says, Brief: c.brief}
		if o.Moved == nil {
			o.Moved = []string{}
		}
		if o.Refused == nil {
			o.Refused = []sprint.Refusal{}
		}
		if err != nil {
			o.Error = err.Error()
		}
		b, _ := json.Marshal(o)
		fmt.Fprintln(stdout, string(b))
		return code
	}
	for _, r := range res.Repaired {
		fmt.Fprintf(stdout, "REPAIRED %s\n", oneline.Escape(r))
	}
	listed(stdout, "MOVED", res.Moved, c.max, verbName)
	for _, p := range c.handed {
		printPacket(stdout, p)
	}
	if c.group.ID != "" {
		fmt.Fprintln(stdout, c.group.line())
	}
	var why []string
	for _, r := range res.Refused {
		why = append(why, r.Key+": "+r.Why)
	}
	listed(stderr, "REFUSED", why, c.max, verbName)
	status := "OK"
	if code != 0 {
		status = "FAILED"
	}
	fields := fmt.Sprintf("moved=%d refused=%d notes=%d", len(res.Moved), len(res.Refused), res.Notes)
	if verbName == "add" {
		fields = fmt.Sprintf("stream=%s cards=%d before=%s %s", oneline.Field(c.addStream), len(res.Moved), oneline.Field(dashed(c.addBefore)), fields)
	}
	if res.Op != "" {
		fields += " op=" + oneline.Escape(res.Op)
	}
	if res.Replay {
		fields += " replay=yes"
	}
	if res.Pending != "" {
		fields += " pending=" + oneline.Escape(res.Pending)
	}
	if err != nil && !errors.As(err, &synced) {
		changed := "no"
		if errors.Is(err, store.ErrUnknown) {
			changed = "unknown"
		}
		fields += " changed=" + changed
	}
	out := stdout
	if code != 0 {
		out = stderr
	}
	fmt.Fprintf(out, "%s %s %s\n", token(verbName), status, fields)
	for _, s := range c.says {
		fmt.Fprintf(out, "NOTE %s\n", oneline.Escape(s))
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, verbName, oneline.Escape(err.Error()))
	}
	if line != "" {
		fmt.Fprintln(stdout, line)
	}
	return code
}

// listed prints at most max lines of a kind, then a MORE line.
func listed(w io.Writer, kind string, lines []string, max int, verbName string) {
	for i, l := range lines {
		if max > 0 && i == max {
			fmt.Fprintf(w, "MORE kind=%s shown=%d total=%d run: nova-sprint %s ... --max 0\n", strings.ToLower(kind), max, len(lines), verbName)
			return
		}
		fmt.Fprintf(w, "%s %s\n", kind, oneline.Escape(l))
	}
}

// sprintLine is the summary line: landed / all primaries, percent, ETA. A
// STOPPED machine has no ETA, so its line is the STOPPED text the header of
// where shows, then, with cards on the table, landed / all and the percent.
// Every primary landed, the line has no ETA: while the
// machine runs, "N/N 100.0% done in <duration>" from its first start; once it
// has stopped because the sprint is done, "STOPPED  N/N 100.0% done".
func sprintLine(ctx context.Context, st *store.Store) string {
	if st == nil {
		return ""
	}
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil || len(shapes) == 0 {
		return ""
	}
	machine := st.MachineLine(ctx)
	landed, all := counts(shapes[0])
	full := sprintDone(shapes[0])
	state := strings.TrimPrefix(machine, "machine: ")
	switch {
	case state == store.DoneState:
		if full {
			return store.Stopped + "  " + doneLine(shapes[0])
		}
		return store.Stopped + "  " + progress(shapes[0])
	case strings.HasPrefix(state, "STOPPED"):
		if all == 0 && landed == 0 {
			return state
		}
		return state + "  " + progress(shapes[0])
	case full:
		return strings.TrimSpace(doneLine(shapes[0]) + tookSince(ctx, st) + "  " + machine)
	}
	// reads no cards: the rate is the whole sprint's average, over the epoch's
	// landings, an archived stream's too (archiving lands nothing)
	gone, _ := archivedCounts(shapes[0])
	return strings.TrimSpace(summary(shapes[0], 0, etaMinutes(shapes[0], st.LandingRate(ctx, nil, landed+gone))) + "  " + machine)
}

// tookSince is " in <duration>": the wall time from the machine's first start
// of the sprint's epoch to now; empty when it is not known.
func tookSince(ctx context.Context, st *store.Store) string {
	if d, ok := st.SinceFirstStart(ctx); ok {
		return " in " + sprint.TookText(d)
	}
	return ""
}

func (a *app) cmdInit(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("init")
	readers := fs.String("readers", "", "the readers' rows, comma separated")
	members := fs.String("members", "", fmt.Sprintf("fleet members to bring up, comma separated, each <name> or <name>:<width>, its width the most work cards it runs at once; it holds %d times that, ready and working (default %d)", sprint.DealAhead, sprint.DefaultWidth))
	coordinator := fs.String("coordinator", "", "the sprint's coordinator, the one actor who releases sentinels (default: the actor); the seat then moves by coordinator <name>")
	owner := fs.String("owner", "", "the sprint's owner, who may give the seat and whose name a take of it carries (coordinator --take --approved-by); set once, never changed (else "+OwnerEnv+")")
	rules := fs.String("rules", "", "the child rules file every brief is held to: one required sentence per line, its path recorded for the sprint (default: the built-in general rules; add --rules <file> overrides it for one add)")
	attempts := fs.String("attempts", "", fmt.Sprintf("the sprint's attempt cap: how many attempts one brief may run before the card is the coordinator's as a brief defect; 1 to %d (default %d; later: nova-sprint set --attempts <n>)", sprint.AttemptsMax, sprint.AttemptsDefault))
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "init", err.Error())
	}
	if *attempts != "" {
		if _, err := sprint.ParseAttempts(*attempts); err != nil {
			return refuse(stderr, "init", err.Error())
		}
	}
	if len(pos) > 0 {
		return refuse(stderr, "init", "takes no words, found "+pos[0])
	}
	rulesPath := ""
	if *rules != "" {
		// the file is read now, so a file that cannot be a rule set is refused before the sprint exists
		abs, err := filepath.Abs(*rules)
		if err != nil {
			return refuse(stderr, "init", "--rules: "+err.Error())
		}
		if _, err := swarm.ReadChildRules(abs); err != nil {
			return refuse(stderr, "init", "--rules: "+err.Error()+"; one required sentence per line, see `nova-swarm lint --rules`")
		}
		rulesPath = abs
	}
	c.coordinator = *coordinator
	specs, err := sprint.ParseMembers(*members)
	if err != nil {
		return refuse(stderr, "init", "--members: "+err.Error())
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "init", err.Error())
	}
	ctx := context.Background()
	if *owner != "" {
		if !sprint.ValidID(*owner) {
			return refuse(stderr, "init", "--owner wants letters, digits, _ and -: "+*owner)
		}
		was, err := st.Owner(ctx)
		if err != nil {
			return a.readFailed("init", err, stderr)
		}
		if was != "" && was != *owner {
			return refuse(stderr, "init", "the sprint's owner is "+was+", and init does not change the owner; nothing was changed")
		}
	}
	if *coordinator == "" {
		*coordinator = c.actor
	}
	if !sprint.ValidID(*coordinator) {
		return refuse(stderr, "init", "--coordinator wants letters, digits, _ and -: "+*coordinator)
	}
	if err := st.EnsureReaderTiers(ctx); err != nil {
		fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if err := st.Init(ctx); err != nil {
		fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if *owner != "" {
		if err := st.SetOwner(ctx, *owner); err != nil {
			fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
			return 1
		}
	}
	// the key from the seat's record when the seat has moved, else the first
	// coordinator: never from an actor that is not the holder (seat-key-follows-record.w2)
	if _, err := st.InitSeat(ctx, *coordinator); err != nil {
		fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if rs := sprint.Split(*readers); len(rs) > 0 {
		for _, r := range rs {
			if !sprint.ValidID(r) {
				return refuse(stderr, "init", "a reader name wants letters, digits, _ and -: "+r)
			}
		}
		if err := st.B.RowsAdd(ctx, st.Names.Table(sprint.Readers), rs); err != nil {
			fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
			return 1
		}
	}
	if rulesPath != "" {
		if err := st.SetRulesPath(ctx, rulesPath); err != nil {
			fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
			return 1
		}
	}
	if *attempts != "" {
		// the sprint's attempt cap, as set --attempts writes it (the coordinator's: init names them)
		if res, err := st.Run(ctx, store.SetStep(sprint.SetReq{Attempts: *attempts, Who: *coordinator})); err != nil || len(res.Refused) > 0 {
			fmt.Fprintf(stderr, "%s init: --attempts: %v %v\n", prog, err, res.Refused)
			return 1
		}
	}
	readerRows, err := st.ReaderRows(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s init: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	tables := []string{st.Names.Table(sprint.Work), st.Names.Table(sprint.Readers), st.Names.Table(sprint.Merge), st.Names.Table(sprint.Fleet)}
	// with --json the members' steps print nothing of their own: the one object
	// names them (tool ledger P8)
	steps := stdout
	if c.json {
		steps = io.Discard
	} else {
		fmt.Fprintf(stdout, "INIT OK tables=%s view=%s readers=%s\n", strings.Join(tables, ","), st.Names.View(), dashed(strings.Join(readerRows, ",")))
	}
	var memberNames []string
	for _, m := range specs {
		if code := a.runStep("fleet up", *c, st, a.fleetStep(st, "up", m.Name, c.actor, m.Width, false, 0, false), steps, stderr); code != 0 {
			return code
		}
		memberNames = append(memberNames, m.Name)
	}
	notes := []string{}
	if len(specs) > 0 && a.twinOpen(c.redis) {
		// a twin beats every member at every verb (beatTwin): a member added
		// down beats at the next verb and is up from the next tick's presence
		notes = append(notes, "a twin beats every member at every verb: each member added is up after the next nova-sprint tick")
	}
	if c.json {
		sayOK(stdout, true, "init", "", map[string]any{"tables": tables, "view": st.Names.View(), "readers": nonNil(readerRows),
			"members": nonNil(memberNames), "coordinator": *coordinator, "notes": notes})
		return 0
	}
	for _, n := range notes {
		fmt.Fprintln(stdout, "NOTE "+n)
	}
	return 0
}

func (a *app) cmdAdd(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("add")
	stream := fs.String("stream", "", "the stream the primaries belong to, for life; with --count, several streams comma separated, one step")
	count := fs.Int("count", 0, "admit n primaries with generated ids <stream>-<n>")
	needs := fs.String("needs", "", "primaries that must land first, comma separated; each is a primary on the table or of this add (default: the brief's Needs: or DEPENDS-ON: line; with a brief per card, added to each card's own)")
	brief := fs.String("brief", "", fmt.Sprintf("the brief: a child's whole brief, at most %d KiB (the card lint advises %d bytes), held to the card lint (the sentences of the rules file: --rules, else the one init --rules recorded, else the built-in general rules; nova-swarm template --name card prints a card that passes the general ones, nova-swarm lint --rules lists them) and refused, exit 2, nothing written, when it fails; a card with no brief is not linted; a brief that names PATHS, REPO and BASE is also held at the BASE tip (a literal path must exist, a glob must match a file, and every func, type or verb STOP or START names with a file, and a TEST name the tree already holds, must be inside a PATHS file; one line per miss names the nearest file; a new _test file or a NEW: line may be absent); under JEV_API_KEY each card's brief is then asked nova-decide's brief decision (one BRIEF line per card, an uncalibrated rank) and refused under the sprint row's decide_brief_bar, empty by default", cardlimits.MaxBriefBytes>>10, cardlimits.BriefAdvisoryBytes))
	var briefFiles stringList
	fs.Var(&briefFiles, "brief-file", "the brief, read from this file: its bytes as they are, its one trailing newline cut (a brief of many paragraphs), then held to the card lint like --brief; given once with ids, --count or --sentinel, the brief of the cards they name; given alone or again, one card per file in the order given, each card's id its file's name without .md (a1.md is a1); not with --brief or --brief-dir")
	briefDir := fs.String("brief-dir", "", "one card per *.md file in this directory, in byte order of file name, each card's id its file's name without .md (a1.md is a1); not with --brief-file")
	rules := fs.String("rules", "", "the child rules `file`, read at add time: one required sentence per line, [name] sentence names its token (default: the file init --rules recorded, else the built-in general rules); e.g. --rules rules/card.txt. A file the members hold (fleet/child-rules*.txt of this build) is by reference: a card on a repository with a held file (fleet/child-rules.txt for nova-tools, fleet/child-rules.<repo>.txt) need not carry it, the card names the file, and the member injects it at stage time")
	score := fs.String("score", "", "the first primary's score; the rest follow it (default: after every primary)")
	sentinel := fs.String("sentinel", "", "admit a sentinel with this id: a stop the coordinator releases; what sorts after it waits for it")
	before := fs.String("before", "", "place the cards in line in front of this primary of the stream")
	after := fs.String("after", "", "place the cards in line after this primary of the stream")
	every := fs.Int("sentinel-every", 0, "with --count: a sentinel <stream>-gate-<n> after every k cards (a stop by its place in line)")
	last := fs.Bool("sentinel-last", false, "with --sentinel-every: a sentinel after the last card too")
	allowShared := fs.Bool("allow-shared-paths", false, "with a card per brief file (--brief-dir, or --brief-file with no ids): admit cards that name one file in their PATHS: lines though neither needs the other and neither brief declares it on a SHARED: line (by default refused, naming the file and the cards)")
	one := fs.Bool("one", false, "admit a single card (one positional id, --count 1 on one stream, or one --brief-file alone): refused without it, since cards are admitted in waves (--brief-dir, --count 2 or more, several --brief-file)")
	replaces := fs.String("replaces", "", "the card this add admits is the twin of these `ids`, comma separated: it takes over every edge where a waiting card needs one of them (that card needs the twin instead, in the same place), each still on the table is dropped with the reason \"replaced by <the new id>\", and no blocked judgment is raised for it, in one step; a card dropped before is replaced too, and its blocked judgments are answered; one card only (it means --one), never a sentinel")
	allowPersonal := fs.Bool("allow-personal-base", false, "admit cards whose brief's BASE: is a personal branch (<name>/* for the sprint's coordinator, its owner or a friends table row), by default refused naming the base and this flag: no sprint watches a personal branch's gate (docs/SPEC-SPRINT.md section 11, bases-view-r.w2)")
	held := fs.Bool("held", false, "admit the cards held: waiting, a sentinel never reached and no card dealt, nothing raised, until nova-sprint release <id> --reason <text>; a wave loads behind a held sentinel with nothing before it")
	decideRecord := fs.String("decide-record", "", "the record `file` of the cards' brief decisions under JEV_API_KEY (default ~/nova-sprint/decide/brief.jsonl, the coordinator's root); each card stores it and its op, and land and drop attach the card's end there")
	var briefOps stringList
	fs.Var(&briefOps, "brief-op", "`id=op`: a card's brief decision op id (<id>@brief-<hex>), which add sends its server itself when it asked the decision where it was typed; refused when typed on an add no server runs; repeated, one per card")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "add", err.Error())
	}
	if len(briefOps) > 0 && a.serveAddr == "" {
		return refuse(stderr, "add", briefOpWord)
	}
	if *replaces != "" {
		*one = true // a twin is one card
	}
	if why := reservedCardID(append(ids, *sentinel)...); why != "" {
		return refuse(stderr, "add", why)
	}
	if *count < 0 {
		// a negative count admitted no card and opened the stream with an OK
		return refuse(stderr, "add", fmt.Sprintf("--count wants the number of cards to admit, at least 1, got %d", *count))
	}
	// The many-brief form: --brief-dir <dir>, or --brief-file given alone or
	// again, names one brief file per card, its id the file's name. One
	// --brief-file with ids, --count or --sentinel is the one brief for every
	// card they name.
	if *briefDir != "" || len(briefFiles) > 1 || len(briefFiles) == 1 && len(ids) == 0 && *count == 0 && *sentinel == "" {
		if *briefDir == "" && len(briefFiles) == 1 && !*one {
			return refuse(stderr, "add", oneCardWhy(*stream))
		}
		if *briefDir != "" && len(briefFiles) > 0 {
			return refuse(stderr, "add", "--brief-dir and --brief-file are two ways to name the brief files: give one")
		}
		if *brief != "" {
			return refuse(stderr, "add", "--brief and --brief-dir (or --brief-file, a card per file) are two ways to give the brief: give one")
		}
		if len(ids) > 0 {
			return refuse(stderr, "add", "takes no ids with --brief-dir or a repeated --brief-file: the cards are the files")
		}
		if *count != 0 {
			return refuse(stderr, "add", "--count names the cards by number, and --brief-dir or a repeated --brief-file names them by file: give one")
		}
		if *every != 0 || *last {
			return refuse(stderr, "add", "--sentinel-every goes with --count, not a card per brief file")
		}
		return a.cmdAddMany(*stream, *needs, *briefDir, briefFiles, *sentinel, *rules, *score, *before, *after, *held, *allowShared, *allowPersonal, *decideRecord, briefOps, sprint.Split(*replaces), c, stdout, stderr)
	}
	if len(briefFiles) == 1 {
		if *brief != "" {
			return refuse(stderr, "add", "--brief and --brief-file are two ways to give the brief: give one")
		}
		text, err := readBriefFile(briefFiles[0])
		if err != nil {
			return refuse(stderr, "add", "--brief-file: "+err.Error())
		}
		*brief = text
	}
	if *sentinel != "" {
		if len(ids) > 0 || *count != 0 {
			return refuse(stderr, "add", "--sentinel <id> admits one sentinel, with no other ids or --count")
		}
		ids = []string{*sentinel}
	}
	if *stream == "" || (len(ids) == 0) == (*count == 0) {
		return refuse(stderr, "add", "wants --stream and either ids, --count <n> or --sentinel <id> (or --brief-dir <dir>, or --brief-file: a card per file)")
	}
	streams := sprint.Split(*stream)
	// a single card: one id, or --count 1 on one stream (on several it is one card each)
	if (len(ids) == 1 && *sentinel == "" || *count == 1 && len(streams) == 1) && !*one {
		return refuse(stderr, "add", oneCardWhy(*stream))
	}
	if *last && *every == 0 {
		return refuse(stderr, "add", "--sentinel-last goes with --sentinel-every <k>")
	}
	if len(streams) > 1 && *count == 0 {
		return refuse(stderr, "add", "several streams take --count <n>: each gets n cards")
	}
	// A BRIEF IS A CHILD'S WHOLE BRIEF, AND THE CARD LINT HOLDS IT TO THE RULES OF ONE: the
	// rules are the coordinator's (internal/swarm/lintchild.go), from --rules, else the file
	// init recorded, else the general defaults, and they are checked here in process, before
	// anything is written. A card with no brief (a --count card, a sentinel) carries none to
	// check.
	var st *store.Store
	if *rules != "" && *brief == "" {
		return refuse(stderr, "add", "--rules is the rule set a brief is held to, and this add gives no brief; give --brief or --brief-file")
	}
	var rs0 ruleSet
	if *sentinel == "" && *brief != "" {
		var code int
		if rs0, code = a.holdBrief("add", *brief, *rules, c, &st, stderr); code != 0 {
			return code
		}
		c.says = append(c.says, unfilledSays("the brief", *brief)...)
	}
	cardNeeds := sprint.Split(*needs)
	if *needs == "" {
		cardNeeds = briefNeeds(*brief) // its Needs: or DEPENDS-ON: line, when --needs is not given
	}
	var rs []sprint.AddReq
	for _, sn := range streams {
		r := sprint.AddReq{Stream: sn, IDs: ids, Count: *count, Needs: cardNeeds, Brief: *brief, Rules: cardRules(*brief, rs0).held, Base: swarm.ReadCardBase([]byte(*brief)).Ref, Repo: swarm.ReadCardBase([]byte(*brief)).Named, Who: c.actor,
			Sentinel: *sentinel != "", Before: *before, After: *after, Every: *every, Last: *last, Held: *held, Replaces: sprint.Split(*replaces)}
		if *score != "" {
			f, err := strconv.ParseFloat(*score, 64)
			if err != nil {
				return refuse(stderr, "add", "--score wants a number")
			}
			r.Score = &f
		}
		rs = append(rs, r)
	}
	if *sentinel == "" && *brief != "" && len(ids) > 0 { // the cards named, each with the brief (briefdecide.go)
		cards := map[string]string{}
		for _, id := range ids {
			cards[id] = *brief
		}
		asked, code := a.briefGate(cards, *decideRecord, briefOps, c, stdout, stderr)
		if code != 0 {
			return code
		}
		for i := range rs {
			rs[i].BriefOps, rs[i].BriefRecord = asked.ops, asked.record
		}
	}
	if a.gateOnly != nil {
		return 0 // the served add's checks and brief decisions ran here; the server writes
	}
	if st == nil {
		var err error
		if st, err = a.store(*c); err != nil {
			return refuse(stderr, "add", err.Error())
		}
	}
	if *brief == "" && *sentinel == "" {
		c.says = append(c.says, "the cards have no brief, so a worker is handed no task with them; give each one before it is dealt, on a STOPPED machine: nova-sprint brief <id> --brief-file <path>")
	}
	if code := a.holdWho("add", st, stderr, []string{*stream}, *brief); code != 0 {
		return code
	}
	if code := a.holdBase("add", st, *allowPersonal, stderr, *brief); code != 0 {
		return code
	}
	if code := holdTlaRecords("add", stderr, briefCheck{id: strings.Join(ids, ","), brief: *brief}); code != 0 {
		return code
	}
	if *sentinel == "" {
		checks := make([]briefCheck, 0, len(ids))
		for _, id := range ids {
			checks = append(checks, briefCheck{id: id, brief: *brief})
		}
		if len(ids) == 0 { // --count: the ids are made at the write
			checks = append(checks, briefCheck{brief: *brief})
		}
		if code := a.holdCardChecks("add", st, stderr, checks...); code != 0 {
			return code
		}
		if code := a.holdBriefBase("add", st, *allowPersonal, stderr, checks...); code != 0 {
			return code
		}
		if code := a.holdPathsAdmit("add", stderr, checks...); code != 0 {
			return code
		}
	}
	c.addStream = *stream
	c.addBefore = *before
	step := store.AddEachStep(rs)
	if len(rs) == 1 {
		step = store.AddStep(rs[0])
	}
	return a.runStep("add", *c, st, promotionGuard(step, rs), stdout, stderr)
}

// promotionGuard is the add step refused whole, nothing written, when any card it admits
// is cut on a protected branch, dev or main, outside the promotion stream
// (sprint.PromotionRefusals; docs/SPEC-SPRINT.md section 7, protected-bases-pb-b.w2). It
// plans on the step's own snapshot, so the stream's mark is read with the tables it writes.
func promotionGuard(step store.Step, rs []sprint.AddReq) store.Step {
	plan := step.Plan
	step.Plan = func(s *sprint.Snapshot) sprint.Plan {
		if refused := sprint.PromotionRefusals(s, rs); len(refused) > 0 {
			return sprint.Plan{Refused: refused}
		}
		return plan(s)
	}
	return step
}

// cmdAddMany is add --brief-dir <dir>, or add with --brief-file given again:
// one card per brief file, in byte order of the directory's *.md files or in
// the order the files were named. Every brief is read and linted first (one
// failing brief refuses the whole call, exit 2, nothing written), and one
// store write adds every card.
func (a *app) cmdAddMany(stream, needs, briefDir string, briefFiles []string, sentinel, rules, score, before, after string, held, allowShared, allowPersonal bool, decideRecord string, briefOps, replaces []string, c *common, stdout, stderr io.Writer) int {
	if stream == "" {
		return refuse(stderr, "add", "wants --stream and --brief-dir <dir> or --brief-file <file>...")
	}
	if strings.Contains(stream, ",") {
		return refuse(stderr, "add", "--brief-dir and a repeated --brief-file name the cards by file in one stream: give one stream")
	}
	files, code := a.briefFiles(briefDir, briefFiles, stderr)
	if code != 0 {
		return code
	}
	var st *store.Store
	rs, code := a.briefRules("add", rules, c, &st, stderr)
	if code != 0 {
		return code
	}
	extra := sprint.Split(needs)
	cards := make([]sprint.CardAdd, 0, len(files))
	for _, path := range files {
		id := strings.TrimSuffix(filepath.Base(path), ".md")
		if why := reservedCardID(id); why != "" {
			return refuse(stderr, "add", path+": "+why)
		}
		if !sprint.ValidID(id) {
			return refuse(stderr, "add", fmt.Sprintf("%s: the card id is the file's base name without .md, and %q is not one (letters, digits, _ and -)", path, id))
		}
		text, err := readTextFile(path, briefReadCap)
		if err != nil {
			return refuse(stderr, "add", fmt.Sprintf("%s: %v", path, err))
		}
		// an empty file here is linted, and the lint says it once, naming the file
		briefText := strings.TrimSuffix(text, "\n")
		if len(briefText) > store.MaxBriefBytes {
			return refuse(stderr, "add", fmt.Sprintf("%s: the brief is %d bytes, over the %d bytes a brief may be; a brief is a child's whole brief; shorten it", path, len(briefText), store.MaxBriefBytes))
		}
		cards = append(cards, sprint.CardAdd{ID: id, Brief: briefText, Needs: uniquify(append(briefNeeds(briefText), extra...)), File: path, Base: swarm.ReadCardBase([]byte(briefText)).Ref, Repo: swarm.ReadCardBase([]byte(briefText)).Named})
	}
	if !allowShared {
		if why := sharedPaths(cards); why != "" {
			return refuse(stderr, "add", why)
		}
	}
	// Every brief is linted first: one failing brief refuses the whole call,
	// nothing written, every failing file named with its findings.
	if code := lintBriefFiles("add", cards, rs, c.max, stderr); code != 0 {
		return code
	}
	for i := range cards {
		cards[i].Rules = cardRules(cards[i].Brief, rs).held // each card names the rules the member injects into it
	}
	var at *float64
	if score != "" { // every argument is checked before the brief decision asks (briefdecide.go)
		f, err := strconv.ParseFloat(score, 64)
		if err != nil {
			return refuse(stderr, "add", "--score wants a number")
		}
		at = &f
	}
	briefs := map[string]string{}
	for _, cd := range cards {
		briefs[cd.ID] = cd.Brief
	}
	asked, code := a.briefGate(briefs, decideRecord, briefOps, c, stdout, stderr)
	if code != 0 || a.gateOnly != nil {
		return code // refused; or the served add's checks and brief decisions ran here and the server writes
	}
	// --sentinel <id> admits a stop after every card of the call: the sentinel
	// sorts after the cards, and what sorts after it waits for it.
	if sentinel != "" {
		cards = append(cards, sprint.CardAdd{ID: sentinel, Sentinel: true})
	}
	if st == nil {
		s, err := a.store(*c)
		if err != nil {
			return refuse(stderr, "add", err.Error())
		}
		st = s
	}
	texts := make([]string, len(cards))
	streams := make([]string, len(cards))
	for i, cd := range cards {
		texts[i] = cd.Brief
		streams[i] = stream
	}
	if code := a.holdWho("add", st, stderr, streams, texts...); code != 0 {
		return code
	}
	if code := a.holdBase("add", st, allowPersonal, stderr, texts...); code != 0 {
		return code
	}
	checks := make([]briefCheck, len(cards))
	for i, cd := range cards {
		checks[i] = briefCheck{id: cd.ID, brief: cd.Brief}
	}
	if code := holdTlaRecords("add", stderr, checks...); code != 0 {
		return code
	}
	if code := a.holdCardChecks("add", st, stderr, checks...); code != 0 {
		return code
	}
	if code := a.holdBriefBase("add", st, allowPersonal, stderr, checks...); code != 0 {
		return code
	}
	if code := a.holdPathsAdmit("add", stderr, checks...); code != 0 {
		return code
	}
	r := sprint.AddReq{Stream: stream, Cards: cards, Who: c.actor, Before: before, After: after, Held: held, Score: at, BriefOps: asked.ops, BriefRecord: asked.record, Replaces: replaces}
	c.says = append(c.says, fmt.Sprintf("each card's id is its brief file's name without .md (%s is %s)", files[0], cards[0].ID))
	for _, cd := range cards {
		c.says = append(c.says, unfilledSays("the brief of "+cd.ID, cd.Brief)...)
	}
	c.addStream = stream
	c.addBefore = before
	return a.runStep("add", *c, st, promotionGuard(store.AddStep(r), []sprint.AddReq{r}), stdout, stderr)
}

// briefFiles is the brief files of a many-brief add, in order: the *.md files
// of dir in byte order of file name, or the named files in the order given. A
// directory with no *.md file is refused naming the directory.
func (a *app) briefFiles(dir string, files []string, stderr io.Writer) ([]string, int) {
	if dir == "" {
		return append([]string(nil), files...), 0
	}
	out, err := decide.CardFilePaths(dir) // nova-decide brief --card <dir> reads the same cards
	if err != nil {
		return nil, refuse(stderr, "add", "--brief-dir: "+err.Error())
	}
	if len(out) == 0 {
		return nil, refuse(stderr, "add", fmt.Sprintf("--brief-dir %s holds no *.md file", dir))
	}
	return out, 0
}

// briefNeeds is the needs a brief names: the first `Needs:` header line (read
// by cardhdr.KeyValue), else the DEPENDS-ON: line of its typed header block
// (swarm.CardHeaderValue; nova-tools#5096 item 17), its ids comma separated,
// each cut at an opening parenthesis. "none" or "-" (also after the cut, so
// "none (first card)"), an owner/repo#n reference (a pull request or issue,
// not a card), or no such line is no needs.
func briefNeeds(brief string) []string {
	value, found := "", false
	for _, line := range strings.Split(brief, "\n") {
		if key, v, ok := cardhdr.KeyValue(line); ok && key == "Needs" {
			value, found = v, true
			break
		}
	}
	if !found {
		value, _ = swarm.CardHeaderValue([]byte(brief), "DEPENDS-ON")
	}
	var out []string
	for _, id := range strings.Split(value, ",") {
		if cut, _, ok := strings.Cut(id, "("); ok {
			id = cut
		}
		switch id = strings.TrimSpace(id); {
		case id == "", id == "-", id == "none", strings.Contains(id, "#"):
			continue
		}
		out = append(out, id)
	}
	return out
}

// sharedPaths is the refusal of a many-brief add whose cards name one file in
// their PATHS: header lines (swarm.CardHeaderValue; commas or blanks between them)
// when neither needs the other through the needs of the add: they would edit
// it at once (nova-tools#5096 item 17). "" when none does.
func sharedPaths(cards []sprint.CardAdd) string {
	needs := map[string][]string{}
	for _, c := range cards {
		needs[c.ID] = c.Needs
	}
	// reaches says a needs b, through the needs of the add's cards
	var reaches func(a, b string, seen map[string]bool) bool
	reaches = func(a, b string, seen map[string]bool) bool {
		if seen[a] {
			return false
		}
		seen[a] = true
		for _, n := range needs[a] {
			if n == b || reaches(n, b, seen) {
				return true
			}
		}
		return false
	}
	byPath := map[string][]string{}
	var order []string
	// shared is each card's SHARED: header line: the files it declares it edits
	// beside other cards of the add (a ledger every card touches)
	shared := map[string]map[string]bool{}
	for _, c := range cards {
		shared[c.ID] = map[string]bool{}
		for _, p := range headerPaths(c.Brief, "SHARED") {
			shared[c.ID][p] = true
		}
		for _, p := range headerPaths(c.Brief, "PATHS") {
			if slices.Contains(byPath[p], c.ID) {
				continue
			}
			if len(byPath[p]) == 0 {
				order = append(order, p)
			}
			byPath[p] = append(byPath[p], c.ID)
		}
	}
	var clash []string
	for _, p := range order {
		ids := byPath[p]
		for i := range ids {
			for _, b := range ids[i+1:] {
				if a := ids[i]; !reaches(a, b, map[string]bool{}) && !reaches(b, a, map[string]bool{}) && (!shared[a][p] || !shared[b][p]) {
					clash = append(clash, fmt.Sprintf("%s is named in PATHS by %s and %s, and neither needs the other", p, a, b))
				}
			}
		}
	}
	if len(clash) == 0 {
		return ""
	}
	return strings.Join(clash, "; ") + "; two cards that edit one file at once conflict at the merge: chain them (DEPENDS-ON: or Needs: in the later brief), declare the file on a SHARED: line of both briefs, or give --allow-shared-paths"
}

// headerPaths is the files a brief's typed header line names (PATHS:, SHARED:),
// commas or blanks between them; none and - name no file.
func headerPaths(brief, key string) []string {
	value, _ := swarm.CardHeaderValue([]byte(brief), key)
	var out []string
	for _, p := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if p != "none" && p != "-" {
			out = append(out, p)
		}
	}
	return out
}

// holdTlaRecords refuses a brief that edits a TLA+ model and does not refresh the TLC
// records (docs/SPEC-SPRINT.md section 11, add-tla-edit-refreshes-records-b.w1): its
// PATHS: or NEW: header lines cover a tla/*.tla model, and either cover no tla/RUNS.tsv
// or no STEP of it names tlacheck merge --keep. Such a card lands records the TLC
// records class test (internal/ci/tlc_records_class_test.go) calls stale on the base.
// A brief that only reads tla/ names no model there and is untouched. One such brief
// refuses the whole call, exit 2, nothing written.
func holdTlaRecords(verbName string, stderr io.Writer, briefs ...briefCheck) int {
	var red []string
	for _, b := range briefs {
		paths := append(headerPaths(b.brief, "PATHS"), headerPaths(b.brief, "NEW")...)
		var models []string
		records := false
		for _, p := range paths {
			if namesModel(p) {
				models = append(models, p)
			}
			records = records || pathCovers(p, tlaRecordsFile)
		}
		if len(models) == 0 {
			continue
		}
		var why []string
		if !records {
			why = append(why, "does not cover "+tlaRecordsFile)
		}
		if !stepsMergeRecords(b.brief) {
			why = append(why, "names no STEP that runs tlacheck merge --keep")
		}
		if len(why) == 0 {
			continue
		}
		who := "the brief"
		if b.id != "" {
			who = "the brief of " + b.id
		}
		red = append(red, fmt.Sprintf("%s edits the TLA+ model %s and %s", who, strings.Join(models, ","), strings.Join(why, " and ")))
	}
	if len(red) == 0 {
		return 0
	}
	return refuse(stderr, verbName, strings.Join(red, "; ")+"; a model edit refreshes the TLC records, or the records class test calls them stale on the base: name "+tlaRecordsFile+" in PATHS and add a STEP that runs the changed groups on a Linux bench, then tlacheck merge --keep "+tlaRecordsFile+", as tla/README.md says (\"Refreshing the records after a model edit\"); nothing was written")
}

// tlaRecordsFile is the TLC records every model's runs are measured into.
const tlaRecordsFile = "tla/RUNS.tsv"

// namesModel says a PATHS entry names a TLA+ model of tla/: a .tla file under it, a
// glob of them (tla/*.tla), or tla/ itself or a glob over it (tla/**, tla/*).
func namesModel(entry string) bool {
	e := strings.TrimSuffix(strings.TrimPrefix(entry, "./"), "/")
	return strings.HasPrefix(e, "tla/") && strings.HasSuffix(e, ".tla") || pathCovers(entry, "tla/Model.tla")
}

// pathCovers says a PATHS entry covers the file: the file itself, a directory over it
// (tla, tla/), a tree glob over it (tla/**, tla/...) or a glob that matches it (tla/*).
func pathCovers(entry, file string) bool {
	e := strings.TrimSuffix(strings.TrimPrefix(entry, "./"), "/")
	if e == file || strings.HasPrefix(file, e+"/") {
		return true
	}
	for _, tree := range []string{"/**", "/..."} {
		if dir, ok := strings.CutSuffix(e, tree); ok && strings.HasPrefix(file, dir+"/") {
			return true
		}
	}
	ok, _ := path.Match(e, file)
	return ok
}

// tlaMergeKeepRE is a command that merges runs onto the kept records: tlacheck merge
// with --keep (/tmp/tlacheck merge --root . --keep tla/RUNS.tsv, tla/README.md).
var tlaMergeKeepRE = regexp.MustCompile(`\btlacheck\s+merge\b[^\n]*\s--keep\b`)

// stepsMergeRecords says some STEP of the brief names tlacheck merge --keep: a STEP is
// a line that begins STEP and the lines under it up to the next blank line.
func stepsMergeRecords(brief string) bool {
	in := false
	for _, line := range strings.Split(brief, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "STEP"):
			in = true
		case t == "":
			in = false
		}
		if in && tlaMergeKeepRE.MatchString(t) {
			return true
		}
	}
	return false
}

// uniquify keeps the first of each id, in order: a need named by a brief and
// again by --needs is stored once.
func uniquify(ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// holdWho holds each brief's WHO line (cardhdr.ReadWho; the owner, 2026-10-03: "doing
// parts on friends where we would normally do friend work"): `WHO: friend` or
// `WHO: friend <name>`, the name a row of the friends table (nova-config's friend rows,
// copied by friend sync), and `WHO: friend` only while the table has a friend. A named
// friend's configured work restriction is held too: the card's stream (streams, one per
// brief, "" when the caller cannot say: no restriction is judged) and its KIND must be
// within her streams and kinds, or the whole call refuses with the restriction named
// (docs/SPEC-SPRINT.md section 1, a friend's card). A brief whose line does not read, or
// names no friend of the table, refuses the whole call, exit 2, nothing written. A brief
// with no WHO line is a machine's, as before.
func (a *app) holdWho(verbName string, st *store.Store, stderr io.Writer, streams []string, briefs ...string) int {
	var specs []store.FriendSpec
	byName := map[string]store.FriendSpec{}
	read := false
	for i, b := range briefs {
		w, why := cardhdr.ReadWho(b)
		if why != "" {
			return refuse(stderr, verbName, "the brief's "+why)
		}
		if !w.Friend {
			continue
		}
		if !read {
			var err error
			if specs, err = st.FriendSpecs(context.Background()); err != nil {
				return a.readFailed(verbName, err, stderr)
			}
			byName = make(map[string]store.FriendSpec, len(specs))
			for _, spec := range specs {
				byName[spec.Name] = spec
			}
			read = true
		}
		stream := ""
		if i < len(streams) {
			stream = streams[i]
		}
		switch {
		case len(specs) == 0:
			return refuse(stderr, verbName, "the brief says WHO: friend, and the friends table has no friend: its rows are nova-config's friend rows; run: nova-sprint friend sync")
		case w.Name != "" && byName[w.Name].Name == "":
			names := make([]string, 0, len(specs))
			for _, spec := range specs {
				names = append(names, spec.Name)
			}
			return refuse(stderr, verbName, fmt.Sprintf("the brief says WHO: friend %s, and %s is no row of the friends table (friends: %s): name one, or write WHO: friend for any; run: nova-sprint friend sync", w.Name, w.Name, strings.Join(names, ",")))
		case w.Name != "" && stream != "":
			if why := byName[w.Name].RestrictionWhy(stream, sprint.BriefKind(b)); why != "" {
				return refuse(stderr, verbName, "the named friend cannot receive this card: "+why)
			}
		}
	}
	return 0
}

// modelLinesWhy is the refusal a brief's model lines get, the same whether the
// brief came in as one or among many: the model lines the deal reads (line 1's
// tier, a model: pin) are read by the one parser the deal and the frame use, so
// a brief the deal would refuse is refused here, before it is admitted.
func modelLinesWhy(why string) string {
	return "the brief's model lines: " + why + "; line 1 names `tier: flash|pro|heavy|frontier`, and a pinned card carries `model: <provider>/<model>` (with `tokens: <n>|unmetered` and `deadline: <seconds>`) under it"
}

// lintBriefReads holds one brief to the card lint's child rules and to its
// model lines: it returns the model-line why ("" when the lines read) and the
// lint findings. The single-brief and many-brief paths both call it, so one
// brief is held the same however it is given.
func lintBriefReads(brief string, rs ruleSet) (modelWhy string, findings []swarm.CardHeaderFinding) {
	if _, why := cardhdr.ReadModel(brief); why != "" {
		return why, nil
	}
	if rs = cardRules(brief, rs); rs.held != "" {
		// rules by reference: the member injects the held file at stage time, so the brief
		// is linted as the child is handed it (nova-tools#5174 rule 6)
		findings = swarm.LintCardChildByReference([]byte(brief), rs.rules)
	} else {
		findings = swarm.LintCardChildWith([]byte(brief), rs.rules)
	}
	// a tree card's steps are held too (internal/cardtree; docs/SPEC-SPRINT.md, a card is
	// a tree of steps): a flat brief has no such finding
	for _, f := range cardtree.Lint(brief) {
		findings = append(findings, swarm.CardHeaderFinding{Check: f.Check, Line: f.Line, Excerpt: f.Excerpt})
	}
	return "", findings
}

// cardRules is the rule set one brief is held to under the add's set rs (nova-tools#5174
// rule 6): under a set the members hold, the held file of the brief's repository by reference
// (swarm.OwnRulesName: fleet/child-rules.txt for this repository, fleet/child-rules.<repo>.txt
// for another that has one; rs itself for a brief naming no repository), and rs carried, as
// before rules by reference, for a repository the members hold no file for; rs carried when
// the members do not hold it. Its held name is the one the card names (sprint.FieldRules).
func cardRules(brief string, rs ruleSet) ruleSet {
	if rs.held == "" {
		return rs
	}
	switch own := swarm.OwnRulesName(brief, rs.held); own {
	case "":
		return ruleSet{rules: rs.rules}
	case rs.held:
		return rs
	default:
		rules, err := swarm.HeldRules(own)
		if err != nil { // own is a file this build holds, so it parses (swarm.TestTheHeldRulesAreTheFleetFile)
			return ruleSet{rules: rs.rules}
		}
		return ruleSet{rules: rules, held: own}
	}
}

// lintBriefFiles holds every brief of a many-brief add, or of brief --dir
// (verbName), to the card lint's child rules and its model lines: one
// failing brief refuses the whole call, exit 2, nothing written, every failing
// file named with its findings, at most max of them (0 is all) before the one
// MORE line.
func lintBriefFiles(verbName string, cards []sprint.CardAdd, rs ruleSet, max int, stderr io.Writer) int {
	if rs.server {
		return 0
	}
	type finding struct {
		file  string
		f     swarm.CardHeaderFinding
		rules []swarm.ChildRule
	}
	var all []finding
	var failed []string
	for _, c := range cards {
		modelWhy, findings := lintBriefReads(c.Brief, rs)
		if modelWhy != "" {
			return refuse(stderr, verbName, c.File+": "+modelLinesWhy(modelWhy))
		}
		if len(findings) == 0 {
			continue
		}
		failed = append(failed, c.File)
		for _, f := range findings {
			all = append(all, finding{c.File, f, cardRules(c.Brief, rs).rules})
		}
	}
	if len(all) == 0 {
		return 0
	}
	printed, more := all, false
	if max > 0 && len(all) > max {
		printed, more = all[:max], true
	}
	for _, x := range printed {
		fmt.Fprintf(stderr, "LINT DRIFT brief %s: %s: %d: %s remedy=%s\n", oneline.Field(x.file), oneline.Field(x.f.Check), x.f.Line,
			oneline.Escape(oneline.Cap(x.f.Excerpt, oneline.TailBytes)), oneline.Escape(swarm.ChildRemedy(x.rules, x.f.Check)))
	}
	if more {
		fmt.Fprintf(stderr, "LINT MORE brief findings=%d remedy=%s --max 0\n", len(all), verbName)
	}
	return refuse(stderr, verbName, fmt.Sprintf("the brief of %s fails the card lint (%s); a brief is a child's whole brief and carries every rule of its rule set (--rules, else the file init --rules recorded, else the general rules); run: nova-swarm template --name card", strings.Join(failed, ", "), findingsCount(len(all))))
}

// ruleSet is the rule set a brief is held to, and held, the base name of the rules file the
// members hold that it is (swarm.HeldRulesText), "" when they hold none: a held set is by
// reference (nova-tools#5174 rule 6), the member injects it at stage time, so the stored brief
// need not carry it and is linted as the child is handed it (lintBriefReads).
type ruleSet struct {
	rules  []swarm.ChildRule
	held   string
	server bool // the sprint's rules are the server's to read and lint with: the half of a served add run here lints nothing
}

// briefRules is the rule set an add holds its brief to: the file --rules names, else the
// file init --rules recorded for the sprint (read through the store, opened once into *st
// for the add to use), else swarm.DefaultChildRules. A file that cannot be read or is no
// rule set is a usage refusal naming it, and so is a file named as one the members hold
// (fleet/child-rules*.txt) whose text is not this build's copy, the one they inject.
func (a *app) briefRules(verbName, file string, c *common, st **store.Store, stderr io.Writer) (ruleSet, int) {
	if file != "" {
		abs, err := filepath.Abs(file)
		if err != nil {
			return ruleSet{}, refuse(stderr, verbName, "--rules: "+err.Error())
		}
		rs, err := swarm.ReadChildRules(abs)
		if err != nil {
			return ruleSet{}, refuse(stderr, verbName, "--rules: "+err.Error())
		}
		return heldSet(verbName, abs, rs, stderr)
	}
	if a.gateOnly != nil {
		return ruleSet{server: true}, 0 // the server holds the sprint's rules file and lints with it
	}
	s, err := a.store(*c)
	if err != nil {
		return ruleSet{}, refuse(stderr, verbName, err.Error())
	}
	*st = s
	path, err := s.RulesPath(context.Background())
	if err != nil {
		return ruleSet{}, a.readFailed(verbName, err, stderr)
	}
	if path == "" {
		return ruleSet{rules: swarm.DefaultChildRules}, 0
	}
	rs, err := swarm.ReadChildRules(path)
	if err != nil {
		return ruleSet{}, refuse(stderr, verbName, "the sprint's rules file (recorded by init --rules) cannot serve: "+err.Error()+"; give --rules <file> for this "+verbName+", or run: nova-sprint init --rules <file>")
	}
	return heldSet(verbName, path, rs, stderr)
}

// heldSet is the rule set read from path, held by reference when its base name is a rules
// file the members hold and its text is this build's copy; a held name whose text differs is
// refused, since the members inject their copy and not the file named.
func heldSet(verbName, path string, rules []swarm.ChildRule, stderr io.Writer) (ruleSet, int) {
	base := filepath.Base(path)
	want, ok := swarm.HeldRulesText(base)
	if !ok {
		return ruleSet{rules: rules}, 0
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return ruleSet{}, refuse(stderr, verbName, "--rules: "+err.Error())
	}
	if !bytes.Equal(got, want) {
		return ruleSet{}, refuse(stderr, verbName, fmt.Sprintf("%s is the rules file fleet/%s the members hold and inject at stage time, and its text is not this build's copy: the members would inject the copy they hold, not this file; install the build that carries it (nova-update), or name a file of another name", path, base))
	}
	return ruleSet{rules: rules, held: base}, 0
}

// holdBrief holds one brief to the card lint under the rule set briefRules
// finds (rules, else the sprint's, else the general ones): add's one brief and
// brief's replacement, so a brief is held the same however it comes. It returns
// the rule set, whose held file (cardRules) the card names.
func (a *app) holdBrief(verbName, brief, rules string, c *common, st **store.Store, stderr io.Writer) (ruleSet, int) {
	rs, code := a.briefRules(verbName, rules, c, st, stderr)
	if code != 0 {
		return ruleSet{}, code
	}
	if code = lintBrief(verbName, brief, rs, c.max, stderr); code != 0 {
		return rs, code
	}
	// recut's new brief is admitted here (recut.go is outside this card's PATHS). add and
	// brief run the same check at their own write, after the holds they already have.
	if verbName == "recut" {
		if code = a.holdPathsAdmit(verbName, stderr, briefCheck{brief: brief}); code != 0 {
			return rs, code
		}
	}
	return rs, 0
}

// readBriefFile is a --brief-file's brief: the file's bytes as they are, read
// whole up to briefReadCap, its one trailing newline cut.
func readBriefFile(path string) (string, error) {
	text, err := readTextFile(path, briefReadCap)
	if err != nil {
		return "", err
	}
	return briefFromFile(path, text)
}

// briefFromFile is a brief file's text, its one trailing newline cut; a file
// that holds nothing but blanks and newlines is refused naming it, since a card
// admitted with no brief is handed no task and is never linted.
func briefFromFile(path, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s holds no brief (it is empty); write the card's whole brief there (nova-swarm template --name card prints one to start from)", path)
	}
	return strings.TrimSuffix(text, "\n"), nil
}

// lintBrief holds one brief to the card lint's child rules and its model lines
// (lintBriefReads): the findings print on stderr in the lint's own grammar, at
// most max of them (0 is all) before a MORE line, and a brief with any is
// refused, exit 2.
func lintBrief(verbName, brief string, rs ruleSet, max int, stderr io.Writer) int {
	if rs.server {
		return 0
	}
	modelWhy, findings := lintBriefReads(brief, rs)
	if modelWhy != "" {
		return refuse(stderr, verbName, modelLinesWhy(modelWhy))
	}
	if len(findings) == 0 {
		return 0
	}
	printed, more := findings, false
	if max > 0 && len(findings) > max {
		printed, more = findings[:max], true
	}
	for _, f := range printed {
		fmt.Fprintf(stderr, "LINT DRIFT brief %s: %d: %s remedy=%s\n", oneline.Field(f.Check), f.Line,
			oneline.Escape(oneline.Cap(f.Excerpt, oneline.TailBytes)), oneline.Escape(swarm.ChildRemedy(cardRules(brief, rs).rules, f.Check)))
	}
	if more {
		fmt.Fprintf(stderr, "LINT MORE brief findings=%d remedy=%s --max 0\n", len(findings), verbName)
	}
	return refuse(stderr, verbName, fmt.Sprintf("the brief fails the card lint (%s); a brief is a child's whole brief and carries every rule of its rule set (--rules, else the file init --rules recorded, else the general rules); run: nova-swarm template --name card", findingsCount(len(findings))))
}

func (a *app) cmdRelease(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("release")
	reason := fs.String("reason", "", "what you looked at and found: recorded on the sentinel or held card, and in a sentinel's notification; a sentinel not yet reached is released when each card it waits for has landed, was dropped, or is in flight (taken, in review or merging), and refused naming the first that has not started")
	ans := fs.String("answers", "", answersWords)
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "release", err.Error())
	}
	if len(ids) == 0 {
		return refuse(stderr, "release", "wants the sentinels or held cards it releases and --reason <text>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "release", err.Error())
	}
	if err := unaliasFlag(context.Background(), st, fs, "answers"); err != nil {
		return refuse(stderr, "release", "--answers: "+err.Error())
	}
	coordinator, err := st.B.Coordinator(context.Background())
	if err != nil {
		return a.readFailed("release", err, stderr)
	}
	return a.runStep("release", *c, st, store.ReleaseStep(sprint.ReleaseReq{IDs: ids, Reason: *reason, Coordinator: coordinator,
		Answers: answers(*ans), Who: c.actor}), stdout, stderr)
}

// withGroup resolves --group into ids: the group of the id, checked against
// --expect. A group of another size than expected is refused, naming what
// changed, and nothing moves.
// oneCardWhy is the refusal of a single-card add (the owner, 2026-10-03: "It feels very much
// like we are dealing cards one at a time. Stop this. BATCH EVERYTHING."): cards are admitted
// in waves, and --one says a single card is meant.
func oneCardWhy(stream string) string {
	if stream == "" {
		stream = "<s>"
	}
	return "one card at a time is the mistake; put the briefs in a directory and run: nova-sprint add --stream " + stream + " --brief-dir <dir>; or say --one for a single card"
}

// oneOfAGroupWhy is the refusal of rework or drop naming one card while the inbox holds a
// judgment group of several that names it: the group is answered whole (the owner,
// 2026-10-03, "BATCH EVERYTHING"), and --one says the one card is meant. It reads the open
// notes alone (store.OpenGroups), never the whole inbox.
func oneOfAGroupWhy(ctx context.Context, verbName string, st *store.Store, id string) (string, error) {
	groups, err := st.OpenGroups(ctx)
	if err != nil {
		return "", err
	}
	for _, g := range groups {
		if g.Kind == sprint.Judgment && len(g.Members) > 1 && slices.Contains(g.Members, id) {
			return fmt.Sprintf("the inbox holds a group of %d for this card; answer the group; run: nova-sprint %s --group %s --expect %d; or say --one", len(g.Members), verbName, g.ID, len(g.Members)), nil
		}
	}
	return "", nil
}

func (a *app) withGroup(verbName string, fs flagSet, c *common, st *store.Store, s *sel, ids []string, stdout, stderr io.Writer) ([]string, int) {
	if s.group == "" {
		if s.expect != 0 && len(s.repo) == 0 {
			return nil, refuse(stderr, verbName, "--expect goes with --group <id>")
		}
		return ids, 0
	}
	if len(ids) > 0 {
		return nil, refuse(stderr, verbName, "takes ids or --group, not both")
	}
	if s.expect < 0 {
		return nil, refuse(stderr, verbName, "--expect wants the group's size, a whole number from 1")
	}
	v, g, err := groupIDs(context.Background(), st, s.group)
	if err != nil {
		if isNumber(s.group) {
			return nil, refuse(stderr, verbName, err.Error())
		}
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, verbName, oneline.Escape(err.Error()))
		return nil, 1
	}
	var ans []string
	if f := fs.Lookup("answers"); f != nil {
		ans = answers(f.Value.String())
	}
	c.group = groupReport{ID: g.ID, ActedOn: len(g.Members), Expected: s.expect}
	if s.expect > 0 && len(g.Members) != s.expect {
		added, gone := groupChange(v, g, ans)
		if c.json {
			b, _ := json.Marshal(map[string]any{"error": "the group changed", "group": g.ID, "size": len(g.Members), "expected": s.expect,
				"added": nonNil(added), "gone": nonNil(gone), "members": nonNil(g.Members), "moved": []string{}})
			fmt.Fprintln(stdout, string(b))
			return nil, 1
		}
		fmt.Fprintf(stderr, "REFUSED group %s: it has %d now, not %d as printed; nothing changed; run: %s inbox --open %s and answer the group as it is now\n", oneline.Escape(g.ID), len(g.Members), s.expect, prog, oneline.Escape(g.ID))
		if ans == nil {
			listed(stderr, "NOW", g.Members, c.max, "inbox --open "+g.ID)
		} else {
			listed(stderr, "ADDED", added, c.max, "inbox --open "+g.ID)
			listed(stderr, "GONE", gone, c.max, "inbox --open "+g.ID)
		}
		fmt.Fprintf(stderr, "%s FAILED moved=0 group=%s size=%d expected=%d; run: nova-sprint inbox --open %s\n", token(verbName), oneline.Escape(g.ID), len(g.Members), s.expect, oneline.Escape(g.ID))
		return nil, 1
	}
	if len(g.Members) == 0 {
		fmt.Fprintf(stderr, "%s %s: group %s has no primaries to act on; run: nova-sprint inbox --open %s\n", prog, verbName, oneline.Escape(g.ID), oneline.Escape(g.ID))
		return nil, 1
	}
	return g.Members, 0
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// setVerb is the shape of the verbs over a set of primaries.
func (a *app) setVerb(verbName string, args []string, stdout, stderr io.Writer, withCol bool, extra func(fs flagSet),
	need func(ids []string, s *sel) string, step func(ids []string, s *sel, c *common) store.Step) int {
	fs, c := a.verbSetup(verbName)
	var s sel
	s.register(fs, withCol)
	if verbName == "drop" {
		// drop alone takes a repository selector: the streams recording it, read from
		// their control cards, acted on only with --expect <n> (docs/SPEC-SPRINT.md
		// section 11, the streams verb)
		fs.Var(&s.repo, "repo", "only the cards of the streams recording this repository (owner/name), comma separated or repeated; needs --expect <n>, the number of streams it selects")
	}
	if extra != nil {
		extra(fs)
	}
	ids, err := parse(fs, args)
	applyCapAlias(fs, stderr, &s.limit, c.max)
	if err != nil {
		return refuse(stderr, verbName, err.Error())
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verbName, err.Error())
	}
	if len(s.repo) > 0 {
		if s.group != "" || s.stream != "" {
			return refuse(stderr, verbName, "--repo and --group or --stream select different sets: give --repo alone")
		}
		streams, err := a.repoStreams(context.Background(), st, s.repo)
		if err != nil {
			return a.readFailed(verbName, err, stderr)
		}
		if len(streams) == 0 {
			return refuse(stderr, verbName, "no stream records "+strings.Join(s.repo, ",")+": nothing to select; run: nova-sprint streams")
		}
		if s.expect == 0 {
			return refuse(stderr, verbName, "--repo selects "+strconv.Itoa(len(streams))+" stream(s) ("+strings.Join(streams, ",")+"): give --expect "+strconv.Itoa(len(streams))+" to act on them, so what it removes is read before it runs")
		}
		if s.expect != len(streams) {
			return refuse(stderr, verbName, "--expect "+strconv.Itoa(s.expect)+" was printed for another set: the repository selects "+strconv.Itoa(len(streams))+" stream(s) ("+strings.Join(streams, ",")+")")
		}
		// The cards of the streams, by id, so one step acts across several streams:
		// the open cards (a --col keeps its column), landed cards left alone.
		s2, err := st.Load(context.Background(), []string{sprint.Work}, nil)
		if err != nil {
			return a.readFailed(verbName, err, stderr)
		}
		for _, stream := range streams {
			for _, col := range sprint.States {
				if !sprint.IsOpen(col) || (s.col != "" && col != s.col) {
					continue
				}
				for _, c := range s2.Work.Cell(stream, col) {
					ids = append(ids, c.ID)
				}
			}
		}
		if len(ids) == 0 {
			// nothing open to act on: name the streams, so the step is an empty plan
			s.stream = strings.Join(streams, ",")
		}
	}
	// a judgment's alias stands for its id in --answers and --group (alias.go)
	if err := unaliasFlag(context.Background(), st, fs, "answers"); err != nil {
		return refuse(stderr, verbName, "--answers: "+err.Error())
	}
	if sprint.IsAlias(s.group) {
		full, err := unalias(context.Background(), st, []string{s.group})
		if err != nil {
			return refuse(stderr, verbName, "--group: "+err.Error())
		}
		s.group = full[0]
	}
	ids, code := a.withGroup(verbName, fs, c, st, &s, ids, stdout, stderr)
	if code != 0 {
		return code
	}
	if (verbName == "rework" || verbName == "drop") && s.group == "" && len(ids) == 1 && !s.one {
		why, err := oneOfAGroupWhy(context.Background(), verbName, st, ids[0])
		if err != nil {
			return a.readFailed(verbName, err, stderr)
		}
		if why != "" {
			return refuse(stderr, verbName, why)
		}
	}
	if need != nil {
		if why := need(ids, &s); why != "" {
			return refuse(stderr, verbName, why)
		}
	}
	before := a.judgedBefore(context.Background(), st, answers(flagValue(fs, "answers")))
	stp := step(ids, &s, c) // first: a step may set what the verb does after it (c.after)
	if len(before) > 0 {
		c.after = a.afterAnswering(verbName, before, flagValue(fs, "reason"), flagValue(fs, "fix"), c.actor, c.after)
	}
	return a.runStep(verbName, *c, st, stp, stdout, stderr)
}

func (a *app) cmdResolve(args []string, stdout, stderr io.Writer) int {
	return a.setVerb("resolve", args, stdout, stderr, false, nil, nil, func(ids []string, s *sel, c *common) store.Step {
		return store.ResolveStep(sprint.ResolveReq{Sel: s.sel(ids), Who: c.actor})
	})
}

// cardGens splits <card>@<gen> words into ids and generations.
func cardGens(words []string) ([]string, map[string]int, error) {
	gens := map[string]int{}
	var ids []string
	for _, w := range words {
		id, g, ok := strings.Cut(w, "@")
		if ok {
			n, err := strconv.Atoi(g)
			if err != nil || n < 1 {
				return nil, nil, fmt.Errorf("%s: a generation is a whole number from 1", w)
			}
			gens[id] = n
		}
		ids = append(ids, id)
	}
	return ids, gens, nil
}

func (a *app) cmdTake(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("take")
	as := fs.String("as", "", "the fleet member taking its cards; several, comma separated, each take from their own ready queue in one step")
	var limit int
	bindCapAlias(fs, "listed items of each kind (0 is all); when given, the first n of its ready queue (omitted, 1); with several members, n of each")
	words, err := parse(fs, args)
	applyCapAlias(fs, stderr, &limit, c.max)
	if err != nil {
		return refuse(stderr, "take", err.Error())
	}
	ids, gens, err := cardGens(words)
	if err != nil {
		return refuse(stderr, "take", err.Error())
	}
	if *as == "" || len(gens) != len(ids) {
		return refuse(stderr, "take", "wants --as <member>, and every card named as <card>@<gen>, the generation from queue --as <member>")
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "take", err.Error())
	}
	name := "take"
	if len(ids) > 0 {
		name = "take by id" // a report on named cards
	}
	members := sprint.Split(*as)
	c.packets = func(ctx context.Context, st *store.Store, res store.Result) []sprint.Packet {
		taken := map[string]bool{}
		for _, m := range res.Moved {
			if f := strings.Fields(m); len(f) > 0 {
				taken[f[0]] = true
			}
		}
		var mine []*sprint.Card
		for _, member := range members {
			cs, err := st.ReadCells(ctx, sprint.Fleet, member, sprint.Working)
			if err != nil {
				return nil
			}
			for _, x := range cs {
				if taken[x.ID] {
					mine = append(mine, x)
				}
			}
		}
		ps, _ := st.Packets(ctx, mine)
		return ps
	}
	if len(ids) == 0 && limit >= 0 {
		c.after = func(ctx context.Context, st *store.Store, res store.Result) []string {
			return takeShort(ctx, st, res, members, max(limit, 1))
		}
	}
	return a.runStep(name, *c, st, store.TakeStep(sprint.TakeReq{Sel: sprint.Sel{IDs: ids, Limit: limit}, As: *as, Gens: gens, Who: *as}), stdout, stderr)
}

// takeShort is why a take by count took fewer than asked, one line per member
// it names: the member is not up, it is at its width (the width is hard: it
// takes another as one is finished, sprint.Take), or its ready queue is
// empty. A member that took what it asked says nothing.
func takeShort(ctx context.Context, st *store.Store, res store.Result, members []string, asked int) []string {
	taken := map[string]bool{}
	for _, m := range res.Moved {
		if f := strings.Fields(m); len(f) > 0 {
			taken[f[0]] = true
		}
	}
	s, err := st.Load(ctx, []string{sprint.Fleet}, nil)
	if err != nil {
		return []string{"why the take took what it did is not known: the fleet table did not read: " + err.Error()}
	}
	var out []string
	for _, m := range members {
		working := s.Fleet.Cell(m, sprint.Working)
		n := 0
		for _, x := range working {
			if taken[x.ID] {
				n++
			}
		}
		if n >= asked {
			continue
		}
		head := fmt.Sprintf("%s took %d of the %d asked: ", m, n, asked)
		if sprint.IsFriendRow(m) {
			// a friend's row has no control card: her status and her lanes are her friends row's
			if len(s.Fleet.Cell(m, sprint.Ready)) == 0 {
				out = append(out, head+"its ready queue is empty")
			} else {
				out = append(out, fmt.Sprintf("%sher lanes are full (%d working) or she is not up; she takes another as she finishes one", head, len(working)))
			}
			continue
		}
		if !s.Fleet.HasRow(m) {
			// a name the fleet table lacks takes nothing, and says so rather than an OK alone
			out = append(out, head+"it is no member of the fleet table (members: "+orDashStr(strings.Join(s.Members(), ","), "none")+"); run: nova-sprint fleet up "+m+" --width <n>")
			continue
		}
		switch status := s.MemberCtl(m).F("status"); {
		case status != sprint.Up:
			out = append(out, head+"it is "+orDashStr(status, "-")+", and only a member up takes")
		case len(working) >= s.Width(m):
			out = append(out, fmt.Sprintf("%sit is at its width, %d working of %d; it takes another as it finishes one", head, len(working), s.Width(m)))
		case len(s.Fleet.Cell(m, sprint.Ready)) == 0:
			out = append(out, head+"its ready queue is empty")
		}
	}
	return out
}

func (a *app) cmdFinish(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("finish")
	as := fs.String("as", "", "the fleet member finishing its cards; several, comma separated, each finishing its own named cards in one step")
	failed := fs.Bool("failed", false, "the work failed (default: ok); a card named by id whose attempt a deadline failed already, with no later attempt started, is finished by this report, LAND or HOLD, rather than refused")
	head := fs.String("head", "", "the commit the work finished at, the head land merges (default: the card's id, for a run with no git: land refuses a head that is not a commit id)")
	report := fs.String("report", "", "the worker's report")
	branch := fs.String("branch", "", "the branch the work is on (its packet names the one to use)")
	baseBranch := fs.String("base", "", "the branch the work started from")
	usage := fs.String("usage", "", "what the run spent, one line (the member passes its child's budget, wall, tokens by class and cost): kept on the attempt's record, timed and priced")
	decision := fs.String("decision", "", "the take's attempt decision, one JSON record line as nova-decide makes it (a work member with JEV_API_KEY asks it for every take): its op naming this take's card and attempt, else the finish is refused; kept on the card, recorded by the server's decide lane, and a failed finish whose class is no-result or nothing-to-do at or above that class's bar on the card is routed by it (docs/SPEC-SPRINT.md section 2)")
	words, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "finish", err.Error())
	}
	var decided string
	var d decide.Decision
	if *decision != "" {
		if d, err = decide.ParseAttempt([]byte(*decision)); err == nil {
			var dec decide.Decided
			dec, err = decide.AttemptDecided(d)
			decided = dec.String()
		}
		if err != nil {
			return refuse(stderr, "finish", "--decision: "+err.Error())
		}
	}
	ids, gens, err := cardGens(words)
	if err != nil {
		return refuse(stderr, "finish", err.Error())
	}
	if *as == "" || len(ids) == 0 || len(gens) != len(ids) {
		return refuse(stderr, "finish", "wants --as <member> and every card as <card>@<gen>, the generation the worker holds")
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "finish", err.Error())
	}
	if decided != "" && a.decide != nil {
		// the decide lane records it (decidelane.go) when the finish moved its card: a finish
		// refused (one naming another take's decision among them) records nothing
		c.after = func(_ context.Context, _ *store.Store, res store.Result) []string {
			if len(res.Moved) > 0 {
				a.decide.put(d)
			}
			return nil
		}
	}
	return a.runStep("finish", *c, st, store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: ids}, As: *as, Gens: gens, Failed: *failed,
		Head: *head, Report: *report, Branch: *branch, Base: *baseBranch, Usage: *usage, Decided: decided, Who: *as}), stdout, stderr)
}

// cmdProgress stamps progress on the work cards a worker holds: the late rule's sign that a
// late card moves (docs/SPEC-SPRINT.md section 8, the rules table's row late). The member
// and the friend daemon send it every sprint.ProgressEvery while the child or the turn
// prints; a card another row holds is refused.
func (a *app) cmdProgress(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("progress")
	as := fs.String("as", "", "the fleet member or friend that holds the cards: only the holder stamps a card's progress")
	words, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "progress", err.Error())
	}
	ids, gens, err := cardGens(words)
	if err != nil {
		return refuse(stderr, "progress", err.Error())
	}
	if *as == "" || len(ids) == 0 {
		return refuse(stderr, "progress", "wants --as <worker> and the cards it holds, each as <card> or <card>@<gen>")
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "progress", err.Error())
	}
	return a.runStep("progress", *c, st, store.ProgressStep(sprint.ProgressReq{Sel: sprint.Sel{IDs: ids}, As: *as, Gens: gens, Who: *as}), stdout, stderr)
}

func (a *app) cmdAsk(args []string, stdout, stderr io.Writer) int {
	var another *bool
	var ans, instead *string
	return a.setVerb("ask", args, stdout, stderr, false, func(fs flagSet) {
		another = fs.Bool("another", false, "one more reader for a primary already asked")
		ans = fs.String("answers", "", answersWords)
		instead = fs.String("instead", "", "take back this reader's read (asked or reading) of the one primary named and ask one other reader, as --another chooses")
	}, func(ids []string, s *sel) string {
		if *instead != "" && s.group != "" {
			return "--instead takes back one read of one primary and asks one other reader: ask <primary> --instead <reader>, with no --another, --group, --stream or --max; nothing was changed"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.AskStep(sprint.AskReq{Sel: s.sel(ids), Another: *another, Answers: answers(*ans), Who: c.actor, Instead: *instead})
	})
}

func (a *app) cmdRead(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("read")
	as := fs.String("as", "", "the reader; use read-card IDs from queue --as <reader>; several readers, comma separated, each reporting its own named read cards in one step")
	begin := fs.Bool("begin", false, "asked -> reading; a named queued packet uses <read-card>@<gen>, while --max selects the live queue")
	ok := fs.Bool("ok", false, "the read found it good")
	broken := fs.Bool("broken", false, "the read found it broken")
	finding := fs.String("finding", "", "what the read found; with --broken it names the file (file:line), the line, or the card's STEP or RULE the work breaks, and what to change, or the read is refused")
	var limit int
	bindCapAlias(fs, "listed items of each kind (0 is all); when given, the first n of the reader's queue (omitted, 1)")
	ret := fs.String("return", "", "hand back a read the reader holds and has no verdict on: not a read; the next tick asks it of another reader free at the attempt, or of this reader again; no finding against the work")
	reason := fs.String("reason", "", "with --return: why the read has no verdict (it reaches the inbox)")
	usage := fs.String("usage", "", "with --ok, --broken or --return: what the read spent, one line (the reader passes its child's tokens, wall and cost): kept on the read card, timed and priced")
	ids, err := parse(fs, args)
	applyCapAlias(fs, stderr, &limit, c.max)
	if err != nil {
		return refuse(stderr, "read", err.Error())
	}
	n := 0
	for _, b := range []bool{*begin, *ok, *broken, *ret != ""} {
		if b {
			n++
		}
	}
	if *as == "" || n != 1 {
		return refuse(stderr, "read", "wants --as <reader> and one of --begin, --ok, --broken, --return <card> --reason <text>")
	}
	// Attribution is never a finding (docs/SPEC-SPRINT.md, reader-ignores-attribution):
	// a broken read whose every sentence is about the trailer, the By: line or the
	// model or harness named is refused before the store is touched.
	if *broken && sprint.AttributionOnly(*finding) {
		return refuse(stderr, "read", sprint.AttributionRefusal)
	}
	if *ret != "" {
		if len(ids) > 0 || *reason == "" {
			return refuse(stderr, "read", "--return names its one card and wants --reason <text>")
		}
		ids = []string{*ret}
		c.actor = *as // the returner is the reader, whoever runs the verb
	}
	ids, gens, err := cardGens(ids)
	if err != nil {
		return refuse(stderr, "read", err.Error())
	}
	verdict := "ok"
	if *broken {
		verdict = "broken"
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "read", err.Error())
	}
	if len(ids) == 0 && !sprint.IsFriendRow(*as) {
		col := sprint.Reading
		if *begin {
			col = sprint.Asked
		}
		c.after = func(ctx context.Context, st *store.Store, res store.Result) []string {
			return readShort(ctx, st, res, sprint.Split(*as), col)
		}
	}
	// a broken read whose branch origin does not hold is the machine's fault: the server
	// checks the branch at the close, and the step asks the read again (sprint read_missing.go)
	var missing map[string]sprint.MissingBranch
	if *broken {
		missing = a.readMissingOf(context.Background(), st, ids)
	}
	return a.runStep("read", *c, st, store.ReadStep(sprint.ReadReq{Sel: sprint.Sel{IDs: ids, Limit: limit}, As: *as, Gens: gens, Begin: *begin,
		Verdict: verdict, Finding: *finding, Missing: missing, Return: *ret != "", Reason: *reason, Usage: *usage, Who: *as}), stdout, stderr)
}

// readShort is why a read by queue moved nothing, one line per reader named: the
// name is no row of the readers table, or the reader holds no read card in the
// column the verb moves from (asked for --begin, reading for a verdict). A read
// that moved a card says nothing.
func readShort(ctx context.Context, st *store.Store, res store.Result, readers []string, col sprint.State) []string {
	if len(res.Moved) > 0 {
		return nil
	}
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return []string{"why the read moved nothing is not known: the readers table did not read: " + err.Error()}
	}
	var out []string
	for _, r := range readers {
		if !slices.Contains(rows, r) {
			out = append(out, r+" read nothing: it is no reader of the readers table (readers: "+orDashStr(strings.Join(rows, ","), "none")+"); run: nova-sprint reader add "+r)
			continue
		}
		if cs, err := st.ReadCells(ctx, sprint.Readers, r, col); err == nil && len(cs) == 0 {
			out = append(out, r+" read nothing: it holds no read card "+col+"; run: nova-sprint queue --as "+r)
		}
	}
	return out
}

// cmdAccept is the coordinator accepting; --heavy records its own heavy read,
// the evidence file read here and named with its sha256 (sprint.AcceptReq.Heavy;
// docs/SPEC-SPRINT.md section 6, accept-heavy-verdict-b.w1).
func (a *app) cmdAccept(args []string, stdout, stderr io.Writer) int {
	var readOK, heavy *bool
	var ans, evidence, reason *string
	var sha string
	return a.setVerb("accept", args, stdout, stderr, false, func(fs flagSet) {
		readOK = fs.Bool("read-ok", false, "every primary in review with the ok reads it needs (one reader's for a flash card, two different readers' for a pro card); moves eligible primaries into the merge queue. The tick does this itself every tick and tells the seat (ready to merge); the verb is for a stuck case, and says \"nothing waits: the tick accepts\" when there is nothing")
		ans = fs.String("answers", "", answersWords)
		heavy = fs.Bool("heavy", false, "the coordinator's own heavy read of each named primary in review, at its attempt and head: one ok read toward its read rule, recorded on the primary under coordinator:<actor> with --evidence and its sha256, never as a reader's read; a reader's broken read at the attempt stays, marked overruled; wants ids, --evidence and --reason")
		evidence = fs.String("evidence", "", "with --heavy: the path of a readable file the coordinator's heavy read rests on; its sha256 is recorded beside it")
		reason = fs.String("reason", "", "with --heavy: why the coordinator's read stands, kept on the primary")
	}, func(ids []string, s *sel) string {
		if len(ids) == 0 && s.stream == "" && !*readOK && s.limit == 0 {
			return "wants ids, --stream <s>, --read-ok or --group <id>"
		}
		if !*heavy {
			if *evidence != "" || *reason != "" {
				return "--evidence and --reason go with --heavy"
			}
			return ""
		}
		var why []string
		if len(ids) == 0 || s.stream != "" || *readOK || s.group != "" {
			why = append(why, "--heavy wants named ids alone, not --stream, --read-ok or --group")
		}
		if *reason == "" {
			why = append(why, "--heavy wants --reason <text>")
		}
		if b, err := os.ReadFile(*evidence); *evidence == "" || err != nil {
			why = append(why, "--heavy wants --evidence <path>, a readable file: "+oneline.Escape(*evidence)+" is not")
		} else {
			sum := sha256.Sum256(b)
			sha = hex.EncodeToString(sum[:])
		}
		return strings.Join(why, "; ")
	}, func(ids []string, s *sel, c *common) store.Step {
		r := sprint.AcceptReq{Sel: s.sel(ids), Answers: answers(*ans), Who: c.actor, ReadOK: *readOK}
		if *heavy {
			r.Heavy, r.Evidence, r.EvidenceSHA, r.Reason = true, *evidence, sha, *reason
		}
		if s.group != "" {
			r.Sel = sprint.Sel{Only: ids} // a selection: the eligible move
		}
		return store.AcceptStep(r)
	})
}

func (a *app) cmdRework(args []string, stdout, stderr io.Writer) int {
	var fix, ans, tier *string
	return a.setVerb("rework", args, stdout, stderr, false, func(fs flagSet) {
		fix = fs.String("fix", "", "the fix for every primary; without it each takes its own: the finding of its broken read, or the report of its failed work; the next attempt is staged at the tip of the card's base branch, the last pushed attempt's work carried on top where it applies cleanly, and where it does not the child is told that work must be redone")
		ans = fs.String("answers", "", answersWords)
		tier = fs.String("tier", "", "the tier ("+cardhdr.RouteList+") this attempt and every later deal of the card draws its route from, over its brief's line 1, kept on the card: it pins the card, never escalated past it (flash first); a card whose brief pins a model is refused; at a redeal bound it never names a lower tier, and when the attempt before also ended at its bound on the card's tier the rework is refused unless it names a tier above (flash, pro, heavy, frontier) or the provider its takes failed on is back, which lifts it once per tier per card")
	}, func(ids []string, s *sel) string {
		var why []string
		if len(ids) == 0 && s.stream == "" {
			why = append(why, "wants ids (or --group, --stream); --fix <text> for all, else each primary's own finding or report")
		}
		if *tier != "" && !cardhdr.IsRoute(*tier) {
			why = append(why, "--tier wants "+cardhdr.RouteList+", found "+oneline.Escape(*tier))
		}
		return strings.Join(why, "; ")
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.ReworkStep(sprint.ReworkReq{Sel: s.sel(ids), Fix: *fix, Answers: answers(*ans), Tier: *tier, Who: c.actor})
	})
}

func (a *app) cmdReturn(args []string, stdout, stderr io.Writer) int {
	var reason, ans *string
	return a.setVerb("return", args, stdout, stderr, false, func(fs flagSet) {
		reason = fs.String("reason", "", "why it goes back to review")
		ans = fs.String("answers", "", answersWords)
	}, func(ids []string, s *sel) string {
		if len(ids) == 0 && s.stream == "" {
			return "wants ids, --stream <s> or --group <id>"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.ReturnStep(sprint.ReturnReq{Sel: s.sel(ids), Reason: *reason, Answers: answers(*ans), Who: c.actor})
	})
}

func (a *app) cmdRedo(args []string, stdout, stderr io.Writer) int {
	var ans *string
	return a.setVerb("redo", args, stdout, stderr, false, func(fs flagSet) {
		ans = fs.String("answers", "", "the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing")
	}, func(ids []string, s *sel) string {
		if len(ids) == 0 && s.stream == "" {
			return "wants ids or --stream <s>"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.RedoStep(sprint.RedoReq{Sel: s.sel(ids), Answers: answers(*ans), Who: c.actor})
	})
}

func (a *app) cmdDrop(args []string, stdout, stderr io.Writer) int {
	var reason, ans *string
	var cascade *bool
	return a.setVerb("drop", args, stdout, stderr, true, func(fs flagSet) {
		reason = fs.String("reason", "", "why it leaves the table; kept with its record")
		ans = fs.String("answers", "", answersWords)
		cascade = fs.Bool("cascade", false, "drop too every waiting primary that needs a card named, and their dependants, with the same reason; without it a card another waiting primary still needs is refused, naming the dependants")
	}, func(ids []string, s *sel) string {
		if *reason == "" || len(ids) == 0 && s.stream == "" && s.col == "" {
			return "wants ids (or --stream/--col, --group) and --reason <text>"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		c.after = func(ctx context.Context, st *store.Store, res store.Result) []string {
			return a.droppedBriefs(ctx, st, res, *reason)
		}
		return store.DropStep(sprint.DropReq{Sel: s.sel(ids), Reason: *reason, Answers: answers(*ans), Cascade: *cascade, Who: c.actor})
	})
}

// cmdBrief replaces a primary's brief in place, on a running machine as on a stopped one
// (changing a sprint happens through the verbs, never by hand; docs/SPEC-SPRINT.md, the
// brief verb): the brief held to the card lint as add's is (holdBrief), then one step
// (sprint.Brief), refused for a card working, merging or landed; the card keeps its id,
// stream, score and needs, and one in review opens its next attempt. --dir replaces one
// brief per file (cmdBriefDir); --group answers an inbox group as inbox prints it
// (a group of one with --brief-file, of several with --dir); --widen widens its PATHS by
// its last report's PATHS-PROPOSED line (briefWiden). A correction is never a twin (the
// owner, 2026-10-06: "We gotta stop doing this twin shit. it's waste.").
func (a *app) cmdBrief(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("brief")
	brief := fs.String("brief", "", fmt.Sprintf("the new brief: a child's whole brief, at most %d KiB, held to the card lint as add holds one (--rules, else the file init --rules recorded, else the built-in general rules) and to the same PATHS check at the BASE tip (a literal path must exist, a glob must match a file, and every func, type or verb STOP or START names with a file must be inside a PATHS file; a CARRY: head= brief, such as --widen writes, skips the existence check) and refused, exit 2, nothing written, when it fails; a primary waiting, ready or in review takes one in place, on a STOPPED machine or a RUNNING one (there applied at its next tick), keeping its id: one an attempt was dealt for opens its next attempt, staged from its last pushed head, its bound reset; a card working, merging or landed keeps its brief; one that differs in its DEPENDS-ON: line alone is taken in any state, the machine running or the card dealt, and re-points the card's needs", cardlimits.MaxBriefBytes>>10))
	briefFile := fs.String("brief-file", "", "the new brief, read from this file: its bytes as they are, its one trailing newline cut; not with --brief")
	dir := fs.String("dir", "", "a directory of new briefs: one per *.md file, the card its base name without .md, each read and held as --brief-file's; one bad file refuses the whole call, nothing written; not with an id, --brief or --brief-file; with --group, one file for each member of the group and no other")
	rules := fs.String("rules", "", "the child rules file the brief is held to (default: the file init --rules recorded, else the built-in general rules)")
	tier := fs.String("tier", "", "re-tier the card instead of replacing its brief: the tier ("+cardhdr.RouteList+") every later deal and read of the card draws its route from, kept on the card as rework --tier keeps it (it pins the card, never escalated past it); taken on a RUNNING machine and for a card dealt, where it applies to the next attempt; not with --brief or --brief-file")
	widen := fs.Bool("widen", false, "the card's brief edited in place with its PATHS widened by the PATHS-PROPOSED line of its latest attempt's report (the paths before any prose on that line) and a CARRY: line naming that attempt's pushed head, its next attempt starting from it; the same id, no twin; refused, exit 1, for no line, a glob that climbs out with .. or names no file at the base or the head")
	repoDir := fs.String("repo-dir", "", "with --widen: the clone the base's and the head's files are read in (default: land's clone of the card's REPO:)")
	var s sel
	fs.StringVar(&s.group, "group", "", "the members of the inbox group of this id (the id or alias inbox prints): a group of one takes --brief or --brief-file, a group of several --dir")
	fs.IntVar(&s.expect, "expect", 0, "with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes")
	ans := fs.String("answers", "", answersWords)
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "brief", err.Error())
	}
	if *tier != "" {
		switch {
		case len(ids) != 1 || *brief != "" || *briefFile != "" || *rules != "" || *dir != "" || *widen || s.group != "":
			return refuse(stderr, "brief", "--tier wants one primary and no --brief, --brief-file, --dir, --rules, --widen or --group: it re-tiers the card and replaces nothing")
		case !cardhdr.IsRoute(*tier):
			return refuse(stderr, "brief", "--tier wants "+cardhdr.RouteList+", found "+oneline.Escape(*tier))
		}
		st, err := a.store(*c)
		if err != nil {
			return refuse(stderr, "brief", err.Error())
		}
		return a.runStep("brief", *c, st, store.BriefStep(sprint.BriefReq{ID: ids[0], Tier: *tier, Who: c.actor}), stdout, stderr)
	}
	if *repoDir != "" && !*widen {
		return refuse(stderr, "brief", "--repo-dir goes with --widen")
	}
	if *widen && (len(ids) != 1 || *brief != "" || *briefFile != "" || *dir != "" || *rules != "" || s.group != "") {
		return refuse(stderr, "brief", "--widen wants one primary and no --brief, --brief-file, --dir, --rules or --group: it writes the brief from the card's own")
	}
	var st *store.Store
	var members []string
	if s.group != "" {
		if st, err = a.store(*c); err != nil {
			return refuse(stderr, "brief", err.Error())
		}
		if err := unaliasFlag(context.Background(), st, fs, "answers"); err != nil {
			return refuse(stderr, "brief", "--answers: "+err.Error())
		}
		if sprint.IsAlias(s.group) {
			full, err := unalias(context.Background(), st, []string{s.group})
			if err != nil {
				return refuse(stderr, "brief", "--group: "+err.Error())
			}
			s.group = full[0]
		}
		var code int
		if members, code = a.withGroup("brief", fs, c, st, &s, ids, stdout, stderr); code != 0 {
			return code
		}
		if *dir == "" && len(members) != 1 {
			return refuse(stderr, "brief", fmt.Sprintf("group %s has %d cards and one brief is for one card: give --dir <dir> holding <id>.md for each of %s", s.group, len(members), strings.Join(members, ", ")))
		}
		if *dir == "" {
			ids = members
		}
	} else if s.expect != 0 {
		return refuse(stderr, "brief", "--expect goes with --group <id>")
	}
	if st != nil {
		before := a.judgedBefore(context.Background(), st, answers(*ans))
		if len(before) > 0 {
			c.after = a.afterAnswering("brief", before, "", "", c.actor, c.after)
		}
	}
	if *widen {
		return a.briefWiden(ids[0], *repoDir, answers(*ans), c, stdout, stderr)
	}
	if *dir != "" {
		if len(ids) > 0 || *brief != "" || *briefFile != "" {
			return refuse(stderr, "brief", "--dir names each card by its file: give no id, --brief or --brief-file with it")
		}
		return a.cmdBriefDir(*dir, *rules, members, answers(*ans), c, st, stdout, stderr)
	}
	if len(ids) != 1 || (*brief == "") == (*briefFile == "") {
		return refuse(stderr, "brief", "wants one primary (or --group <id> of one) and one of --brief <text>, --brief-file <path>, --dir <dir> alone, --widen, or --tier <t>")
	}
	if *briefFile != "" {
		text, err := readBriefFile(*briefFile)
		if err != nil {
			return refuse(stderr, "brief", "--brief-file: "+err.Error())
		}
		*brief = text
	}
	rs, code := a.holdBrief("brief", *brief, *rules, c, &st, stderr)
	if code != 0 {
		return code
	}
	return a.replaceBriefs([]sprint.CardAdd{{ID: ids[0], Brief: *brief}}, answers(*ans), rs, c, st, stdout, stderr)
}

// briefWiden is brief <id> --widen: the card's brief with its PATHS widened by its latest
// attempt's PATHS-PROPOSED line and a CARRY: line at that attempt's head (widen), held to the
// card lint, then edited in place as any brief is: the same id, its next attempt.
func (a *app) briefWiden(id, repoDir string, ans []string, c *common, stdout, stderr io.Writer) int {
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "brief", err.Error())
	}
	w, why, err := a.widen(context.Background(), st, id, repoDir)
	if err != nil {
		return a.readFailed("brief", err, stderr)
	}
	if why != "" {
		return widenRefused(stderr, why)
	}
	rs, code := a.holdBrief("brief", w.brief, "", c, &st, stderr)
	if code != 0 {
		return code
	}
	c.says = append(c.says, fmt.Sprintf("NEXT %s attempt %d starts from attempt %d head=%s", id, w.carry.Attempt+1, w.carry.Attempt, w.carry.Head))
	return a.replaceBriefs([]sprint.CardAdd{{ID: id, Brief: w.brief}}, ans, rs, c, st, stdout, stderr)
}

// cmdBriefDir is brief --dir (docs/SPEC-SPRINT.md, the brief verb): one brief
// per *.md file of dir in byte order of file name, read as add --brief-dir
// reads them (decide.CardFilePaths), each card the file's base name without
// .md; every brief is held to the size bound and the card lint before
// anything is written, one bad file refusing the whole call naming it, and one
// step replaces them all or none, its moved= the count replaced. With members
// (brief --group), the files are one for each member and no other.
func (a *app) cmdBriefDir(dir, rules string, members, ans []string, c *common, st *store.Store, stdout, stderr io.Writer) int {
	files, err := decide.CardFilePaths(dir)
	if err != nil {
		return refuse(stderr, "brief", "--dir: "+err.Error())
	}
	if len(files) == 0 {
		return refuse(stderr, "brief", fmt.Sprintf("--dir %s holds no *.md file; write one brief per card there, named <id>.md", dir))
	}
	cards := make([]sprint.CardAdd, 0, len(files))
	for _, path := range files {
		id := strings.TrimSuffix(filepath.Base(path), ".md")
		if !sprint.ValidID(id) {
			return refuse(stderr, "brief", fmt.Sprintf("%s: the card id is the file's base name without .md, and %q is not one (letters, digits, _ and -)", path, id))
		}
		text, err := readBriefFile(path)
		if err != nil {
			return refuse(stderr, "brief", err.Error())
		}
		if len(text) > store.MaxBriefBytes {
			return refuse(stderr, "brief", fmt.Sprintf("%s: the brief is %d bytes, over the %d bytes a brief may be; a brief is a child's whole brief; shorten it", path, len(text), store.MaxBriefBytes))
		}
		cards = append(cards, sprint.CardAdd{ID: id, Brief: text, File: path})
	}
	if members != nil {
		var missing, extra []string
		for _, m := range members {
			if !slices.ContainsFunc(cards, func(cd sprint.CardAdd) bool { return cd.ID == m }) {
				missing = append(missing, m)
			}
		}
		for _, cd := range cards {
			if !slices.Contains(members, cd.ID) {
				extra = append(extra, cd.ID)
			}
		}
		if len(missing) > 0 || len(extra) > 0 {
			return refuse(stderr, "brief", fmt.Sprintf("--dir %s holds one brief for each member of the group and no other: missing %s, not of the group %s", dir, orNone(missing), orNone(extra)))
		}
	}
	rs, code := a.briefRules("brief", rules, c, &st, stderr)
	if code != 0 {
		return code
	}
	if code := lintBriefFiles("brief", cards, rs, c.max, stderr); code != 0 {
		return code
	}
	return a.replaceBriefs(cards, ans, rs, c, st, stdout, stderr)
}

// orNone is the ids comma separated, "none" for none.
func orNone(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ",")
}

// replaceBriefs is brief's write, one step for every card (sprint.Brief): each
// brief's WHO line held to the friends table (holdWho), each card naming the
// rules the member injects into it (cardRules); ans the judgments it answers.
func (a *app) replaceBriefs(cards []sprint.CardAdd, ans []string, rs ruleSet, c *common, st *store.Store, stdout, stderr io.Writer) int {
	if st == nil { // --rules named the rule set: briefRules opened no store
		var err error
		if st, err = a.store(*c); err != nil {
			return refuse(stderr, "brief", err.Error())
		}
	}
	req := sprint.BriefReq{Who: c.actor, Answers: ans}
	texts := make([]string, len(cards))
	checks := make([]briefCheck, len(cards))
	for i, cd := range cards {
		checks[i] = briefCheck{id: cd.ID, brief: cd.Brief}
	}
	if code := a.holdPathsAdmit("brief", stderr, checks...); code != 0 {
		return code
	}
	streams := make([]string, len(cards))
	for i, cd := range cards {
		texts[i] = cd.Brief
		// the card's own stream, for its named friend's restriction; a card the store does
		// not know yet is left to the write step's refusal
		if info, err := st.CardOf(context.Background(), cd.ID); err == nil && info.Primary != nil {
			streams[i] = info.Primary.F("stream")
		}
		req.Cards = append(req.Cards, sprint.BriefCard{ID: cd.ID, Brief: cd.Brief, Rules: cardRules(cd.Brief, rs).held, Needs: uniquify(briefNeeds(cd.Brief))})
		c.says = append(c.says, unfilledSays("the brief of "+cd.ID, cd.Brief)...)
	}
	if code := a.holdWho("brief", st, stderr, streams, texts...); code != 0 {
		return code
	}
	return a.runStep("brief", *c, st, store.BriefStep(req), stdout, stderr)
}

// cmdMove moves unstarted primaries to another stream (changing a stopped
// sprint happens through the verbs, never by hand): one step (sprint.MoveCards),
// on a STOPPED machine, each card waiting or ready with nothing dealt, placed
// as add places cards, all or none.
func (a *app) cmdMove(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("move")
	stream := fs.String("stream", "", "the stream the cards move to: one of the sprint's, or a new one, made as add makes it")
	score := fs.String("score", "", "the first card's score in its new line; the rest follow it (default: after every primary)")
	before := fs.String("before", "", "place the cards in line in front of this primary of the stream")
	after := fs.String("after", "", "place the cards in line after this primary of the stream")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "move", err.Error())
	}
	if len(ids) == 0 || *stream == "" {
		return refuse(stderr, "move", "wants ids and --stream <s>")
	}
	r := sprint.MoveReq{IDs: ids, Stream: *stream, Before: *before, After: *after, Who: c.actor}
	if *score != "" {
		f, err := strconv.ParseFloat(*score, 64)
		if err != nil {
			return refuse(stderr, "move", "--score wants a number")
		}
		r.Score = &f
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "move", err.Error())
	}
	return a.runStep("move", *c, st, store.MoveStep(r), stdout, stderr)
}

func (a *app) cmdRank(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("rank")
	score := fs.String("score", "", "the new score of the first id; the rest follow it")
	first := fs.Bool("first", false, "ahead of every primary")
	before := fs.String("before", "", "in line in front of this primary of the cards' own stream, in the order named, placed as add --before places cards (the line is never renumbered)")
	ans := fs.String("answers", "", answersWords)
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "rank", err.Error())
	}
	given := 0
	for _, on := range []bool{*score != "", *first, *before != ""} {
		if on {
			given++
		}
	}
	if len(ids) == 0 || given != 1 {
		return refuse(stderr, "rank", "wants ids and one of --score <n>, --first, --before <id>")
	}
	r := sprint.RankReq{IDs: ids, First: *first, Before: *before, Answers: answers(*ans), Who: c.actor}
	if *score != "" {
		f, err := strconv.ParseFloat(*score, 64)
		if err != nil {
			return refuse(stderr, "rank", "--score wants a number")
		}
		r.Score = &f
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "rank", err.Error())
	}
	if err := unaliasFlag(context.Background(), st, fs, "answers"); err != nil {
		return refuse(stderr, "rank", "--answers: "+err.Error())
	}
	return a.runStep("rank", *c, st, store.RankStep(r), stdout, stderr)
}

func (a *app) cmdMerge(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("merge")
	stream := fs.String("stream", "", "the stream whose queued batches are selected to merge and land")
	batch := fs.Int("batch", 10, "the batch: the head n of the stream's queue (the lander's selection; to record a landing name the cards with --landed)")
	var landed listFlag
	fs.Var(&landed, "landed", "the record by name: <id>@<head> of each card pushed, again or comma separated; each must be merging in --stream at that head and the head an ancestor of --base-ref in --repo, or all are refused and nothing is written")
	repo := fs.String("repo", "", "with --landed: a clone whose --base-ref is fetched; git merge-base --is-ancestor runs there, once per card")
	baseRef := fs.String("base-ref", "", "with --landed: the fetched tip of the base branch in --repo (origin/<base>)")
	conflict := fs.String("conflict", "", "fact: this card of the batch did not merge; on its own head (a file conflict, the lander's checks, the tree gate its base passes, as --note and --conflict-kind say) it is reworked at the base's tip, or returned for the widen rule, and the stream goes on; otherwise the stream stops")
	cross := fs.String("cross", "", "fact: <card>=<other>: the card needs <other> first; <other> is on the table, in another stream, not landed")
	red := fs.Bool("red", false, "fact: the stream branch went red on the batch")
	rejected := fs.Bool("rejected", false, "fact: the merge queue rejected the batch")
	baseRed := fs.String("base-red", "", "fact: the base fails its tree gate, this the error (land's base-gate rule, after its third failure): the stream stops, no card moves")
	conflictKind := fs.String("conflict-kind", "", "with --conflict: file (a path no generated ledger owns did not merge: the card's own, reworked at the tip) or ledger (a generated ledger the lander could not resolve: the stream stops)")
	var conflictPaths listFlag
	fs.Var(&conflictPaths, "conflict-path", "with --conflict: a path that did not merge; again, or comma separated, for more")
	note := fs.String("note", "", "what the facts' source said")
	var suspects listFlag
	fs.Var(&suspects, "suspect", "with --red: a card of the batch suspected of turning it red; again, comma separated, or ids after it for more")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "merge", err.Error())
	}
	if len(suspects) > 0 {
		suspects, pos = append(suspects, pos...), nil
		if !*red {
			return refuse(stderr, "merge", "--suspect goes with --red")
		}
	}
	facts := 0
	for _, f := range []bool{*conflict != "", *cross != "", *red, *rejected, *baseRed != ""} {
		if f {
			facts++
		}
	}
	if *stream == "" || len(pos) > 0 || facts > 1 {
		return refuse(stderr, "merge", "wants --stream <s> and at most one fact of --conflict, --cross, --red, --rejected, --base-red")
	}
	var pins []sprint.LandedPin
	if len(landed) > 0 || *repo != "" || *baseRef != "" {
		if len(landed) == 0 || *repo == "" || *baseRef == "" || facts > 0 {
			return refuse(stderr, "merge", "the record by name wants --landed <id>@<head>... with --repo <dir> and --base-ref <ref>, and no fact flag; run: nova-sprint merge --stream "+*stream+" --landed <id>@<head> --repo <dir> --base-ref origin/<base>")
		}
		var err error
		if pins, err = landedPins(context.Background(), landed, *repo, *baseRef); err != nil {
			return refuse(stderr, "merge", err.Error())
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "merge", err.Error())
	}
	return a.runStep("merge", *c, st, store.MergeStep(sprint.MergeReq{Stream: *stream, Batch: *batch, Landed: pins, Conflict: *conflict, Cross: *cross,
		Red: *red, Suspects: suspects, Rejected: *rejected, BaseRed: *baseRed, ConflictKind: *conflictKind, ConflictPaths: conflictPaths, Note: *note, Who: c.actor}), stdout, stderr)
}

// landedPins reads the --landed pairs and runs, once per card, the one git merge-base
// --is-ancestor of its head against the fetched base tip: the fact the step checks (docs/
// SPEC-SPRINT.md section 8). Exit 1 is "not an ancestor", a fact; anything else (a head git
// does not know, no repository) is a refusal naming the card, so nothing is recorded.
func landedPins(ctx context.Context, pairs []string, repo, baseRef string) ([]sprint.LandedPin, error) {
	var pins []sprint.LandedPin
	for _, pair := range pairs {
		id, head, ok := strings.Cut(pair, "@")
		if !ok || id == "" || head == "" {
			return nil, fmt.Errorf("--landed %s: wants <id>@<head>", oneline.Escape(pair))
		}
		err := subproc.Context(ctx, "git", "-C", repo, "merge-base", "--is-ancestor", head, baseRef).Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit) && exit.ExitCode() == 1: // not an ancestor: the step refuses it, naming the card
		default:
			return nil, fmt.Errorf("--landed %s: git merge-base --is-ancestor %s %s in %s failed (%s); fetch the base and the head there, then run again", id, head, baseRef, repo, oneline.Err(err))
		}
		pins = append(pins, sprint.LandedPin{ID: id, Head: head, InBase: err == nil})
	}
	return pins, nil
}

func (a *app) cmdResume(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("resume")
	var streams listFlag
	fs.Var(&streams, "stream", "a stopped stream; again, or comma separated, for more (each is resumed or refused on its own line, and the exit is 1 when any is refused)")
	did := fs.String("did", "", "what the coordinator did about the cause; required after a red branch")
	ans := fs.String("answers", "", answersWords)
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "resume", err.Error())
	}
	if len(streams) == 0 || len(pos) > 0 {
		return refuse(stderr, "resume", "wants --stream <s>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "resume", err.Error())
	}
	if err := unaliasFlag(context.Background(), st, fs, "answers"); err != nil {
		return refuse(stderr, "resume", "--answers: "+err.Error())
	}
	// Each stream is its own step: one stopped stream never hides another's
	// refusal, and one --did applies to all of them (docs/SPEC-SPRINT.md).
	code := 0
	for _, s := range streams {
		if rc := a.runStep("resume", *c, st, store.ResumeStep(sprint.ResumeReq{Stream: s, Did: *did, Answers: answers(*ans), Who: c.actor}), stdout, stderr); rc != 0 {
			code = rc
		}
	}
	return code
}

func (a *app) cmdFleet(op string, args []string, stdout, stderr io.Writer) int {
	name := "fleet " + op
	fs, c := a.verbSetup(name)
	var width, deadline *string
	if op == "up" {
		width = fs.String("width", "", fmt.Sprintf("the member's width: the most work cards it runs at once; the deal holds it at %d times that, ready and working; 1 to %d (default: as it is, %d for a new member); 0 drains the member: no new deals, its untaken ready cards are levelled away, its working cards finish (fleet down deals them again elsewhere)", sprint.DealAhead, sprint.MaxWidth, sprint.DefaultWidth))
		deadline = fs.String("deadline", "", fmt.Sprintf("pin the deadline every card dealt to the member gets, a duration (45m, 2700s); default takes the pin off: each card's own deadline, or %d times the member's median run wall over its last %d ok attempts, whichever is larger", sprint.DeadlineK, sprint.DeadlineSamples))
	}
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if (op == "level") != (len(pos) == 0) || len(pos) > 1 {
		return refuse(stderr, name, "wants one member (level takes none)")
	}
	w, drain := 0, false
	if width != nil && *width != "" {
		if drain = strings.TrimSpace(*width) == sprint.DrainWidth; !drain {
			if w, err = sprint.ParseWidth(*width); err != nil {
				return refuse(stderr, name, "--width: "+err.Error())
			}
		}
	}
	d, off := 0, false
	if deadline != nil && *deadline != "" {
		if d, off, err = sprint.ParseDeadline(*deadline); err != nil {
			return refuse(stderr, name, err.Error())
		}
	}
	member := ""
	if len(pos) == 1 {
		member = pos[0]
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	return a.runStep(name, *c, st, a.fleetStep(st, op, member, c.actor, w, drain, d, off), stdout, stderr)
}

func (a *app) cmdReaderAdd(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("reader add")
	tiersFlag := fs.String("tiers", "", "the tiers this reader reads, comma separated ("+cardhdr.RouteList+"); all names every tier; default, or omitted, is flash on a fleet reader while the store holds routes and every tier on a friend's reader")
	names, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "reader add", err.Error())
	}
	if len(names) == 0 {
		return refuse(stderr, "reader add", "wants at least one reader")
	}
	for _, n := range names {
		if !sprint.ValidID(n) {
			return refuse(stderr, "reader add", "a reader name wants letters, digits, _ and -: "+n)
		}
	}
	tiers, tiersSet, code := readerTiersArg("reader add", fs, *tiersFlag, false, stderr)
	if code != 0 {
		return code
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "reader add", err.Error())
	}
	ctx := context.Background()
	if tiersSet {
		if err := st.EnsureReaderTiers(ctx); err != nil {
			return refuse(stderr, "reader add", err.Error())
		}
	}
	if err := st.B.RowsAdd(ctx, st.Names.Table(sprint.Readers), names); err != nil {
		fmt.Fprintf(stderr, "%s reader add: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if tiersSet {
		if err := writeReaderTiers(ctx, st, names, tiers); err != nil {
			fmt.Fprintf(stderr, "%s reader add: %s\n", prog, oneline.Escape(err.Error()))
			return 1
		}
	}
	line := "READER-ADD OK readers=" + strings.Join(names, ",")
	facts := map[string]any{"readers": names}
	if tiersSet {
		word := sprint.ReaderTiersShown(tiers)
		line += " tiers=" + word
		facts["tiers"] = word
	}
	sayOK(stdout, c.json, "reader add", line, facts)
	return 0
}

// cmdReaderSet writes the tiers the named readers read. --tiers is required.
// all and default store an empty cell, which means every tier. A tier that is
// not a route, or a reader with no row, refuses the whole call and writes
// nothing.
func (a *app) cmdReaderSet(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("reader set")
	tiersFlag := fs.String("tiers", "", "the tiers these readers read, comma separated ("+cardhdr.RouteList+"); all names every tier; default is flash on a fleet reader while the store holds routes and every tier on a friend's reader")
	names, code := readerNames("reader set", args, stderr, fs)
	if code != 0 {
		return code
	}
	tiers, _, code := readerTiersArg("reader set", fs, *tiersFlag, true, stderr)
	if code != 0 {
		return code
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "reader set", err.Error())
	}
	ctx := context.Background()
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return a.readFailed("reader set", err, stderr)
	}
	if bad := unknownReaders(rows, names); len(bad) > 0 {
		fmt.Fprintf(stderr, "%s reader set: no reader %s on the readers table (readers: %s); nothing was changed; run: nova-sprint reader add <name>\n", prog, strings.Join(bad, ","), strings.Join(rows, ","))
		return 1
	}
	if err := st.EnsureReaderTiers(ctx); err != nil {
		return refuse(stderr, "reader set", err.Error())
	}
	if err := writeReaderTiers(ctx, st, names, tiers); err != nil {
		fmt.Fprintf(stderr, "%s reader set: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	word := sprint.ReaderTiersShown(tiers)
	sayOK(stdout, c.json, "reader set", "READER-SET OK readers="+strings.Join(names, ",")+" tiers="+word, map[string]any{"readers": names, "tiers": word})
	return 0
}

// readerTiersArg parses --tiers. required refuses a call that omitted it.
// The stored value is "" for the default (flash on a fleet reader, every tier on a friend's).
// Nothing is written here.
func readerTiersArg(verbName string, fs *flag.FlagSet, raw string, required bool, stderr io.Writer) (string, bool, int) {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "tiers" {
			set = true
		}
	})
	if !set {
		if required {
			return "", false, refuse(stderr, verbName, "wants --tiers ("+cardhdr.RouteList+", or all, or default)")
		}
		return "", false, 0
	}
	stored, err := sprint.ParseReaderTiers(raw)
	if err != nil {
		return "", true, refuse(stderr, verbName, err.Error())
	}
	return stored, true, 0
}

// writeReaderTiers sets the tiers cell on each named row. "" is every tier.
func writeReaderTiers(ctx context.Context, st *store.Store, names []string, tiers string) error {
	table := st.Names.Table(sprint.Readers)
	for _, n := range names {
		if err := st.B.RowSet(ctx, table, n, map[string]string{sprint.ReaderTiers: tiers}); err != nil {
			return err
		}
	}
	return nil
}

// readerNames are the readers a reader verb names: at least one, each a name
// sprint.ValidID accepts.
func readerNames(verbName string, args []string, stderr io.Writer, fs flagSet) ([]string, int) {
	names, err := parse(fs, args)
	if err != nil {
		return nil, refuse(stderr, verbName, err.Error())
	}
	if len(names) == 0 {
		return nil, refuse(stderr, verbName, "wants at least one reader")
	}
	for _, n := range names {
		if !sprint.ValidID(n) {
			return nil, refuse(stderr, verbName, "a reader name wants letters, digits, _ and -: "+n)
		}
	}
	return names, 0
}

// unknownReaders is the named readers the readers table has no row for.
func unknownReaders(rows, names []string) []string {
	var out []string
	for _, n := range names {
		if !slices.Contains(rows, n) {
			out = append(out, n)
		}
	}
	return out
}

// cmdReaderHold is reader away (away) and reader up: the coordinator holds the
// named readers away, whatever they beat, or releases the hold (the state is
// then the beat's). A named reader with no row refuses the whole call, and
// nothing is written (docs/SPEC-SPRINT.md section 6).
func (a *app) cmdReaderHold(away bool, args []string, stdout, stderr io.Writer) int {
	verbName := map[bool]string{true: "reader away", false: "reader up"}[away]
	fs, c := a.verbSetup(verbName)
	names, code := readerNames(verbName, args, stderr, fs)
	if code != 0 {
		return code
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verbName, err.Error())
	}
	ctx := context.Background()
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return a.readFailed(verbName, err, stderr)
	}
	if bad := unknownReaders(rows, names); len(bad) > 0 {
		fmt.Fprintf(stderr, "%s %s: no reader %s on the readers table (readers: %s); nothing was changed; run: nova-sprint reader add <name>\n", prog, verbName, strings.Join(bad, ","), strings.Join(rows, ","))
		return 1
	}
	// the old words of hold <reader>... --return and unhold <reader>... (one release): the
	// reads it holds are asked of another, as a reader away's always were
	// (docs/SPEC-SPRINT.md section 11)
	return a.runHold(verbName, "", *c, st, sprint.HoldReq{Names: names, Kind: sprint.HoldReader, Release: !away, Return: away, Who: c.actor}, func() (string, map[string]any) {
		return token(verbName) + " OK readers=" + strings.Join(names, ","), map[string]any{"readers": names}
	}, stdout, stderr)
}

// cmdReaderRemove takes the named readers off the readers table (the mirror of
// reader add), in one write: refused, exit 1 and nothing written, when a named
// reader is no row of the table or holds a read card (asked, reading, ok or
// broken: the row delete would take the card's place with it), naming the
// reader and the read cards it holds. The model tla/SprintEvents.tla holds
// the readers as a constant set with no add or remove action; the presence of
// a reader is tla/DirtyTick.tla's.
func (a *app) cmdReaderRemove(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("reader remove")
	names, code := readerNames("reader remove", args, stderr, fs)
	if code != 0 {
		return code
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "reader remove", err.Error())
	}
	ctx := context.Background()
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return a.readFailed("reader remove", err, stderr)
	}
	if bad := unknownReaders(rows, names); len(bad) > 0 {
		fmt.Fprintf(stderr, "%s reader remove: no reader %s on the readers table (readers: %s); nothing was changed\n", prog, strings.Join(bad, ","), strings.Join(rows, ","))
		return 1
	}
	var holds []string
	for _, n := range names {
		cs, err := st.ReadCells(ctx, sprint.Readers, n, sprint.Asked, sprint.Reading, sprint.OK, sprint.Broken)
		if err != nil {
			return a.readFailed("reader remove", err, stderr)
		}
		var ids []string
		for _, x := range cs {
			ids = append(ids, x.ID+" ("+x.Col+")")
		}
		if len(ids) > 0 {
			holds = append(holds, n+" holds "+sprint.Preview(ids, ", "))
		}
	}
	if len(holds) > 0 {
		fmt.Fprintf(stderr, "%s reader remove: %s; nothing was changed; a read moves on (read --as <reader>), is sent to another reader (ask --another), or leaves with its primary (rework, drop)\n", prog, oneline.Escape(strings.Join(holds, "; ")))
		return 1
	}
	if err := st.B.RowsDel(ctx, st.Names.Table(sprint.Readers), names); err != nil {
		fmt.Fprintf(stderr, "%s reader remove: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	// the rows are gone: their beats and holds go with them
	if err := st.ForgetReaders(ctx, names); err != nil {
		fmt.Fprintf(stderr, "%s reader remove: the rows were removed, their records were not: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	sayOK(stdout, c.json, "reader remove", "READER-REMOVE OK readers="+strings.Join(names, ","), map[string]any{"readers": names})
	return 0
}

// streamWords is what a stream is to the coordinator's verbs, in nova-sprint
// help and nova-sprint help stream.
func streamWords() string {
	return strings.TrimSpace(`
The streams: add --stream <s> opens a stream, its row of the work and merge
tables, and a clear keeps it. stream remove takes streams off both tables, on
a STOPPED machine only (nova-sprint stop first), refused while a stream holds a
card (a primary or a sentinel in any column of its work row, a merge card in
its merge row: nova-sprint clear --confirm sprint, or drop) and all or none
for the streams named. A clear does not bring a removed stream back; an add
under its name does, in this epoch or the next (a NOTE line says it came
back). stream archive takes streams
whose every card has landed off both tables, on a running machine too: their
rows are hidden and every landed card, its cost and its landing stay, in
where --json's archived (streams, cards, landed, cost), stream_costs and
where --json --archived; the summary line and the drawn footers count only
the streams on the table, and where --json carries archived_cards and
archived_landed beside them; refused while a
stream holds a card not landed, naming it, all or none. stream unarchive
draws them again. The tick archives a stream itself when its last card lands
and nothing waits behind it (one note names it), and draws an archived stream
again when a card not landed is in it. stream set <s> --read-tier pro
puts the reads of the stream's cards on pro, over the sprint's read tier (set
--read-tier); a read tier raises a card's reads and never lowers them below the
card's own tier, and default takes the stream's off.`) + "\n"
}

// cmdSet writes the sprint's settings (sprint.Set): its read tier, the tier every
// card's reads are raised to, and its dealt bound, how long a work card may wait
// dealt and never taken before it is a judgment (nova-tools#5096 items 22, 27), and
// the backlog alarms' thresholds (sprint.TickAlarms).
func (a *app) cmdSet(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("set")
	tier := fs.String("read-tier", "", "the tier every card's reads draw their route from when it is stronger than the card's own (flash, pro or heavy; default takes it off: each card's own tier)")
	dealt := fs.String("dealt-max", "", fmt.Sprintf("how long a work card may wait dealt and never taken (in its member's ready queue, or withdrawn) before it is a judgment: a duration, or default (%s, 3 times the take deadline); a taken card's own deadline starts at its take", sprint.DealtMaxDefault))
	lanes := fs.String("go-lanes", "", fmt.Sprintf("the Go lanes of every machine, the Go build and test runs one machine grants at once (nova-sprint lane take go): a whole number from 1, or default (%d, one test stream per machine)", sprint.LaneWidthDefault))
	review := fs.String("alarm-review", "", "the backlog alarm on review: a judgment, once an episode, while more primaries than this whole number are in review; off takes it off (the default)")
	merging := fs.String("alarm-merging", "", "the backlog alarm on merging: a judgment, once an episode, while more primaries than this whole number are merging; off takes it off (the default)")
	fleet := fs.String("alarm-fleet", "", "the backlog alarm on the fleet: a judgment, once an episode, while the members up work fewer cards than this percent (1 to 100) of their width with a primary ready or waiting; off takes it off (the default)")
	readyAlarm := fs.String("alarm-ready", "", "the backlog alarm on the feed: on raises a judgment, once an episode, while no primary is ready and one waits; off takes it off (the default)")
	reviewStarved := fs.String("review-starved", "", "how long cards may wait in review with no read out, or free readers idle, before a judgment: a positive duration (at least one tick), or off; default two ticks")
	attempts := fs.String("attempts", "", fmt.Sprintf("the attempt cap: how many attempts one brief may run before the card is the coordinator's as a brief defect (brief, drop; never dealt again); 1 to %d, or default (%d); a stream's own: nova-sprint stream set <s> --attempts <n>", sprint.AttemptsMax, sprint.AttemptsDefault))
	idle := fs.String("friend-idle", "", fmt.Sprintf("how long a friend holding cards may show no file write under her working directory and outbox before it is an alarm: a duration, or default (%s)", sprint.FriendIdleDefault))
	pinWait := fs.String("pin-wait", "", fmt.Sprintf("how long a named WHO preference waits for a friend who is down, held, or already at her room before the pin is waived and the card is dealt on: a duration, or default (%s); WHO: only friend and WHO: friend <name> only never waive", sprint.PinWaitDefault))
	fleetWork := fs.String("fleet", "", "the fleet's work: off and the deal hands no work card to a machine (its readers still read; no fleet idle alarm), on deals again (the default)")
	friendsWork := fs.String("friends", "", "the friends' work: off and the deal hands no work card to a friend (her reads still flow; no empty-row alarm), on deals again (the default)")
	fleetTiers := fs.String("fleet-tiers", "", "the tiers the fleet may take: flash, pro, heavy, frontier, comma separated, or all (the default); the deal hands a machine only a work card, and a member's reader only a read card, whose tier is one of them, on top of each row's own tiers")
	friendsTiers := fs.String("friends-tiers", "", "the tiers the friends may take, as --fleet-tiers says the fleet's: a friend is dealt a work or read card only of one of them, on top of her row's own tiers")
	finish := fs.String("friend-finish", "", fmt.Sprintf("how long a friend holding working cards may finish none (working to done) before the coordinator's pass judges her idle: a duration, or default (%s)", sprint.FriendFinishDefault))
	readCards := fs.String("read-cards", "", "on: the tick asks every read a card in review needs at once, as read cards on the fleet table dealt to friends and to members with a reader row, half a slot each; off or default: the readers table asks, one read at a time")
	reworkPriority := fs.String("rework-priority", "", "the priority a normal or low card gets when its next attempt opens: fix (the default), high, or keep to retain its level")
	reads := fs.String("reads", "", "the ok reads at its head every card in review needs, whatever its tier: 0 (no read: a primary whose work finished LAND is accepted on it), 1 or 2; default: one for a flash card, two above")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "set", err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, "set", "takes no positional words (a stream's read tier: nova-sprint stream set <s> --read-tier <tier>)")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "set", err.Error())
	}
	step := store.SetStep(sprint.SetReq{ReadTier: *tier, DealtMax: *dealt, GoLanes: *lanes, Attempts: *attempts, FriendIdle: *idle, PinWait: *pinWait,
		AlarmReview: *review, AlarmMerging: *merging, AlarmFleet: *fleet, AlarmReady: *readyAlarm,
		Fleet: *fleetWork, Friends: *friendsWork, FleetTiers: *fleetTiers, FriendsTiers: *friendsTiers, ReadCards: *readCards, Reads: *reads, ReworkPriority: *reworkPriority, Who: c.actor})
	if *finish != "" {
		set := step.Plan
		step.Plan = func(s *sprint.Snapshot) sprint.Plan { return sprint.WithFriendFinish(set(s), s, *finish) }
	}
	if *reviewStarved != "" {
		step.Args = store.ArgsOf(struct{ Set, ReviewStarved string }{step.Args, *reviewStarved})
		set := step.Plan
		step.Plan = func(s *sprint.Snapshot) sprint.Plan { return sprint.WithReviewStarved(set(s), s, *reviewStarved) }
	}
	return a.runStep("set", *c, st, step, stdout, stderr)
}

// cmdPromoted is the coordinator's word that the sprint branch was promoted into dev
// (sprint.Promoted): the store counts landings from here, and the judgment "dev is behind"
// closes.
func (a *app) cmdPromoted(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("promoted")
	sha := fs.String("sha", "", "the merge commit's sha on dev, 7 to 40 hex digits (required)")
	ans := fs.String("answers", "", "the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing")
	dry := fs.Bool("dry-run", false, "check the sha and say what would be recorded; record nothing")
	returned := fs.String("returned", "", "landed cards dev or an audit returned with this promotion, comma separated: each is marked on the card and the tick asks to raise its stream's read tier")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "promoted", err.Error())
	}
	if len(pos) > 0 || *sha == "" {
		return refuse(stderr, "promoted", "wants --sha <merge sha> and no positional words")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "promoted", err.Error())
	}
	if *dry {
		sha, why := sprint.PromotedSha(*sha)
		if why != "" {
			return refuse(stderr, "promoted", why)
		}
		fmt.Fprintf(stdout, "PROMOTED DRY-RUN sha=%s; nothing was changed\n", sha)
		return 0
	}
	return a.runStep("promoted", *c, st, store.PromotedStep(sprint.PromotedReq{Sha: *sha, Answers: answers(*ans), Returned: sprint.Split(*returned), Who: c.actor}), stdout, stderr)
}

// cmdFunded is the coordinator's word that a provider was paid: its rest of its funds ends
// now (sprint.Funded; nova-tools#5199), for a provider whose balance no poll can read as for
// any; the balance poll ends it by itself when it reads enough again.
func (a *app) cmdFunded(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("funded")
	reason := fs.String("reason", "", "the payment made, in a few words (required)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, "funded", argErr("wants one word, the provider (as the routes name it), ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "funded", err.Error())
	}
	return a.runStep("funded", *c, st, store.FundedStep(sprint.FundedReq{Provider: pos[0], Reason: *reason, Who: c.actor}), stdout, stderr)
}

// cmdStreamSet writes the read tier or release of the streams named (sprint.Set), over the
// sprint's, their protected-branch mark: the repositories whose protected branches the
// lander lands their cards on, their prose globs: the files the lander does not read
// for a code span (docs/SPEC-SPRINT.md section 7 and section 11), and the base their
// cards not yet dealt and queued to merge are re-pointed to (stream set --base,
// docs/SPEC-SPRINT.md section 11).
func (a *app) cmdStreamSet(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("stream set")
	tier := fs.String("read-tier", "", "the tier the stream's reads draw their route from when it is stronger than the card's own (flash, pro or heavy; default takes it off: the sprint's)")
	mark := fs.String("land-protected", "", "the repositories (owner/name, comma separated; any for every one) on whose protected branches, dev and main, the lander lands the stream's cards; default takes the mark off, and a card based on a protected branch is then refused at land")
	release := fs.String("release", "", "the release this stream belongs to (default or none clears it)")
	prose := fs.String("prose", "", "the globs (PATHS globs, comma separated: security/**,ratings/**) of the files whose backquotes are their own, which the lander does not read for a code span; default takes them off")
	attempts := fs.String("attempts", "", fmt.Sprintf("the stream's attempt cap, over the sprint's: how many attempts one brief may run before the card is the coordinator's as a brief defect; 1 to %d, or default (the sprint's)", sprint.AttemptsMax))
	base := fs.String("base", "", "the base branch to re-point the stream's cards to: every card not yet dealt and every card queued to merge has its BASE line rewritten; refused when origin holds no such branch or a card's PATHS are absent at its tip; dealt and working cards keep their base")
	reason := fs.String("reason", "", "why the read tier is set, recorded on the stream row (the judgment 'raise the read tier of the stream?' names it)")
	ans := fs.String("answers", "", "the judgment notifications this answers, comma separated")
	promotion := fs.Bool("promotion", false, "mark the streams the promotion stream: they alone take cards cut on dev or main, and land them there (--land-protected any); --promotion=false takes the mark off (--land-protected default)")
	names, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "stream set", err.Error())
	}
	var promotionGiven bool
	fs.Visit(func(f *flag.Flag) { promotionGiven = promotionGiven || f.Name == "promotion" })
	if promotionGiven {
		// the promotion mark is the protected-branch mark for every repository
		// (docs/SPEC-SPRINT.md section 7, protected-bases-pb-b.w2)
		if *mark != "" {
			return refuse(stderr, "stream set", "--promotion and --land-protected both set the stream's mark: give one")
		}
		*mark = map[bool]string{true: sprint.LandProtectedAny, false: sprint.ReadTierDefault}[*promotion]
	}
	if len(names) == 0 || (*tier == "" && *mark == "" && *release == "" && *prose == "" && *attempts == "" && *base == "") {
		return refuse(stderr, "stream set", "wants at least one stream and --read-tier <flash|pro|heavy|default>, --land-protected <owner/name,...|any|default>, --promotion[=false], --release <name>, --prose <glob,...|default>, --attempts <n|default> or --base <branch>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "stream set", err.Error())
	}
	var baseChecked []sprint.StreamSetBaseCheck
	if *base != "" {
		// The check add runs, over each brief the base rewrite would carry: the new
		// branch is admitted as a base (pointing the stream at it is the verb's
		// point), and each rewritten brief is held to the brief checks at its tip in
		// the lander's clone of its repository, so a base origin does not hold, or a
		// PATHS entry absent at its tip, refuses the whole call, nothing written
		// (docs/SPEC-SPRINT.md section 11, stream set --base). The candidates read
		// here are bound to the step, which plans over a snapshot of its own: a card
		// added, dealt or revised while the check fetches refuses the step rather
		// than being rewritten without its PATHS checked (sprint.baseChecksHeld).
		ctx := context.Background()
		s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
		if err != nil {
			return a.readFailed("stream set", err, stderr)
		}
		baseChecked = sprint.StreamSetBaseChecks(s, names, *base)
		var checks []briefCheck
		for _, b := range baseChecked {
			checks = append(checks, briefCheck{id: b.ID, brief: b.Brief})
		}
		if code := a.holdBriefBase("stream set", st, true, stderr, checks...); code != 0 {
			return code
		}
	}
	return a.runStep("stream set", *c, st, store.SetStep(sprint.SetReq{Streams: names, ReadTier: *tier, LandProtected: *mark, Release: *release, Prose: *prose, Attempts: *attempts, Base: *base, BaseChecked: baseChecked, Reason: *reason, Answers: answers(*ans), Who: c.actor}), stdout, stderr)
}

// cmdStreamRemove takes the named streams off the work and merge tables:
// removing a work stream is a verb, one that succeeds only on a STOPPED
// machine; it removes each stream's row of both tables, with the stream's
// control card the merge row holds, the one card add made for it. Refused,
// exit 1 and nothing written, on a RUNNING machine, for a stream that is no
// row, or for one that holds a card (sprint.StreamRemove), all or none for
// the streams named. Every open judgment and held condition that names a
// removed stream is retired with it, a NOTE line each (sprint.RetireStreams).
func (a *app) cmdStreamRemove(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("stream remove")
	names, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "stream remove", err.Error())
	}
	if len(names) == 0 {
		return refuse(stderr, "stream remove", "wants at least one stream")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "stream remove", err.Error())
	}
	ctx := context.Background()
	m, _, err := st.Machine(ctx)
	if err != nil {
		return a.readFailed("stream remove", err, stderr)
	}
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return a.readFailed("stream remove", err, stderr)
	}
	if refused := sprint.StreamRemove(s, m.Running(), names); len(refused) > 0 {
		var whys []string
		for _, r := range refused {
			whys = append(whys, r.Key+": "+r.Why)
		}
		fmt.Fprintf(stderr, "%s stream remove: %s; run: nova-sprint help stream\n", prog, oneline.Escape(strings.Join(whys, "; ")))
		return 1
	}
	for _, t := range []string{sprint.Work, sprint.Merge} {
		if err := st.B.RowsDel(ctx, st.Names.Table(t), names); err != nil {
			fmt.Fprintf(stderr, "%s stream remove: %s; run it again to finish\n", prog, oneline.Escape(err.Error()))
			return 1
		}
	}
	said, err := retireStreams(ctx, st, "stream remove", names, "was removed (stream remove), so nothing can act on this")
	if err != nil {
		fmt.Fprintf(stderr, "%s stream remove: the streams are removed, and retiring what names them failed: %s; the next tick retires them\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintf(stdout, "STREAM-REMOVE OK streams=%s\n", strings.Join(names, ","))
	for _, l := range said {
		fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(l))
	}
	return 0
}

func (a *app) cmdCI(args []string, stdout, stderr io.Writer) int {
	var red, green *bool
	var head, run, source, note *string
	return a.setVerb("ci", args, stdout, stderr, false, func(fs flagSet) {
		red = fs.Bool("red", false, "the run failed")
		green = fs.Bool("green", false, "the run passed")
		head = fs.String("head", "", "the head the run tested (default: the primary's)")
		run = fs.String("run", "", "the run's id: a retried report of it is recorded once")
		source = fs.String("source", "", "where the result comes from")
		note = fs.String("note", "", "what the run said")
	}, func(ids []string, s *sel) string {
		if *red == *green || len(ids) == 0 && s.stream == "" {
			return "wants ids (or --stream, --group) and one of --red, --green"
		}
		return ""
	}, func(ids []string, s *sel, c *common) store.Step {
		return store.CIStep(sprint.CIReq{Sel: s.sel(ids), Red: *red, Head: *head, Run: *run, Source: *source, Note: *note, Who: c.actor})
	})
}

func (a *app) cmdWait(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("wait")
	var group string
	var expect int
	fs.StringVar(&group, "group", "", "the notes of the inbox group of this id (the id inbox prints; a group number is refused)")
	fs.IntVar(&expect, "expect", 0, "with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes")
	dur := fs.Duration("for", 0, "review it again after this long")
	until := fs.String("until", "", "review it again at this time (RFC3339)")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "wait", err.Error())
	}
	if (*dur == 0) == (*until == "") {
		return refuse(stderr, "wait", "wants notification ids, or --group <id> [--expect <n>], and one of --for <duration>, --until <time>")
	}
	at := a.now().Add(*dur)
	if *until != "" {
		if at, err = time.Parse(time.RFC3339, *until); err != nil {
			return refuse(stderr, "wait", "--until wants an RFC3339 time")
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "wait", err.Error())
	}
	ctx := context.Background()
	if sprint.IsAlias(group) {
		full, err := unalias(ctx, st, []string{group})
		if err != nil {
			return refuse(stderr, "wait", "--group: "+err.Error())
		}
		group = full[0]
	}
	notes := splitCommas(pos)
	if notes, err = unalias(ctx, st, notes); err != nil {
		return refuse(stderr, "wait", err.Error())
	}
	notes, code := a.waitNotes(group, expect, notes, c, st, stdout, stderr)
	if code != 0 {
		return code
	}
	code = 0
	for _, id := range notes {
		if a.waitOne(st, c, id, at, stdout, stderr) != 0 {
			code = 1
		}
	}
	if c.group.ID != "" {
		fmt.Fprintln(stdout, c.group.line())
	}
	return code
}

// waitNotes is the notes wait sets: the ids named, or every note of --group
// (a stalled stream's group, which has no note, is its own id). A group whose
// size is not --expect is refused here, and nothing is changed.
func (a *app) waitNotes(group string, expect int, notes []string, c *common, st *store.Store, stdout, stderr io.Writer) ([]string, int) {
	if group == "" {
		if expect != 0 {
			return nil, refuse(stderr, "wait", "--expect goes with --group <id>")
		}
		if len(notes) == 0 {
			return nil, refuse(stderr, "wait", "wants notification ids, or --group <id> [--expect <n>], and one of --for <duration>, --until <time>")
		}
		return notes, 0
	}
	if len(notes) > 0 {
		return nil, refuse(stderr, "wait", "takes ids or --group, not both")
	}
	if expect < 0 {
		return nil, refuse(stderr, "wait", "--expect wants the group's size, a whole number from 1")
	}
	_, g, err := groupIDs(context.Background(), st, group)
	if err != nil {
		if isNumber(group) {
			return nil, refuse(stderr, "wait", err.Error())
		}
		fmt.Fprintf(stderr, "%s wait: %s\n", prog, oneline.Escape(err.Error()))
		return nil, 1
	}
	ids := append([]string(nil), g.Notes...)
	if len(ids) == 0 {
		ids = []string{g.ID}
	}
	c.group = groupReport{ID: g.ID, ActedOn: len(ids), Expected: expect}
	if expect > 0 && len(g.Members) != expect {
		if c.json {
			b, _ := json.Marshal(map[string]any{"error": "the group changed", "group": g.ID, "size": len(g.Members), "expected": expect,
				"added": []string{}, "gone": []string{}, "members": nonNil(g.Members), "moved": []string{}})
			fmt.Fprintln(stdout, string(b))
			return nil, 1
		}
		fmt.Fprintf(stderr, "REFUSED group %s: it has %d now, not %d as printed; nothing changed; run: %s inbox --open %s and answer the group as it is now\n", oneline.Escape(g.ID), len(g.Members), expect, prog, oneline.Escape(g.ID))
		listed(stderr, "NOW", g.Members, c.max, "inbox --open "+g.ID)
		fmt.Fprintf(stderr, "%s FAILED moved=0 group=%s size=%d expected=%d; run: nova-sprint inbox --open %s\n", token("wait"), oneline.Escape(g.ID), len(g.Members), expect, oneline.Escape(g.ID))
		return nil, 1
	}
	return ids, 0
}

// waitOne sets one note's review, or refuses it, on its own line. 0 is set.
func (a *app) waitOne(st *store.Store, c *common, id string, at time.Time, stdout, stderr io.Writer) int {
	ctx := context.Background()
	before := a.judgedBefore(ctx, st, []string{id})
	res, held, err := st.Wait(ctx, id, at)
	if err == nil && len(res.Refused) > 0 {
		err = fmt.Errorf("%s", res.Refused[0].Why)
	}
	if err != nil {
		fmt.Fprintf(stderr, "WAIT REFUSED note=%s: %s; run: nova-sprint help wait\n", oneline.Escape(id), oneline.Escape(err.Error()))
		return 1
	}
	for _, say := range a.recordAnswers(ctx, st, "wait", before, store.Result{Moved: []string{id}}, "until "+at.UTC().Format(time.RFC3339), "", c.actor) {
		fmt.Fprintf(stdout, "NOTE %s\n", say)
	}
	if _, stale := sprint.StaleStream(id); stale {
		fmt.Fprintf(stdout, "WAIT OK note=%s quiet until=%s: the inbox shows the stream stale again then if it still has not moved\n", oneline.Escape(id), at.UTC().Format(time.RFC3339))
		return 0
	}
	if held {
		fmt.Fprintf(stdout, "WAIT OK note=%s held until=%s of running time: the tick raises it again then if it still holds\n", oneline.Escape(id), at.UTC().Format(time.RFC3339))
		return 0
	}
	fmt.Fprintf(stdout, "WAIT OK note=%s review=%s\n", oneline.Escape(id), at.UTC().Format(time.RFC3339))
	return 0
}

// splitCommas is the words with each comma list among them split into its
// items: ack takes the ack line inbox prints, whose notes are joined by commas.
func splitCommas(words []string) []string {
	var out []string
	for _, w := range words {
		for _, item := range strings.Split(w, ",") {
			if item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

func (a *app) cmdAck(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("ack")
	reason := fs.String("reason", "", "why nothing is to be done")
	notes, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "ack", err.Error())
	}
	notes = splitCommas(notes) // the ack line inbox prints joins a group's notes with commas
	if len(notes) == 0 || *reason == "" {
		return refuse(stderr, "ack", "wants notification ids and --reason <text>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "ack", err.Error())
	}
	if notes, err = unalias(context.Background(), st, notes); err != nil {
		return refuse(stderr, "ack", err.Error())
	}
	if before := a.judgedBefore(context.Background(), st, notes); len(before) > 0 {
		c.after = a.afterAnswering("ack", before, *reason, "", c.actor, c.after)
	}
	return a.runStep("ack", *c, st, store.AckStep(sprint.AckReq{Notes: notes, Reason: *reason, Who: c.actor}), stdout, stderr)
}

func (a *app) cmdRepair(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("repair")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "repair", argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "repair", err.Error())
	}
	rr, err := st.Repair(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s repair: %s\n", prog, oneline.WithRemedy(err.Error(), prog+" repair -h"))
		return 2
	}
	if c.json {
		if rr == nil {
			rr = []store.RepairResult{}
		}
		b, _ := json.Marshal(map[string]any{"repaired": rr})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	code := 0
	for _, r := range rr {
		fmt.Fprintf(stdout, "OPERATION %s verb=%s done=%s detail=%s\n", oneline.Escape(r.Op), oneline.Escape(r.Verb), r.Done, oneline.Escape(r.Detail))
		if r.Done == "open" {
			code = 1
		}
	}
	status := "OK"
	if code != 0 {
		status = "FAILED"
	}
	fmt.Fprintf(stdout, "REPAIR %s operations=%d\n", status, len(rr))
	return code
}

// confirmName is what clear and teardown want after --confirm: the name of the
// sprint's view, sprint.
func confirmName() string { return sprint.Names{}.View() }

func (a *app) cmdTeardown(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("teardown")
	confirm := fs.String("confirm", "", "the sprint's name, to confirm: the name of its view, sprint")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "teardown", argErr("takes no words ", err, pos...))
	}
	want := confirmName()
	if *confirm != want {
		return refuse(stderr, "teardown", "drops the four tables, the view and every key of the sprint; wants --confirm "+want+" (the name of the sprint's view)")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "teardown", err.Error())
	}
	n, err := st.Teardown(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s teardown: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	sayOK(stdout, c.json, "teardown", fmt.Sprintf("TEARDOWN OK sprint=%s keys=%d", oneline.Escape(want), n), map[string]any{"sprint": want, "keys": n})
	return 0
}

func (a *app) cmdClear(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("clear")
	confirm := fs.String("confirm", "", "the sprint's name, to confirm: the name of its view, sprint")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "clear", argErr("takes no words ", err, pos...))
	}
	want := confirmName()
	if *confirm != want {
		return refuse(stderr, "clear", "stops the sprint and clears all work in it (a new epoch; the old one stays readable with --at-epoch); wants --confirm "+want+" (the name of the sprint's view)")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "clear", err.Error())
	}
	ctx := context.Background()
	res, err := st.Clear(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s clear: %s\n", prog, oneline.WithRemedy(err.Error(), prog+" clear -h"))
		return 2
	}
	var held []string
	for _, k := range []string{"primaries", "work cards", "read cards", "merge cards", "open judgments"} {
		held = append(held, strings.ReplaceAll(k, " ", "_")+"="+strconv.Itoa(res.Held[k]))
	}
	extra := ""
	if res.Finished != "" {
		extra += " finished=" + oneline.Escape(res.Finished)
	}
	if res.Abandoned != "" {
		extra += " abandoned=" + oneline.Escape(res.Abandoned)
	}
	if res.Restored {
		extra += " restored=yes"
	}
	if res.Machine != "" {
		extra += " machine=" + res.Machine + "->" + store.Stopped
	}
	line := sprintLine(ctx, st)
	if c.json {
		heldN := map[string]int{}
		for k, v := range res.Held {
			heldN[strings.ReplaceAll(k, " ", "_")] = v
		}
		sayOK(stdout, true, "clear", "", map[string]any{"from": res.From, "to": res.To, "at": res.At.UTC().Format(time.RFC3339), "held": heldN,
			"finished": res.Finished, "abandoned": res.Abandoned, "restored": res.Restored, "machine_was": res.Machine, "sprint": line})
		return 0
	}
	fmt.Fprintf(stdout, "CLEAR OK epoch=%d->%d at=%s held: %s%s\n", res.From, res.To, res.At.UTC().Format(time.RFC3339), strings.Join(held, " "), extra)
	if line != "" {
		fmt.Fprintln(stdout, line)
	}
	if res.Machine == store.Running {
		fmt.Fprintf(stdout, "the machine is STOPPED; when the new sprint is ready: nova-sprint start\n")
	}
	return 0
}

// cmdReaderRetire is reader retire: the coordinator retires the named readers,
// each a row of the readers table (the comfort list of 2026-10-03, item 6:
// reader remove refuses a reader that holds read history, so retiring the
// second readers was impossible without losing the record). A retired reader
// is held away for good: no read is asked of it and a read asked and not begun
// is asked of another, its own queue writes no beat and answers reader false,
// and its row and its read cards, the history, stay on the table (where reads
// no reader state: the row is listed, its reads counted); reader up brings it
// back. A named
// reader with no row refuses the whole call, and nothing is written.
func (a *app) cmdReaderRetire(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("reader retire")
	dry := fs.Bool("dry-run", false, "say which readers would be retired and write nothing")
	names, code := readerNames("reader retire", args, stderr, fs)
	if code != 0 {
		return code
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "reader retire", err.Error())
	}
	ctx := context.Background()
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return a.readFailed("reader retire", err, stderr)
	}
	if bad := unknownReaders(rows, names); len(bad) > 0 {
		fmt.Fprintf(stderr, "%s reader retire: no reader %s on the readers table (readers: %s); nothing was changed; run: nova-sprint reader add <name>\n", prog, strings.Join(bad, ","), strings.Join(rows, ","))
		return 1
	}
	if *dry {
		fmt.Fprintf(stdout, "READER-RETIRE DRY-RUN readers=%s; nothing was changed\n", strings.Join(names, ","))
		return 0
	}
	for _, n := range names {
		if err := st.SetReaderRetired(ctx, n, c.actor); err != nil {
			fmt.Fprintf(stderr, "%s reader retire: %s\n", prog, oneline.Escape(err.Error()))
			return 1
		}
	}
	sayOK(stdout, c.json, "reader retire", "READER-RETIRE OK readers="+strings.Join(names, ","), map[string]any{"readers": names})
	return 0
}

// afterAnswering is a verb's after hook with the judgment-answer record added (recordAnswers):
// the verb's own hook runs first, and each record that failed is one more NOTE.
func (a *app) afterAnswering(verb string, before []sprint.Open, reason, fix, actor string, prev func(context.Context, *store.Store, store.Result) []string) func(context.Context, *store.Store, store.Result) []string {
	return func(ctx context.Context, st *store.Store, res store.Result) []string {
		var said []string
		if prev != nil {
			said = prev(ctx, st, res)
		}
		return append(said, a.recordAnswers(ctx, st, verb, before, res, reason, fix, actor)...)
	}
}

func init() {
	verbClasses["card base"] = classCoordinator
}

// card base <id> <branch>: a merging card's BASE re-pointed in one step, its work, its
// reads and its place in the merge queue kept, one log line naming the base before and
// after (sprint.CardBase; docs/SPEC-SPRINT.md section 7, a dead base). The branch is asked
// of the card's origin first (its REPO: line, else --repo-dir's origin), and one not there
// is refused. The next land pass tries the card once against the new base. The
// coordinator's alone.
func (a *app) cmdCardBase(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("card base")
	repoDir := fs.String("repo-dir", "", "a clone whose origin is asked for the branch, for a card whose brief names no REPO: line")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "card base", err.Error())
	}
	if len(pos) == 0 {
		return a.cmdCard(append([]string{"base"}, args...), stdout, stderr) // a card whose id is base
	}
	if len(pos) != 2 {
		return refuse(stderr, "card base", argErr("wants <id> <branch>: a merging card and the branch on origin its BASE names now ", nil, pos...))
	}
	id, branch := pos[0], pos[1]
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "card base", err.Error())
	}
	// the card is read first: a card that is not merging is refused by the step, before any git
	ctx := context.Background()
	s, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return a.readFailed("card base", err, stderr)
	}
	onOrigin := false
	if pr := s.Work.Placed(id); pr != nil && pr.Col == sprint.Merging && branch != "" && !strings.HasPrefix(branch, "-") {
		remote, dir := swarm.ReadCardBase([]byte(pr.F("brief"))).Repo, ""
		if remote == "" {
			remote, dir = "origin", *repoDir
		}
		if remote == "origin" && dir == "" {
			return refuse(stderr, "card base", id+" names no REPO: line and no --repo-dir was given; nothing was changed; run: nova-sprint card base "+id+" "+branch+" --repo-dir <clone>")
		}
		res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: a.gitEnv, OwnRepo: dir != ""}, "ls-remote", "--heads", "--", remote, "refs/heads/"+branch)
		if err != nil {
			fmt.Fprintf(stderr, "%s card base: origin %s could not be asked for %s: %s; nothing was changed; run: nova-sprint card base %s %s\n", prog, oneline.Escape(remote), oneline.Escape(branch), oneline.Escape(strings.TrimSpace(string(res.Stderr)+" "+err.Error())), oneline.Escape(id), oneline.Escape(branch))
			return 1
		}
		// the branch is on origin only where origin advertises that exact ref: a
		// matching pattern (refs/heads/*) is not the branch the card names
		want := "refs/heads/" + branch
		for _, line := range strings.Split(string(res.Stdout), "\n") {
			if _, ref, ok := strings.Cut(line, "\t"); ok && strings.TrimSpace(ref) == want {
				onOrigin = true
				break
			}
		}
	}
	return a.runStep("card base", *c, st, store.Step{Args: store.ArgsOf(sprint.CardBaseReq{ID: id, Base: branch, OnOrigin: onOrigin, Who: c.actor}), Verb: "card base", Load: []string{sprint.Work},
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.CardBase(s, sprint.CardBaseReq{ID: id, Base: branch, OnOrigin: onOrigin, Who: c.actor})
		}}, stdout, stderr)
}
