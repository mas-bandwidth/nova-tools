#!/usr/bin/env bash
# fleet-install-tools.sh <sha> [benches...]: build the nova tools ONCE for linux/amd64 on the build bench, cache the binaries by sha,
# install them on every Linux bench, and verify each bench reports that sha. Works from any directory. One line per bench, exit 1 on any mismatch.
# Why (pit stop 2026-09-17): the upgrade loop built on space into ~/nova-bench/bin, a directory nothing used, and installed nowhere;
# all four Linux benches ran six-hour-old tools while the coordinator believed they were current. Glenn: build once, cache the binary, use that.
set -u; SHA=${1:-}; shift || true; BUILD=${FLEET_BUILD_BENCH:-hulk}; BENCHES=${*:-hulk vision space mini}
say() { printf 'fleet-install: %s\n' "$*" >&2; }
case "$SHA" in ''|*[!0-9a-f]*) echo "FLEET-INSTALL REFUSED: sha must be 7 to 40 hex characters: '$SHA'"; exit 2;; esac; [ ${#SHA} -ge 7 ] && [ ${#SHA} -le 40 ] || { echo "FLEET-INSTALL REFUSED: sha length"; exit 2; }
for b in $BUILD $BENCHES; do printf %s "$b" | grep -qE '^[A-Za-z0-9._-]+$' || { echo "FLEET-INSTALL REFUSED: bad bench name '$b'"; exit 2; }; done
say "building $SHA once on $BUILD (skipped when its cache already holds this sha)"
ssh -o BatchMode=yes -o ConnectTimeout=10 "$BUILD" "SHA=$SHA bash -s" <<'REMOTE' || { echo "FLEET-INSTALL FAIL build on $BUILD"; exit 1; }
set -eu; export PATH=$HOME/go/bin:/usr/local/go/bin:$HOME/.local/bin:$PATH; SRC=$HOME/nova-bench/src/nova-tools; M=$HOME/nova-bench/mirror/nova-tools.git
[ -d "$SRC/.git" ] || git clone -q $([ -d "$M" ] && echo "--reference $M") https://github.com/mas-bandwidth/nova-tools.git "$SRC"
cd "$SRC"; git fetch -q origin dev; full=$(git rev-parse --verify -q "$SHA^{commit}") || { echo "unknown sha $SHA" >&2; exit 1; }
OUT=$HOME/nova-bench/build/$full; if [ -f "$OUT/.complete" ]; then echo "cache hit $full" >&2; else
  git checkout -q "$full"; rm -rf -- "$HOME/nova-bench/build/.partial-$full"; mkdir -p "$HOME/nova-bench/build/.partial-$full"
  n=0; for d in cmd/*/; do t=$(basename "$d"); CGO_ENABLED=0 go build -trimpath -o "$HOME/nova-bench/build/.partial-$full/$t" "./cmd/$t"; n=$((n+1)); done
  "$HOME/nova-bench/build/.partial-$full/nova-swarm" version | grep -q "${full:0:12}" || { echo "built binary does not carry the sha" >&2; exit 1; }
  echo "$n" > "$HOME/nova-bench/build/.partial-$full/.complete"; rm -rf -- "$OUT"; mv "$HOME/nova-bench/build/.partial-$full" "$OUT"; echo "built $n tools at $full" >&2; fi
ls -d $HOME/nova-bench/build/*/ 2>/dev/null | head -n -5 | while read -r old; do case "$old" in "$HOME"/nova-bench/build/[0-9a-f]*/) rm -rf -- "$old";; esac; done
REMOTE
FULL=$(ssh -n -o BatchMode=yes "$BUILD" "cd ~/nova-bench/src/nova-tools && git rev-parse $SHA^{commit}") || exit 1
rc=0; for b in $BENCHES; do
  if [ "$b" != "$BUILD" ]; then say "copying the cached build to $b"; ssh -n -o BatchMode=yes "$BUILD" "tar -C ~/nova-bench/build/$FULL -cf - ." | ssh -o BatchMode=yes -o ConnectTimeout=10 "$b" "mkdir -p ~/nova-bench/build/$FULL && tar -C ~/nova-bench/build/$FULL -xf -" || { echo "FLEET-INSTALL FAIL $b copy"; rc=1; continue; }; fi
  say "installing on $b (atomic rename per tool; running processes keep their old inode)"
  line=$(ssh -n -o BatchMode=yes -o ConnectTimeout=10 "$b" "set -u; B=~/nova-bench/build/$FULL; n=0; for f in \$B/nova-*; do t=\$(basename \$f); for dir in ~/.local/bin ~/go/bin; do [ -d \$dir ] || continue; [ \$dir = ~/go/bin ] && [ ! -e \$dir/\$t ] && continue; cp -p \$f \$dir/.\$t.new && mv -f \$dir/.\$t.new \$dir/\$t; done; n=\$((n+1)); done; echo \"\$n \$(~/.local/bin/nova-swarm version | head -1) | go-bin: \$([ -x ~/go/bin/nova-swarm ] && ~/go/bin/nova-swarm version | head -1 | grep -oE '[0-9a-f]{12}' | tail -1 || echo none)\"") || { echo "FLEET-INSTALL FAIL $b install"; rc=1; continue; }
  case "$line" in *"${FULL:0:12}"*) echo "FLEET-INSTALL OK $b tools=${line%% *} sha=${FULL:0:12}";; *) echo "FLEET-INSTALL MISMATCH $b wanted ${FULL:0:12} got: ${line:0:120}"; rc=1;; esac
done; exit $rc
