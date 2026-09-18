#!/bin/zsh
. "$HOME/rowan-working/bin/safe-rm.sh"
# child-clone.sh — the CHILD KIT.
#
# Makes one ready scratch clone of the schema repository for one child, so no
# brief has to repeat the environment.  One clone per child; never shared.
#
#   child-clone.sh <name> [--branch <base>] [--pr <n>] [--new <branch>]
#                         [--legs c,cpp,go,rust,dart,js,java,cs,elixir]
#   child-clone.sh --gc <name>
#
# Everything it knows about the Studio's toolchains lives in ENVIRONMENT below.

emulate -L zsh
setopt err_return no_unset pipe_fail

# ---------------------------------------------------------------- ENVIRONMENT
SCHEMA_REMOTE='git@github.com:mas-bandwidth/schema.git'
DEFAULT_BASE='fixed-table-form'
WORKING='/Users/glenn/rowan-working'
DIST='/Users/Shared/schema-dist'          # the target repo/dist must point at
SCRATCH_DEFAULT="$HOME/rowan-working/tmp/children" # was a long-dead session scratchpad, outside the roots safe_rm may delete in
SERIALIZE_C_CURRENT="$HOME/rowan-working/serialize.c-current"
SERIALIZE_C_REMOTE='git@github.com:mas-bandwidth/serialize.c.git'

# the sibling checkouts the Makefiles reach for as ../serialize*
typeset -a SIBLINGS
SIBLINGS=(serialize serialize.c serialize.go serialize.cs serialize.rs serialize.js)

# leg -> the leg's fixed-form make gate, as the Makefile spells it today
typeset -A LEG_GATE
LEG_GATE=(
  cpp     'tables-fixedform'
  c       'tables-c-fixedform'
  go      'tables-go-fixed-form'
  rust    'tables-rust-fixedform'
  dart    'tables-dart-fixed-form'
  js      'tables-js-fixed-form'
  java    'tables-java-fixedform'
  cs      'tables-cs-leg'
  elixir  'tables-elixir-fixed-form'
)

die()  { print -u2 -- "child-clone: $*"; return 1 }
note() { print -- "  $*" }

# ---------------------------------------------------------------- ARGUMENTS
NAME=''
BASE="$DEFAULT_BASE"
PR=''
NEWBRANCH=''
LEGS=''
GC=0

while (( $# )); do
  case "$1" in
    --gc)     GC=1; NAME="${2-}"; shift 2 || die '--gc needs a name';;
    --branch) BASE="${2-}";      shift 2 || die '--branch needs a branch';;
    --pr)     PR="${2-}";        shift 2 || die '--pr needs a number';;
    --new)    NEWBRANCH="${2-}"; shift 2 || die '--new needs a branch';;
    --legs)   LEGS="${2-}";      shift 2 || die '--legs needs a list';;
    -h|--help)
      sed -n '3,13p' "$0" | sed 's/^# \{0,1\}//'
      return 0;;
    -*) die "unknown flag $1";;
    *)  [[ -z "$NAME" ]] || die "two names given ($NAME, $1)"; NAME="$1"; shift;;
  esac
done

[[ -n "$NAME" ]] || die 'usage: child-clone.sh <name> [--branch b|--pr n|--new b] [--legs a,b]'
case "$NAME" in
  */*|.|..) die "name must be a single path segment, not '$NAME'";;
esac

SCRATCH_ROOT="${CHILD_SCRATCH:-$SCRATCH_DEFAULT}"
HOME_DIR="$SCRATCH_ROOT/$NAME"
REPO="$HOME_DIR/repo"

# ---------------------------------------------------------------- --gc
if (( GC )); then
  [[ -d "$HOME_DIR" ]] || { print -- "child-clone: nothing at $HOME_DIR"; return 0 }
  case "$HOME_DIR" in
    "$SCRATCH_ROOT"/*) ;;
    *) die "refusing to remove $HOME_DIR: not under $SCRATCH_ROOT";;
  esac
  [ -n "$SCRATCH_ROOT" ] && [ -n "$NAME" ] || exit 2
  safe_rm "$HOME_DIR" && [[ ! -e "$HOME_DIR" ]] || die "could NOT remove $HOME_DIR (safe_rm only deletes below ~/rowan-working or ~/rowan-swarm-root; set CHILD_SCRATCH under one of them)"
  print -- "child-clone: removed $HOME_DIR"
  return 0
fi

# ---------------------------------------------------------------- one per child
[[ -e "$HOME_DIR" ]] && die "$HOME_DIR already exists — one clone per child, never shared (child-clone.sh --gc $NAME to reclaim it)"

(( ${+commands[git]} )) || die 'git not on PATH'
[[ -n "$PR" && -n "$NEWBRANCH" ]] && die '--pr and --new are exclusive'
[[ -n "$PR" ]] && { (( ${+commands[gh]} )) || die 'gh not on PATH, needed for --pr' }

# legs default to every leg
if [[ -z "$LEGS" ]]; then
  LEGS='c,cpp,go,rust,dart,js,java,cs,elixir'
fi
typeset -a LEG_LIST
LEG_LIST=( ${(s:,:)LEGS} )
for leg in $LEG_LIST; do
  [[ -n "${LEG_GATE[$leg]-}" ]] || die "unknown leg '$leg' (known: ${(kj:,:)LEG_GATE})"
done

print -- "child-clone: $NAME -> $HOME_DIR"
mkdir -p -- "$HOME_DIR" "$HOME_DIR/scratch"

# ---------------------------------------------------------------- (1) the clone
# --reference-if-able off an on-disk checkout makes an 87M clone cheap;
# --dissociate copies the objects in so the clone depends on nothing.
typeset -a refargs
refargs=()
if [[ -d "$WORKING/schema/.git" && -z "${CHILD_CLONE_NO_REFERENCE-}" ]]; then
  refargs=( --reference-if-able "$WORKING/schema" --dissociate )
fi

git clone --quiet $refargs --branch "$BASE" -- "$SCHEMA_REMOTE" "$REPO" \
  || die "clone of $SCHEMA_REMOTE at $BASE failed"

if [[ -n "$PR" ]]; then
  ( cd -- "$REPO" && gh pr checkout "$PR" ) || die "gh pr checkout $PR failed"
elif [[ -n "$NEWBRANCH" ]]; then
  ( cd -- "$REPO" && git checkout -q -b "$NEWBRANCH" ) || die "git checkout -b $NEWBRANCH failed"
fi
BRANCH="$( git -C "$REPO" rev-parse --abbrev-ref HEAD )"
note "repo/      $BRANCH  $( git -C "$REPO" log --oneline -1 )"

# ---------------------------------------------------------------- (2) siblings
# serialize.c in rowan-working is stale (58 commits behind origin/main today);
# when it is behind, keep one current clone and point the link there.
serialize_c_target="$WORKING/serialize.c"
serialize_c_why='rowan-working'
if [[ -d "$serialize_c_target/.git" ]]; then
  behind="$( git -C "$serialize_c_target" rev-list --count HEAD..origin/main 2>/dev/null || print 0 )"
  if [[ "$behind" != 0 ]]; then
    if [[ -d "$SERIALIZE_C_CURRENT/.git" ]]; then
      git -C "$SERIALIZE_C_CURRENT" fetch --quiet origin main 2>/dev/null || true
      git -C "$SERIALIZE_C_CURRENT" reset --hard --quiet origin/main 2>/dev/null || true
    else
      git clone --quiet --branch main -- "$SERIALIZE_C_REMOTE" "$SERIALIZE_C_CURRENT" \
        || die "could not clone a current serialize.c into $SERIALIZE_C_CURRENT"
    fi
    serialize_c_target="$SERIALIZE_C_CURRENT"
    serialize_c_why="serialize.c-current (rowan-working was $behind commits behind origin/main)"
  fi
fi

typeset -a MISSING
MISSING=()
for s in $SIBLINGS; do
  if [[ "$s" == serialize.c ]]; then
    target="$serialize_c_target"
  else
    target="$WORKING/$s"
  fi
  if [[ -d "$target" ]]; then
    ln -s -- "$target" "$HOME_DIR/$s"
    if [[ "$s" == serialize.c ]]; then
      note "$s  -> $target   [$serialize_c_why]"
    else
      note "$s  -> $target"
    fi
  else
    MISSING+=( "$s (no $target)" )
    note "$s  MISSING: $target does not exist"
  fi
done

# ---------------------------------------------------------------- (3) dist
if [[ -d "$DIST" ]]; then
  ln -s -- "$DIST" "$REPO/dist"
  note "repo/dist  -> $DIST"
else
  MISSING+=( "dist ($DIST)" )
  note "repo/dist  MISSING: $DIST does not exist"
fi

# ---------------------------------------------------------------- (4) env.sh
# checked, so a child learns about a hole here and not inside a gate
typeset -a ENV_MISSING
ENV_MISSING=()
check() { [[ -e "$2" ]] || ENV_MISSING+=( "$1 ($2)" ) }
check JAVA_HOME     "$DIST/jdk-21.0.12.1/Contents/Home/bin/java"
check ELIXIR_BIN    "$DIST/elixir-1.20.4/bin/elixir"
check ERL_BIN       "$DIST/otp-29.0.5/bin/erl"
check DART          "$DIST/dart-sdk-3.13.2/bin/dart"
check NODE          "$DIST/node-v26.7.0-darwin-arm64/bin/node"
check dotnet        "$HOME/.local/bin/dotnet"

cat > "$HOME_DIR/env.sh" <<ENV_EOF
# env.sh for child '$NAME' — source it in EVERY shell:
#     cd $REPO && source ../env.sh
# Absolute on purpose: a child's cwd moves, the toolchains do not.

export CHILD_NAME='$NAME'
export CHILD_HOME='$HOME_DIR'
export CHILD_REPO='$REPO'

# scratch/ — every temporary file goes here.  Never /tmp.
export CHILD_SCRATCH_DIR='$HOME_DIR/scratch'
export TMPDIR="\$CHILD_SCRATCH_DIR/"
mkdir -p "\$CHILD_SCRATCH_DIR"

# the sibling checkouts the Makefiles reach for as ../serialize*
export SERIALIZE='$HOME_DIR/serialize'
export SERIALIZE_C='$HOME_DIR/serialize.c'
# make/rust.mk and the Go rules PREPEND ../../../ to these two, so they must stay RELATIVE
# (the sibling link beside repo/ resolves them); absolute here doubles the path (2026-09-11).
export SERIALIZE_GO='../serialize.go'
export SERIALIZE_RS='../serialize.rs'
export SERIALIZE_CS='$HOME_DIR/serialize.cs'
export SERIALIZE_JS='$HOME_DIR/serialize.js'

# java — jdk 21 from dist
export JAVA_HOME='$DIST/jdk-21.0.12.1/Contents/Home'
export JAVA="\$JAVA_HOME/bin/java"
export JAVAC="\$JAVA_HOME/bin/javac"

# elixir / beam — the Makefile builds BEAM_PATH from these
export ELIXIR_BIN='$DIST/elixir-1.20.4/bin/elixir'
export ERL_BIN='$DIST/otp-29.0.5/bin'
export BEAM_PATH='$DIST/otp-29.0.5/bin:$DIST/elixir-1.20.4/bin'

# dart and node come out of dist/ (repo/dist -> $DIST)
export DART='$DIST/dart-sdk-3.13.2/bin/dart'
export NODE='$DIST/node-v26.7.0-darwin-arm64/bin/node'

# dotnet lives in ~/.local/bin
export DOTNET="\$HOME/.local/bin/dotnet"
export DOTNET_CLI_TELEMETRY_OPTOUT=1
export DOTNET_NOLOGO=1

# a skipped corpus is a FAILURE, not a pass
export SCHEMA_REQUIRE_CORPUS=1

export PATH="\$JAVA_HOME/bin:\$BEAM_PATH:\$HOME/.local/bin:$DIST/node-v26.7.0-darwin-arm64/bin:\$PATH"
ENV_EOF

note "env.sh     written"
if (( ${#ENV_MISSING} )); then
  for m in $ENV_MISSING; do note "env.sh     NOT FOUND: $m"; done
fi

# ---------------------------------------------------------------- (5) README
typeset -a GATES
GATES=()
for leg in $LEG_LIST; do GATES+=( "${LEG_GATE[$leg]}" ); done

print --
print -- '--- paste into the brief -----------------------------------------------'
cat <<README_EOF
CHILD KIT $NAME — clone: $REPO (branch $BRANCH; siblings at $HOME_DIR/serialize*)
source: cd $REPO && source ../env.sh — in every shell, before anything else
scratch: every temporary file goes in $HOME_DIR/scratch — never /tmp
gate 1: gofmt -l . prints nothing
gate 2: make ${GATES} passes
gate 3: rm -f bin/schema before any regeneration, and let make rebuild the compiler
deadline: push whatever is green at deadline minus 10% — a green part beats a red whole
commit trailer: Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
PR body trailer: 🤖 Generated with [Claude Code](https://claude.com/claude-code)
forbidden: no merge (never --auto), no nova-bus, no spawn_task, no /tmp, never touch $WORKING/schema
README_EOF
print -- '-----------------------------------------------------------------------'

if (( ${#MISSING} + ${#ENV_MISSING} )); then
  print --
  print -u2 -- "child-clone: holes in the environment:"
  for m in $MISSING $ENV_MISSING; do print -u2 -- "  - $m"; done
fi
