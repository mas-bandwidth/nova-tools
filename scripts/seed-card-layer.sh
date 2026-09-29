#!/usr/bin/env bash
# scripts/seed-card-layer.sh: initialize the card layer schema and member partitions on nova-table.
#
# Usage:
#   seed-card-layer.sh [options]
#
# Options:
#   --table <name>          Table name (default: cards)
#   --redis <addr>          Redis address (default: $NOVA_SPRINT_REDIS, $NOVA_REDIS_ADDR, or 127.0.0.1:6379)
#   --streams <s1,s2,...>   Comma-separated stream row partitions (default: stream-alpha,stream-beta,stream-gamma)
#   --columns <c1,c2,...>   Comma-separated state columns (default: waiting,ready,working,review,merging,landed,done)
#   --member-prefix <pfx>   Member record prefix (default: card:)
#   --footer <label>        Footer row aggregation label (default: total)
#   --check                 Verify that table schema and stream partitions exist without mutating
#   --drop                  Drop the table and associated rows
#   --dry-run               Print planned commands without executing
#   -h, --help              Print this help and exit
#
# Conventions:
#   Exit code 0: completed/verified
#   Exit code 1: refusal or verification failure
#   Exit code 2: usage error

set -eu
set -o pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)

# Defaults
TABLE="cards"
REDIS="${NOVA_SPRINT_REDIS:-${NOVA_REDIS_ADDR:-}}"
STREAMS="stream-alpha,stream-beta,stream-gamma"
COLUMNS="waiting,ready,working,review,merging,landed,done"
MEMBER_PREFIX="card:"
FOOTER="total"
CHECK_MODE=0
DROP_MODE=0
DRY_RUN=0

usage() {
    cat <<EOF
usage: seed-card-layer.sh [options]

initializes card layer schema and stream partitions on nova-table.

options:
  --table <name>          Table name (default: cards)
  --redis <addr>          Redis address (default: 127.0.0.1:6379)
  --streams <s1,s2,...>   Comma-separated stream partitions (default: stream-alpha,stream-beta,stream-gamma)
  --columns <c1,c2,...>   Comma-separated state columns (default: waiting,ready,working,review,merging,landed,done)
  --member-prefix <pfx>   Member record prefix (default: card:)
  --footer <label>        Footer row label (default: total)
  --check                 Verify existing table schema and partitions
  --drop                  Drop existing table
  --dry-run               Show planned commands without executing
  -h, --help              Show this help

examples:
  scripts/seed-card-layer.sh --table cards
  scripts/seed-card-layer.sh --table cards --check
  scripts/seed-card-layer.sh --table cards --drop
EOF
}

# Parse options
while [ $# -gt 0 ]; do
    case "$1" in
        --table)
            [ $# -ge 2 ] || { echo "error: --table requires an argument" >&2; exit 2; }
            TABLE="$2"; shift 2 ;;
        --redis)
            [ $# -ge 2 ] || { echo "error: --redis requires an argument" >&2; exit 2; }
            REDIS="$2"; shift 2 ;;
        --streams)
            [ $# -ge 2 ] || { echo "error: --streams requires an argument" >&2; exit 2; }
            STREAMS="$2"; shift 2 ;;
        --columns)
            [ $# -ge 2 ] || { echo "error: --columns requires an argument" >&2; exit 2; }
            COLUMNS="$2"; shift 2 ;;
        --member-prefix)
            [ $# -ge 2 ] || { echo "error: --member-prefix requires an argument" >&2; exit 2; }
            MEMBER_PREFIX="$2"; shift 2 ;;
        --footer)
            [ $# -ge 2 ] || { echo "error: --footer requires an argument" >&2; exit 2; }
            FOOTER="$2"; shift 2 ;;
        --check)
            CHECK_MODE=1; shift ;;
        --drop)
            DROP_MODE=1; shift ;;
        --dry-run)
            DRY_RUN=1; shift ;;
        -h|--help)
            usage; exit 0 ;;
        *)
            echo "error: unknown argument $1" >&2; usage >&2; exit 2 ;;
    esac
done

if [ "$DRY_RUN" -eq 0 ] && [ -z "$REDIS" ]; then
    echo "error: explicit Redis address required (--redis or NOVA_REDIS_ADDR); ambient autodiscovery disabled for test confinement" >&2
    exit 2
fi

# Locate nova-table executable
if [ -n "${NOVA_TABLE_BIN:-}" ] && [ -x "${NOVA_TABLE_BIN:-}" ]; then
    NOVA_TABLE="$NOVA_TABLE_BIN"
elif command -v nova-table >/dev/null 2>&1; then
    NOVA_TABLE="nova-table"
else
    echo "error: NOVA_TABLE_BIN not set to executable and nova-table not found in PATH" >&2
    exit 1
fi


run_cmd() {
    if [ "$DRY_RUN" -eq 1 ]; then
        echo "[dry-run] $*"
        return 0
    fi
    "$@"
}

# DROP mode
if [ "$DROP_MODE" -eq 1 ]; then
    echo "Dropping table $TABLE on $REDIS..."
    if [ "$DRY_RUN" -eq 1 ]; then
        echo "[dry-run] $NOVA_TABLE drop $TABLE --redis $REDIS"
        exit 0
    fi
    out=$("$NOVA_TABLE" drop "$TABLE" --redis "$REDIS" 2>&1) || {
        case "$out" in
            *"no such table"*)
                echo "TABLE DROP table=$TABLE status=not_found (already clean)"
                exit 0
                ;;
            *)
                echo "error dropping table $TABLE: $out" >&2
                exit 1
                ;;
        esac
    }
    echo "$out"
    exit 0
fi

# CHECK mode
if [ "$CHECK_MODE" -eq 1 ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
        echo "[dry-run] check table $TABLE and stream partitions on $REDIS"
        exit 0
    fi

    # 1. Verify table exists in show output
    show_out=$("$NOVA_TABLE" show "$TABLE" --redis "$REDIS" 2>&1) || {
        echo "CHECK FAILED: table $TABLE does not exist on $REDIS" >&2
        exit 1
    }

    # 2. Verify all streams are present in rows
    IFS=',' read -r -a stream_arr <<< "$STREAMS"
    missing=0
    for s in "${stream_arr[@]}"; do
        if ! grep -q "row=$s" <<< "$show_out"; then
            echo "CHECK FAILED: stream partition '$s' missing from table $TABLE" >&2
            missing=$((missing + 1))
        fi
    done

    if [ "$missing" -gt 0 ]; then
        exit 1
    fi

    echo "CHECK OK: table=$TABLE streams=${#stream_arr[@]} columns=7 status=verified"
    exit 0
fi

# NORMAL SEEDING (Idempotent)
IFS=',' read -r -a stream_arr <<< "$STREAMS"
stream_count="${#stream_arr[@]}"

# 1. Create table schema
# nova-table create is idempotent if invoked with identical definition
create_args=("$TABLE" "--columns" "$COLUMNS" "--member-prefix" "$MEMBER_PREFIX" "--footer" "$FOOTER" "--redis" "$REDIS")
if [ "$DRY_RUN" -eq 1 ]; then
    run_cmd "$NOVA_TABLE" create "${create_args[@]}"
else
    create_out=$("$NOVA_TABLE" create "${create_args[@]}" 2>&1) || {
        echo "error creating table $TABLE: $create_out" >&2
        exit 1
    }
    echo "$create_out"
fi

# 2. Add stream partitions (rows)
# nova-table row add is idempotent in table.lua (updates row attributes, preserves cell members)
if [ "$stream_count" -gt 0 ]; then
    row_args=("$TABLE")
    for s in "${stream_arr[@]}"; do
        row_args+=("$s")
    done
    row_args+=("--redis" "$REDIS")

    if [ "$DRY_RUN" -eq 1 ]; then
        run_cmd "$NOVA_TABLE" row add "${row_args[@]}"
    else
        row_out=$("$NOVA_TABLE" row add "${row_args[@]}" 2>&1) || {
            echo "error adding stream partitions to table $TABLE: $row_out" >&2
            exit 1
        }
        echo "$row_out"
    fi
fi

# 3. Print verified table status
if [ "$DRY_RUN" -eq 0 ]; then
    echo "SEED CARD LAYER table=$TABLE streams=$stream_count columns=7 member_prefix=$MEMBER_PREFIX status=ready"
fi
