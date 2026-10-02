#!/bin/bash
# The notes of 2026-10-02, written into nova-config: the reason each route the
# first real sprint disabled was disabled, and why superman is held, as the
# `note` of the row (internal/config, migration 0015; docs/SPEC-CONFIG.md, "The
# note"). The reasons were recorded on nova-tools#5101 until the field existed;
# the numbers below are that issue's comments (ok / total over the day's work
# and reads), nothing re-measured.
#
# The coordinator runs this once, after `nova-config migrate` has applied 0015
# to the store, as the actor that did the choosing:
#
#   NOVA_PG_DSN=postgres://... tools/notes-2026-10-02.sh --dry-run
#   NOVA_PG_DSN=postgres://... tools/notes-2026-10-02.sh
#
# It reads and writes through nova-config only: one `route set <name> --note`
# per disabled route and one `machine set superman --note`, each recorded in the
# row's history under the actor. A route that is not disabled when the script
# reaches it is skipped (a note that says why it is off must not sit on a route
# that is on), and a route that is no row is counted missing and exits 1 at the
# end; nothing is enabled, disabled or removed here. Run again, it writes the
# same notes again, one more history row each.
#
# env: NOVA_CONFIG the binary (default nova-config); NOTES_AS the actor (default
#      rowan, else NOVA_FRIEND); NOTES_CONN extra flags for every call, such as
#      --pg <dsn> or --file <path> (default none: NOVA_PG_DSN names the store).
set -eu

dry=""
case "${1:-}" in
"") ;;
--dry-run) dry="--dry-run" ;;
*) echo "notes-2026-10-02 REFUSED: unknown argument ${1}; want nothing or --dry-run" >&2; exit 2 ;;
esac
bin="${NOVA_CONFIG:-nova-config}"
as="${NOTES_AS:-${NOVA_FRIEND:-rowan}}"
conn="${NOTES_CONN:-}"
wrote=0 skipped=0 missing=0

# route_note <route> <note>: set the note of a route that is disabled.
route_note() {
	local name=$1 note=$2 row
	# shellcheck disable=SC2086
	if ! row=$("$bin" route show "$name" $conn); then
		echo "NOTES MISSING route=$name" >&2
		missing=$((missing + 1))
		return 0
	fi
	case "$row" in
	*" enabled=false "*) ;;
	*)
		echo "NOTES SKIP route=$name reason=enabled"
		skipped=$((skipped + 1))
		return 0
		;;
	esac
	# shellcheck disable=SC2086
	"$bin" route set "$name" --note "$note" --as "$as" $dry $conn
	wrote=$((wrote + 1))
}

# 1:25 PM EDT, comment of 2026-10-02 on nova-tools#5101
route_note flash-mimo26pro-openrouter "disabled 2026-10-02 1:25 PM ET by rowan: 3 ok of 7 (4 ran to the 1200 s deadline with no RESULT.md, each a 20-minute slot on superman); not a limit or balance refusal, so not a rest; re-enable only with a measured reason; nova-tools#5101"
# 2:05 PM, the body of #5101 (revs 300, 301), and the 2:25 PM record
route_note flash-luna6-opencode "disabled 2026-10-02 2:05 PM ET by rowan (rev 300), Glenn: feel free to disable luna: 2 ok of 12 on the day's record; not suited to flash work on this card shape; nova-tools#5101"
route_note flash-luna6-openrouter "disabled 2026-10-02 2:05 PM ET by rowan (rev 301), Glenn: feel free to disable luna: 4 ok of 14 on the day's record; not suited to flash work on this card shape; nova-tools#5101"
# the body of #5101 (rev 304): Glenn, use mercury only direct
route_note flash-mercury-openrouter "disabled 2026-10-02 by rowan (rev 304), Glenn: use mercury only direct: direct 8 ok of 8, via openrouter 10 ok of 16; flash-mercury (direct) stays enabled; nova-tools#5101"
# 2:25 PM EDT, the six of the record over about 1,200 outcomes
route_note flash-nemotron-openrouter "disabled 2026-10-02 2:25 PM ET by rowan: 4 ok of 52 on the day's record (48 ended with no result); re-enable only with a measured reason; nova-tools#5101"
route_note flash-luna56-opencode "disabled 2026-10-02 2:25 PM ET by rowan: 32 ok of 58 on the day's record (55%, under the 64% every kept route reached); re-enable only with a measured reason; nova-tools#5101"
route_note flash-gemini31lite-openrouter "disabled 2026-10-02 2:25 PM ET by rowan: 34 ok of 58 on the day's record (59%, under the 64% every kept route reached); re-enable only with a measured reason; nova-tools#5101"
route_note flash-mimo26-openrouter "disabled 2026-10-02 2:25 PM ET by rowan: 36 ok of 62 on the day's record (58%, under the 64% every kept route reached); re-enable only with a measured reason; nova-tools#5101"

# superman: held, with the load that was measured (nova-tools#5101, the reader widths)
# shellcheck disable=SC2086
"$bin" machine set superman --note "held 1:46 PM ET 2026-10-02: reads kernel-bound (reads run the Go gate; load 80 on 36 cores at 1:48 PM, 40 of 64 cards back with no result); member 8, readers 4 and 4; nova-tools#5101" --as "$as" $dry $conn

echo "NOTES DONE routes_written=$wrote routes_skipped=$skipped routes_missing=$missing machine=superman dry_run=$([ -n "$dry" ] && echo true || echo false)"
[ "$missing" -eq 0 ]
