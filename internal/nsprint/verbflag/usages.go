package verbflag

// Usages is THE usage table (usage.go): every nova-sprint verb path, its
// forms and its examples. A path's flags are its flag set's (-h reads them
// off the set, so they are never written twice); a form names what the
// path needs, an example is a whole line a cold session can run. Every
// store flag is optional under a seat or NOVA_SPRINT_REDIS, so no form
// spells --redis.
var Usages = map[string]Usage{
	// the table, and the refresh the loop units run
	"table": {Forms: []string{"[--sprint <S>] [--once | --loop] [--out <file>]", "--layout live [--sprint <S>] [--friends <a,b>] [--once | --loop]",
		"--check", "--compare <file> --sprint <S> --friends <a,b> [--xy-file <file>]"},
		Examples: []string{"table --sprint quack-0926 --once", "table --layout live --once"}},
	"table clear": {Forms: []string{"--checkpoint <file> [--friends <a,b>]"}, Examples: []string{"table clear --checkpoint table-0926.tsv"}},
	"refresh":     {Forms: []string{"-- <command> [arg...]"}, Examples: []string{"refresh -- /usr/bin/true"}, NoFlags: true},

	"acl check": {Forms: []string{"[--rows <file>] [--fix]"}, Examples: []string{"acl check", "acl check --fix"}},

	"adopt receipt": {Forms: []string{"--verb <v> --state <state> --as <worker> [--gap <g>] [--hand <h>] [--note <n>]"},
		Examples: []string{"adopt receipt --verb card-cut --state adopted --as friend:rowan"}},
	"adopt matrix": {Forms: []string{"[--md | --tsv]"}, Examples: []string{"adopt matrix --md"}},
	"adopt status": {Forms: []string{"[--md | --tsv]"}, Examples: []string{"adopt status"}},

	"backpressure check": {Forms: []string{"--sprint <S>"}, Examples: []string{"backpressure check --sprint quack-0926"}},

	"bench beat":    {Forms: []string{"--bench <b> [--once] [--host <h>] [--user <u>]"}, Examples: []string{"bench beat --bench hulk --once"}},
	"bench release": {Forms: []string{"--bench <b> [--session <s>]"}, Examples: []string{"bench release --bench hulk"}},
	"bench reindex": {Forms: []string{"--sprint <S>"}, Examples: []string{"bench reindex --sprint quack-0926"}},
	"bench ls":      {Examples: []string{"bench ls"}},

	"capacity friend": {Forms: []string{"--as <f> --slots <n> [--machine <m>] [--kinds <k,...>] [--tiers <t,...>]"},
		Examples: []string{"capacity friend --as rowan --slots 32"}},
	"capacity bench": {Forms: []string{"--as <b> --slots <n> [--machine <m>] [--kinds <k,...>] [--tiers <t,...>]"},
		Examples: []string{"capacity bench --as studio --slots 8"}},
	"capacity machine": {Forms: []string{"--machine <m> [--cores <n>] [--mem-gb <n>] [--slots <n>]"}, Examples: []string{"capacity machine --machine studio --cores 24 --mem-gb 192"}},
	"capacity budget":  {Forms: []string{"--machine <m> --cpu-milli <n> --mem-mb <n>"}, Examples: []string{"capacity budget --machine studio --cpu-milli 20000 --mem-mb 150000"}},
	"capacity take":    {Forms: []string{"--as <worker> --machine <m> --pgid <n> [--cpu-milli <n>] [--mem-mb <n>]"}, Examples: []string{"capacity take --as friend:rowan --machine studio --pgid 4242"}},
	"capacity give":    {Forms: []string{"--as <worker> --machine <m> --pgid <n>"}, Examples: []string{"capacity give --as friend:rowan --machine studio --pgid 4242"}},
	"capacity renew":   {Forms: []string{"--as <worker> --machine <m> --pgid <n> [--ttl-ms <n>]"}, Examples: []string{"capacity renew --as friend:rowan --machine studio --pgid 4242"}},
	"capacity reap":    {Forms: []string{"--machine <m>"}, Examples: []string{"capacity reap --machine studio"}},
	"capacity hook":    {Forms: []string{"--as <worker> --machine <m> --event <e> --pgid <n>"}, Examples: []string{"capacity hook --as friend:rowan --machine studio --event start --pgid 4242"}},

	"card cut": {Forms: []string{"--from <cards.tsv|-> --repo <owner/name> [--stream <s>] [--sprint <S>] [--base dev] [--base-sha <sha40>] [--dry-run] [--no-github]",
		"--parent <id> --from <children.tsv|-> [--stitch-route <r>] [--stitch-est <m>]",
		"--sprint <S> --repo <owner/name> --issue <n> [--spec <n>] [--index <dir>] [--stream <s>] [--base <b>]"},
		Examples: []string{"card cut --from cards.tsv --repo mas-bandwidth/nova-tools --stream probe-a --no-github",
			"card cut --sprint quack-0926 --repo mas-bandwidth/nova-tools --issue 4352"}},
	"card push":     {Forms: []string{"--sprint <S> <card file>... | --dir <cards/> | --stdin [--map-kind]"}, Examples: []string{"card push --sprint quack-0926 cards/nova-tools-4352.md"}},
	"card release":  {Forms: []string{"--sprint <S>"}, Examples: []string{"card release --sprint quack-0926"}},
	"card stop":     {Forms: []string{"--stdin [--grace <d>]"}, Examples: []string{"card stop --stdin --grace 10s"}},
	"card show":     {Forms: []string{"--sprint <S> --ids <label>"}, Examples: []string{"card show --sprint quack-0926 --ids nova-tools-4352"}},
	"card run":      {Forms: []string{"--sprint <S> --ids <label> --attempt <n> [--out <dir>] [--job <dir>]"}, Examples: []string{"card run --sprint quack-0926 --ids nova-tools-4352 --attempt 1"}},
	"card launched": {Forms: []string{"--sprint <S> --ids <label> --token <t> --branch <b> --jobdir <d>"}, Examples: []string{"card launched --sprint quack-0926 --ids nova-tools-4352 --token t1 --branch swarm/4352 --jobdir /jobs/4352"}},
	"card beat": {Forms: []string{"--ids <copy> [--as <worker>]", "--sprint <S> --ids <label> --token <t>"},
		Examples: []string{"card beat --ids nova-tools-4352~1 --as friend:rowan"}},
	"card end": {Forms: []string{"--ids <copy> (--ok | --score <N> | --fail <why>) [--pr <n> --head <sha>]", "--sprint <S> --ids <label> --token <t> --outcome <DONE|ABSTAIN|BLOCKED|FAILED> --why <code> --results <dir>"},
		Examples: []string{"card end --ids nova-tools-4352~1 --ok --pr 4399 --head dcd918e6"}},
	"card ls":        {Forms: []string{"--unplaced --sprint <S>"}, Examples: []string{"card ls --unplaced --sprint quack-0926"}},
	"card fsck":      {Forms: []string{"[--repair]", "--sprint <S> [--repair]"}, Examples: []string{"card fsck", "card fsck --sprint quack-0926"}},
	"card launch":    {Forms: []string{"--stdin [--wrapper <path>]"}, Examples: []string{"card launch --stdin"}},
	"card render":    {Forms: []string{"--ids <id> [--brief --model <m>]"}, Examples: []string{"card render --ids nova-tools-4352", "card render --ids nova-tools-4352~1 --brief --model opus"}},
	"card stitch":    {Forms: []string{"--ids <stitch> [--drop <child,...>] [--write]"}, Examples: []string{"card stitch --ids nova-tools-4317-stitch"}},
	"card deal":      {Forms: []string{"--ids <primary> --to <worker> [--n <k>]", "--fill"}, Examples: []string{"card deal --ids nova-tools-4352 --to friend:emma", "card deal --fill"}},
	"card work":      {Forms: []string{"--as <worker> [--ids <copy>] [--n <k>]"}, Examples: []string{"card work --as friend:rowan --n 1"}},
	"card land":      {Forms: []string{"--ids <primary> --sha <merge sha>"}, Examples: []string{"card land --ids nova-tools-4352 --sha dcd918e6"}},
	"card cancel":    {Forms: []string{"--ids <copy> --why <text>"}, Examples: []string{"card cancel --ids nova-tools-4352~1 --why 'wrong base'"}},
	"card expire":    {Forms: []string{"[--as <worker,...>]"}, Examples: []string{"card expire"}},
	"card table":     {Forms: []string{"[--stream <s>]"}, Examples: []string{"card table --stream console"}},
	"card consumers": {Forms: []string{"--ids <primary> [--add <worker>] [--rm <worker>]"}, Examples: []string{"card consumers --ids nova-tools-4352"}},
	"card assign":    {Forms: []string{"--ids <primary> --to <worker>"}, Examples: []string{"card assign --ids nova-tools-4352 --to friend:emma"}},
	"card session":   {Forms: []string{"--ids <copy> --as <worker>"}, Examples: []string{"card session --ids nova-tools-4352~1 --as friend:rowan"}},

	"census": {Forms: []string{"[--sprint <S>] [--set <key> | --keys <k,...> | --keys-from <file>] [--fields <f,...>]"}, Examples: []string{"census --sprint quack-0926"}},

	"ci request": {Forms: []string{"--repo <r> --sha <sha> [--pr <n>] [--checks <c,...>] [--again]"}, Examples: []string{"ci request --repo nova-tools --sha dcd918e6d --pr 4399"}},
	"ci run":     {Forms: []string{"--bench <b> --results <dir> [--scratch <dir>] [--mirror-root <dir>]"}, Examples: []string{"ci run --bench hulk --results /srv/ci/results"}},
	"ci status":  {Forms: []string{"--repo <r> (--sha <sha> | --pr <n>)", "--sprint <S>"}, Examples: []string{"ci status --repo nova-tools --pr 4399"}},
	"ci compare": {Forms: []string{"--repo <r> --sha <sha>"}, Examples: []string{"ci compare --repo nova-tools --sha dcd918e6d"}},
	"ci github": {Forms: []string{"[--once] [--as <consumer>]", "--from-runner --repo <owner/name> --sha <40hex> --run-id <n> --event <ev> --workflow <name> --conclusion <c> --job <name>=<result>..."},
		Examples: []string{"ci github --once"}},
	"ci cut":     {Forms: []string{"--sprint <S> --repo <r> --sha <sha> --base <b> [--pr <n>] [--paths <p>]"}, Examples: []string{"ci cut --sprint quack-0926 --repo nova-tools --sha dcd918e6d --base dev"}},
	"ci show":    {Forms: []string{"--repo <r> --sha <sha> [--sprint <S>]"}, Examples: []string{"ci show --repo nova-tools --sha dcd918e6d"}},
	"ci rerun":   {Forms: []string{"--repo <r> --sha <sha> --why <text> [--pr <n>]"}, Examples: []string{"ci rerun --repo nova-tools --sha dcd918e6d --why 'runner died'"}},
	"ci dispose": {Forms: []string{"--repo <r> --sha <sha> --disposition <d> [--url <u>]"}, Examples: []string{"ci dispose --repo nova-tools --sha dcd918e6d --disposition flaky"}},
	"ci parity":  {Forms: []string{"--sprint <S> [--min <n>]"}, Examples: []string{"ci parity --sprint quack-0926"}},

	"consume list":              {Examples: []string{"consume list"}, NoFlags: true},
	"consume ok-to-friend once": {Forms: []string{"[--as <consumer>]"}, Examples: []string{"consume ok-to-friend once"}},
	"consume ok-to-friend run":  {Forms: []string{"[--as <consumer>] [--every <d>]"}, Examples: []string{"consume ok-to-friend run --every 1s"}},
	"consume pr-to-read once":   {Forms: []string{"--sprint <S> [--as <consumer>]"}, Examples: []string{"consume pr-to-read once --sprint quack-0926"}},
	"consume hold-to-fix once":  {Forms: []string{"--sprint <S> [--as <consumer>]"}, Examples: []string{"consume hold-to-fix once --sprint quack-0926"}},

	"cost import": {Forms: []string{"--provider <anthropic|openrouter|oc> --file <export.csv>"}, Examples: []string{"cost import --provider anthropic --file usage.csv"}},

	"dev-red status":  {Forms: []string{"--repo <r> --base <b>"}, Examples: []string{"dev-red status --repo nova-tools --base dev"}},
	"dev-red check":   {Forms: []string{"--repo <r> --base <b>"}, Examples: []string{"dev-red check --repo nova-tools --base dev"}},
	"dev-red watch":   {Forms: []string{"--repo <r> --base <b> --to <worker>"}, Examples: []string{"dev-red watch --repo nova-tools --base dev --to friend:rowan"}},
	"dev-red unwatch": {Forms: []string{"--repo <r> --base <b> --to <worker>"}, Examples: []string{"dev-red unwatch --repo nova-tools --base dev --to friend:rowan"}},

	"digest": {Forms: []string{"[--repo <r,...>] [--since <d>] [--until <RFC3339>]"}, Examples: []string{"digest --since 2h"}},
	"doctor": {Forms: []string{"[--bench <name>] [--sprint <S>]"}, Examples: []string{"doctor", "doctor --sprint quack-0926"}},
	"drain":  {Forms: []string{"(--sprint <S> | --control <id>) [--resume <worker,...>]"}, Examples: []string{"drain --sprint quack-0926 --resume bench:hulk"}},
	"est":    {Forms: []string{"--sprint <S> [--owner <o>]"}, Examples: []string{"est --sprint quack-0926"}},
	"file": {Forms: []string{"--repo <owner/name|name> --title <t> --body-file <f> [--push-to <friend> [--front] --sprint <S>]", "--repo <owner/name|name> --comment <issue> --body-file <f>"},
		Examples: []string{"file --repo nova-tools --title 'card cut: an empty depends-on cell is refused' --body-file issue.md"}},

	"fleet state":         {Forms: []string{"[--bench <b>] [--up]"}, Examples: []string{"fleet state", "fleet state --bench hulk"}},
	"fleet is-up":         {Forms: []string{"--bench <b>"}, Examples: []string{"fleet is-up --bench hulk"}},
	"fleet hold":          {Forms: []string{"--bench <b> --why <text>"}, Examples: []string{"fleet hold --bench hulk --why 'disk swap'"}},
	"fleet release":       {Forms: []string{"--sha <sha>|dev [--bench <b,...>] [--wait <d>]", "--bench <b>"}, Examples: []string{"fleet release --sha dev", "fleet release --bench hulk"}},
	"fleet config":        {Forms: []string{"[--up-after <n>] [--down-after <n>] [--ssh-fail-after <n>]"}, Examples: []string{"fleet config"}},
	"fleet build":         {Forms: []string{"[--bench <b,...>] [--machines <file>] [--build-cmd <path>] [--dry-run]"}, Examples: []string{"fleet build --bench hulk --dry-run"}},
	"fleet build set":     {Forms: []string{"<key>=<value>..."}, Examples: []string{"fleet build set version=v1"}, SetOf: "fleet build"},
	"fleet build duty":    {Forms: []string{"[--machines <file>] [--dry-run]"}, Examples: []string{"fleet build duty --dry-run"}, SetOf: "fleet build"},
	"fleet build compile": {Forms: []string{"--version <v> --commit <sha40> [--platform <p,...>] [--repo-url <url>] [--dry-run]"}, Examples: []string{"fleet build compile --version v1 --commit 0123456789abcdef0123456789abcdef01234567 --dry-run"}},
	"fleet churn":         {Forms: []string{"[--machines <file>] [--only <b,...>] [--seconds <n>]"}, Examples: []string{"fleet churn --seconds 60"}},
	"fleet ps":            {Forms: []string{"[--bench <b>] [--since <d>] [--stray]"}, Examples: []string{"fleet ps --bench hulk --stray"}},
	"fleet play":          {Forms: []string{"--play <tag> [--bench <b,...>] [--dry-run]"}, Examples: []string{"fleet play --play tools --bench hulk --dry-run"}},

	"fn load":   {Examples: []string{"fn load"}},
	"fn check":  {Examples: []string{"fn check"}},
	"fn deploy": {Forms: []string{"[--want <sha>] [--dry-run]"}, Examples: []string{"fn deploy --dry-run"}},
	"fn sum":    {Examples: []string{"fn sum"}, NoFlags: true},

	"fold": {Forms: []string{"--sprint <S> [--work <nova-work checkout>]"}, Examples: []string{"fold --sprint quack-0926"}},

	"friend pull":        {Forms: []string{"[--as <f>] [--n <k>] [--dir <dir>]"}, Examples: []string{"friend pull --n 1"}},
	"friend done":        {Forms: []string{"--ids <copy> (--ok | --score <N> | --fail <why>) [--pr <n> --head <sha>]"}, Examples: []string{"friend done --ids nova-tools-4352~1 --ok --pr 4399 --head dcd918e6"}},
	"friend beat":        {Forms: []string{"[--as <f>] [--once]"}, Examples: []string{"friend beat --once"}},
	"friend hello":       {Forms: []string{"[--as <f>] [--slots <n>] [--harness <h>] [--once]"}, Examples: []string{"friend hello --slots 32 --once"}},
	"friend bye":         {Forms: []string{"[--as <f>]"}, Examples: []string{"friend bye"}},
	"friend wake":        {Forms: []string{"--as <f> [--why <text>]"}, Examples: []string{"friend wake --as emma --why 'new cards'"}},
	"friend row":         {Forms: []string{"[--as <f>] [--once]"}, Examples: []string{"friend row --once"}},
	"friend roles":       {Forms: []string{"[--set <f> --roles <r,...>]"}, Examples: []string{"friend roles", "friend roles --set emma --roles reader"}},
	"friend report":      {Forms: []string{"--as <f> (--out-of-credits | --away | --clear) [--until <t>] [--why <text>]"}, Examples: []string{"friend report --as emma --away --why lunch"}},
	"friend show":        {Forms: []string{"[--as <f>]"}, Examples: []string{"friend show"}},
	"friend sweep":       {Forms: []string{"[--idle-ticks <n>] [--underfull-ticks <n>]"}, Examples: []string{"friend sweep"}},
	"friend down":        {Forms: []string{"--as <f> --why <text>"}, Examples: []string{"friend down --as emma --why 'out of credits'"}},
	"friend up":          {Forms: []string{"--as <f> [--why <text>]"}, Examples: []string{"friend up --as emma"}},
	"friend declare":     {Forms: []string{"--from <fleet/group_vars/all.yml> [--check]"}, Examples: []string{"friend declare --from fleet/group_vars/all.yml --check"}},
	"friend wake-health": {Forms: []string{"(--as <f> | --all) [--repair]"}, Examples: []string{"friend wake-health --all"}},

	"gh budget": {Examples: []string{"gh budget"}},

	"hold ingest":  {Forms: []string{"--as <f> --sprint <S> --repo <r> --pr <n> --url <u> --body-file <f>"}, Examples: []string{"hold ingest --as emma --sprint quack-0926 --repo nova-tools --pr 4399 --url https://example.invalid/c/1 --body-file hold.md"}},
	"hold show":    {Forms: []string{"--sprint <S> --ref <repo>#<n>"}, Examples: []string{"hold show --sprint quack-0926 --ref nova-tools#4399"}},
	"hold release": {Forms: []string{"--as <f> --sprint <S> --ref <repo>#<n> --holder <f> --head <sha> --evidence <url>"}, Examples: []string{"hold release --as rowan --sprint quack-0926 --ref nova-tools#4399 --holder emma --head dcd918e6 --evidence https://example.invalid/c/2"}},
	"hold route":   {Forms: []string{"--sprint <S> [--as <consumer>] [--once]"}, Examples: []string{"hold route --sprint quack-0926 --once"}},

	"idem resolve": {Forms: []string{"--sprint <S> --key <k> --was <ambiguous:who:at_ms> (--url <u> | --none) --as <f>"}, Examples: []string{"idem resolve --sprint quack-0926 --key k1 --was ambiguous:emma:1790000000000 --none --as rowan"}},

	"jev mech":    {Forms: []string{"--repo <owner/name|name> --n <n> --body-file <f> [--mirror <dir>]"}, Examples: []string{"jev mech --repo nova-tools --n 4399 --body-file body.md"}},
	"jev sync":    {Forms: []string{"[--n <moves>]"}, Examples: []string{"jev sync --n 100"}},
	"jev ask":     {Forms: []string{"[--n <rows>] [--key-env <VAR>] [--base-url <url>]"}, Examples: []string{"jev ask --n 10"}},
	"jev report":  {Forms: []string{"[--type <t>] [--version <v>]"}, Examples: []string{"jev report"}},
	"jev outcome": {Forms: []string{"--type <t> --subject <s> --outcome <o> --why <text> [--as <who>]"}, Examples: []string{"jev outcome --type deal --subject nova-tools-4352 --outcome ok --why 'landed green'"}},

	"land": {Forms: []string{"--repo <owner/name> --stream <s> [--base dev] [--tick <d>]"}, Examples: []string{"land --repo mas-bandwidth/nova-tools --stream console"}},
	"land status": {Forms: []string{"--repo <owner/repo>", "[--ids <unit>] --sprint <S>"},
		Examples: []string{"land status --repo mas-bandwidth/nova-tools", "land status --ids nova-tools-4352 --sprint quack-0926"}},
	"land flaky list":    {Forms: []string{"[--repo <r>]"}, Examples: []string{"land flaky list --repo nova-tools"}},
	"land flaky observe": {Forms: []string{"--sprint <S> --repo <r> --pkg <p> --test <t> --lane <l>"}, Examples: []string{"land flaky observe --sprint quack-0926 --repo nova-tools --pkg ./cmd/nova-sprint --test TestX --lane unit"}},
	"land writer":        {Forms: []string{"--repo <r> --base <b> [--writer old-loop|nova-sprint]"}, Examples: []string{"land writer --repo nova-tools --base dev"}},
	"land eval":          {Forms: []string{"--repo <r> [--sprint <S>] [--once]", "--shadow"}, Examples: []string{"land eval --repo nova-tools --once"}},
	"land stream":        {Forms: []string{"--repo <owner/repo> --stream <s,...> [--base dev] [--dry-run]"}, Examples: []string{"land stream --repo mas-bandwidth/nova-tools --stream console --dry-run"}},
	"land merge":         {Forms: []string{"--repo <owner/repo> --stream <s,...>"}, Examples: []string{"land merge --repo mas-bandwidth/nova-tools --stream console"}},
	"land pr":            {Forms: []string{"--pr <n> [--repo <owner/name>] [--wait <d> [--tick <d>]]"}, Examples: []string{"land pr --pr 4399 --repo mas-bandwidth/nova-tools"}},
	"land run":           {Forms: []string{"--repo <r> --sprint <S> [--once] [--dry-run]"}, Examples: []string{"land run --repo nova-tools --sprint quack-0926 --once --dry-run"}},
	"land offer":         {Forms: []string{"--ref <owner/name>#<n> --sprint <S> --stream <slug> --base <b> --created <RFC3339> --body-first <line> --as <f> [--withdraw]"}, Examples: []string{"land offer --ref mas-bandwidth/nova-tools#4399 --sprint quack-0926 --stream console --base dev --created 2026-09-26T19:00:00Z --body-first 'What:' --as rowan"}},
	"land list":          {Forms: []string{"--repo <r> --base <b> [--sprint <S>]"}, Examples: []string{"land list --repo nova-tools --base dev"}},

	"lander": {Forms: []string{"--sprint <S> --repo <r> --batch <name> --pr <n,...> --gate <prog> --bisect <prog> --land <prog> --file <prog>"}, Examples: []string{"lander --sprint quack-0926 --repo nova-tools --batch b1 --pr 4399 --gate ./gate --bisect ./bisect --land ./land --file ./file"}},

	"lesson append":    {Forms: []string{"--repo <dir> --ids <id> --kind <k> --failure <f> --prevention <p> --evidence <e> --reviewed-by <who>"}, Examples: []string{"lesson append --repo . --ids L42 --kind process --failure 'a wall' --prevention 'one table' --evidence 'PR 4399' --reviewed-by glenn"}},
	"lesson supersede": {Forms: []string{"--repo <dir> --ids <id>"}, Examples: []string{"lesson supersede --repo . --ids L42"}},

	"life event":     {Forms: []string{"--as <f> --kind <beat|deliver|turn-start|turn-end|turn-error|usage-limit> [--cause <c>]"}, Examples: []string{"life event --as rowan --kind beat"}},
	"life wake-mode": {Forms: []string{"--as <f> --set scheduled-model-turn"}, Examples: []string{"life wake-mode --as rowan --set scheduled-model-turn"}},

	"line post":   {Forms: []string{"--repo <r> --n <n> (--line <typed line> | --kind <K> --as <w> --head <sha> [--score <N>] [--gates <g>] [--body-file <f>]) [--mirror <dir>]"}, Examples: []string{"line post --repo nova-tools --n 4399 --kind SCORE --as emma --head dcd918e6 --score 9"}},
	"line list":   {Forms: []string{"--repo <r> --n <n> [--head <sha>]"}, Examples: []string{"line list --repo nova-tools --n 4399"}},
	"line import": {Forms: []string{"--repo <r> --n <n> --comments-file <f|->"}, Examples: []string{"line import --repo nova-tools --n 4399 --comments-file comments.json"}},

	"lineup":         {Forms: []string{"[--sprint <S>] [--probes <p,...>] [--no-conform]"}, Examples: []string{"lineup --sprint quack-0926"}},
	"lineup publish": {Forms: []string{"--bench <b> --run <id> [--all-yml <file>]"}, Examples: []string{"lineup publish --bench hulk --run r1"}},

	"mirror refresh": {Forms: []string{"--bench <b> [--repos <r,...>] [--dir <dir>] [--loop <s>]"}, Examples: []string{"mirror refresh --bench hulk"}},
	"mirror check":   {Forms: []string{"--bench <b> [--repos <r,...>] [--dir <dir>]"}, Examples: []string{"mirror check --bench hulk"}},
	"mirror status":  {Forms: []string{"[--repos <r,...>] [--expect <b,...>] [--stale <d>]"}, Examples: []string{"mirror status"}},

	"note": {Forms: []string{"--rote <class> --as <mind> [--what <text>] [--mech <m>]"}, Examples: []string{"note --rote hand-merge --as rowan --what 'merged 4399 by hand'"}},
	"rote": {Forms: []string{"[--sprint <S>] [--since <t>] [--until <t>]"}, Examples: []string{"rote --since 24h"}},

	"pitstop set":    {Forms: []string{"--stream <s,...> --why <text> [--sprint <S>]"}, Examples: []string{"pitstop set --stream console --why 'dev red'"}},
	"pitstop clear":  {Forms: []string{"--stream <s,...> --why <text> [--force]"}, Examples: []string{"pitstop clear --stream console --why 'dev green'"}},
	"pitstop status": {Forms: []string{"[--stream <s,...>]"}, Examples: []string{"pitstop status"}},

	"plan apply": {Forms: []string{"--sprint <S> --plan <file.tsv>"}, Examples: []string{"plan apply --sprint quack-0926 --plan plan.tsv"}},
	"plan show":  {Forms: []string{"--sprint <S>"}, Examples: []string{"plan show --sprint quack-0926"}},

	"pr record": {Forms: []string{"--repo <r> --n <n> --head <sha> [--base <b>] [--base-sha <sha>] [--stream <s>] [--task <id>]"}, Examples: []string{"pr record --repo nova-tools --n 4399 --head dcd918e6d --base dev"}},
	"pr lines":  {Forms: []string{"--repo <r> --n <n> [--add <typed line>]"}, Examples: []string{"pr lines --repo nova-tools --n 4399"}},
	"pr reap":   {Forms: []string{"[--sprint <S>] [--dry-run]"}, Examples: []string{"pr reap --dry-run"}},

	"preflight": {Forms: []string{"[--sprint <S>] [--fleet] [--all-yml <file>] [--retired <v,...>]"}, Examples: []string{"preflight --sprint quack-0926"}},

	"quack cut": {Forms: []string{"--n <N> --repo <owner/name> --stream <s> --sprint <S> [--tiers flash,pro] [--base dev] [--base-sha <sha40>] [--ref <owner/name#n>]"}, Examples: []string{"quack cut --n 4 --repo mas-bandwidth/nova-tools --stream quack --sprint quack-0926"}},
	"quack run": {Forms: []string{"--sprint <S> [--slots <bench>=<n>,...]"}, Examples: []string{"quack run --sprint quack-0926"}},

	"rank":      {Forms: []string{"[--sprint <S>] [--as <f>]"}, Examples: []string{"rank --sprint quack-0926"}},
	"ready":     {Forms: []string{"[--sprint <S>] [--why <id>]"}, Examples: []string{"ready --sprint quack-0926", "ready --why nova-tools-4352"}},
	"reconcile": {Forms: []string{"[--once] [--host <name>] [--readers <f,...>] [--metrics-addr <host:port>]"}, Examples: []string{"reconcile --once"}},

	"read brief": {Forms: []string{"--ids <task> [--sprint <S>] [--out <dir>] [--mirror <dir>]", "--repo <r> --n <n> [--out <dir>] [--mirror <dir>]", "--pr <n> [--repo <r>] [--issue <ref>] [--no-github]"},
		Examples: []string{"read brief --ids nova-tools-4352", "read brief --repo nova-tools --n 4399 --out /tmp/brief"}},
	"read post":   {Forms: []string{"--repo <r> --n <n> --line <typed line> [--no-github] [--owner <o>]", "--file <scores.tsv> [--no-github]"}, Examples: []string{"read post --repo nova-tools --n 4399 --line 'SCORE who=emma head=dcd918e6 score=9/10' --no-github"}},
	"read digest": {Forms: []string{"--repo <r> --n <n> [--sprint <S>] [--head <sha>] [--base-ref <ref>]"}, Examples: []string{"read digest --repo nova-tools --n 4399"}},
	"read carry":  {Forms: []string{"--repo <r> --n <n> [--sprint <S>] [--base-ref <ref>]"}, Examples: []string{"read carry --repo nova-tools --n 4399"}},

	"redis":     {Forms: []string{"<redis command...>"}, Examples: []string{"redis ZCARD ws:console:ready"}},
	"redis-cli": {Forms: []string{"-- <redis command...>"}, Examples: []string{"redis-cli -- ZCARD ws:console:ready"}},

	"result check":       {Forms: []string{"<RESULT.md|-> [--kind <k>]"}, Examples: []string{"result check RESULT.md"}},
	"result show":        {Forms: []string{"--sprint <S> --ids <label>"}, Examples: []string{"result show --sprint quack-0926 --ids nova-tools-4352"}},
	"result disposition": {Forms: []string{"<RESULT.md|->"}, Examples: []string{"result disposition RESULT.md"}},
	"result contract":    {Forms: []string{"[--markdown]"}, Examples: []string{"result contract --markdown"}},

	"review post": {Forms: []string{"--ids <primary> --verdict <recut|redeal|reassign:<consumer>|drop> --why <text>"}, Examples: []string{"review post --ids nova-tools-4352 --verdict recut --why 'wrong base'"}},

	"route report": {Forms: []string{"--sprint <S>"}, Examples: []string{"route report --sprint quack-0926"}},
	"routes":       {Forms: []string{"[--rung <r>] [--tier <t>] [--type <t>] [--check <route>] [--ids <id>]"}, Examples: []string{"routes", "routes --rung pro"}},

	"scope keep":   {Forms: []string{"--stream <s,...> [--why <text>]"}, Examples: []string{"scope keep --stream console"}},
	"scope park":   {Forms: []string{"--stream <s,...> --why <text> [--checkpoint <file>]"}, Examples: []string{"scope park --stream console --why 'out of scope today'"}},
	"scope unpark": {Forms: []string{"--stream <s,...> [--why <text>]"}, Examples: []string{"scope unpark --stream console"}},
	"scope ls":     {Examples: []string{"scope ls"}},

	"self update": {Forms: []string{"[--sha <sha>] [--from <checkout>] [--allow-branch]"}, Examples: []string{"self update --from ~/rowan-working/nova-tools"}},

	"spec mark": {Forms: []string{"--ref <repo>#<n> --rev <k> --as <f> --score <s> [--stream <s>] [--sprint <S>]"}, Examples: []string{"spec mark --ref nova-tools#4352 --rev 2 --as emma --score 10"}},
	"spec list": {Forms: []string{"[--stream <s>]"}, Examples: []string{"spec list --stream console"}},

	"sprint open":   {Forms: []string{"--sprint <S> [--from <work-set.lisp>]"}, Examples: []string{"sprint open --sprint quack-0926"}},
	"sprint close":  {Forms: []string{"--sprint <S>"}, Examples: []string{"sprint close --sprint quack-0926"}},
	"sprint fold":   {Forms: []string{"--sprint <S>"}, Examples: []string{"sprint fold --sprint quack-0926"}},
	"sprint status": {Forms: []string{"[--sprint <S>] [--now <unix>]"}, Examples: []string{"sprint status"}},
	"sprint clear":  {Forms: []string{"--why <text> [--force] [--checkpoint <file>]"}, Examples: []string{"sprint clear --why 'new sprint'"}},

	"stream ls":     {Forms: []string{"[--tree] [--expand]"}, Examples: []string{"stream ls", "stream ls --tree"}},
	"stream order":  {Forms: []string{"--stream <s,...> [--why <text>]", "--show"}, Examples: []string{"stream order --stream console,probe-a", "stream order --show"}},
	"stream rename": {Forms: []string{"--stream <old> --name <new> [--why <text>]"}, Examples: []string{"stream rename --stream probe-a --name probe-b"}},
	"stream open":   {Forms: []string{"--repo <owner/repo> --stream <s,...> [--base dev] [--dry-run]"}, Examples: []string{"stream open --repo mas-bandwidth/nova-tools --stream probe-a --dry-run"}},
	"stream rebase": {Forms: []string{"--repo <owner/repo> --stream <s,...> [--base dev]"}, Examples: []string{"stream rebase --repo mas-bandwidth/nova-tools --stream probe-a"}},
	"stream pr":     {Forms: []string{"--repo <owner/repo> --stream <s,...> [--base dev] [--dry-run]"}, Examples: []string{"stream pr --repo mas-bandwidth/nova-tools --stream probe-a"}},
	"stream status": {Forms: []string{"--repo <owner/repo>"}, Examples: []string{"stream status --repo mas-bandwidth/nova-tools"}},
	"stream close":  {Forms: []string{"--repo <owner/repo> --stream <s,...>"}, Examples: []string{"stream close --repo mas-bandwidth/nova-tools --stream probe-a"}},

	"task push": {Forms: []string{"--ids <id> --title <t> [--kind work|read] [--to <f>] [--depends-on <ids>] (the one task store)",
		"--as <worker> --ids <id> [--stream <s>] [--to friend:<f>] [--waiting | --depends-on <ids>] [--kind <k>] [--title <t>] [--issue <file|->] [--route pro|flash|friend] (a task card)"},
		Examples: []string{"task push --ids probe-1 --title 'probe one'", "task push --as friend:rowan --ids probe-1 --stream probe-a --title 'probe one'"}},
	"task take":    {Forms: []string{"[--as friend:<f> | --as bench:<b>] [--ids <id>] [--n <k>]"}, Examples: []string{"task take --n 1", "task take --as bench:studio --n 1"}},
	"task beat":    {Forms: []string{"--ids <id> [--as <worker>]"}, Examples: []string{"task beat --ids probe-1"}},
	"task done":    {Forms: []string{"--ids <id> --evidence <text> [--pr <n> [--head <sha>]]"}, Examples: []string{"task done --ids probe-1 --evidence 'PR 4399' --pr 4399"}},
	"task land":    {Forms: []string{"(--ids <id> | --stream <s>) --sha <merge sha>"}, Examples: []string{"task land --stream probe-a --sha dcd918e6"}},
	"task cancel":  {Forms: []string{"--ids <id> --why <text>"}, Examples: []string{"task cancel --ids probe-1 --why 'cut twice'"}},
	"task block":   {Forms: []string{"--ids <id> (--on <conditions> | --why <text>)"}, Examples: []string{"task block --ids probe-1 --why 'waits on dev green'"}},
	"task unblock": {Forms: []string{"--ids <id>"}, Examples: []string{"task unblock --ids probe-1"}},
	"task front":   {Forms: []string{"--ids <id>"}, Examples: []string{"task front --ids probe-1"}},
	"task move":    {Forms: []string{"--ids <id> (--to friend:<f> | --stream <s> | --where <w> [--ok ok|fail] [--why <text>])"}, Examples: []string{"task move --ids probe-1 --where ready"}},
	"task expire":  {Forms: []string{"[--as friend:<f>,...]"}, Examples: []string{"task expire"}},
	"task ls":      {Forms: []string{"[--stream <s> | --as friend:<f>] [--where <w>]"}, Examples: []string{"task ls", "task ls --stream probe-a --where ready"}},
	"task fsck":    {Forms: []string{"--sprint <S> [--repair]"}, Examples: []string{"task fsck --sprint quack-0926"}},
	"task list":    {Forms: []string{"--as <f> [--state <s>] [--sprint <S>] (the one task store)"}, Examples: []string{"task list --as rowan"}},
	"task width":   {Forms: []string{"--as <f> --slots <n>"}, Examples: []string{"task width --as rowan --slots 32"}},

	"verbs unused": {Forms: []string{"--tools <dir> --repo <clone> --receipts <dir> [--days 14]", "--check --repo <clone>"}, Examples: []string{"verbs unused --check --repo ~/rowan-working/nova-tools"}},

	"why":   {Forms: []string{"(--ids <unit> | --ref <repo>#<n>) --sprint <S>"}, Examples: []string{"why --ids nova-tools-4352 --sprint quack-0926"}},
	"width": {Forms: []string{"[--sprint <S>] [--as <f>]"}, Examples: []string{"width"}},

	"worker pause":  {Forms: []string{"--as <worker>"}, Examples: []string{"worker pause --as bench:hulk"}},
	"worker resume": {Forms: []string{"--as <worker>"}, Examples: []string{"worker resume --as bench:hulk"}},
	"worker show":   {Forms: []string{"[--as <worker>]"}, Examples: []string{"worker show --as bench:hulk"}},

	"ws counts":     {Forms: []string{"[--sprint <S>]"}, Examples: []string{"ws counts"}},
	"ws checkpoint": {Forms: []string{"--out <file.tsv>"}, Examples: []string{"ws checkpoint --out ws-0926.tsv"}},
	"ws show":       {Forms: []string{"--order [--stream <s>]"}, Examples: []string{"ws show --order --stream probe-a"}},
}
