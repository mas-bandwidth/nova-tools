#!/usr/bin/env bash
# provision-mac-bench.sh <ssh-host> <runner-count>
#
# Idempotent base-toolchain + GitHub Actions runner provisioning for an Intel
# or Apple Silicon Mac fleet bench. Runs entirely from the Studio over
# `ssh -n <host> '<cmd>'` as user nova (NOPASSWD sudo); the one exception is
# the runner registration token, which is minted here and piped over ssh
# stdin without -n (never echoed, never written to disk, never printed).
#
# Does NOT join the tailnet (`tailscale up`) and does NOT edit any workflow.
# Prebuilt official binaries only, checksums verified where published.
#
# Derived from the manual batman provisioning session; see INSTALL-batman.md
# next to this script for the narrative record of that first run.

set -euo pipefail

log() { printf 'provision: %s\n' "$*" >&2; }
die() { printf 'provision: FATAL: %s\n' "$*" >&2; exit 1; }

if [ $# -ne 2 ]; then
  echo "usage: $0 <ssh-host> <runner-count>" >&2
  exit 1
fi
HOST="$1"
RUNNER_COUNT="$2"
case "$RUNNER_COUNT" in
  ''|*[!0-9]*) die "runner-count must be a positive integer, got '$RUNNER_COUNT'" ;;
esac
[ "$RUNNER_COUNT" -ge 1 ] || die "runner-count must be >= 1"

REPO="mas-bandwidth/nova-tools"
RUNNER_VERSION="2.337.0"   # actions/runner version; bump here to move the whole fleet

# ---------------------------------------------------------------------------
# Phase 1 (remote, no secrets): toolchain, directories, profiles, pmset,
# nova-tools clone+build, mirrors, runner tarball+unpack. Prints two lines on
# stdout only (VERSIONS:... and NEEDS_REGISTRATION:...); all narration is on
# stderr so it streams live through ssh while stdout stays parseable.
# ---------------------------------------------------------------------------
build_phase1() {
  cat <<'REMOTE_EOF'
set -euo pipefail
log() { printf 'provision: %s\n' "$*" >&2; }
HOST="$1"
RUNNER_COUNT="$2"
REPO="mas-bandwidth/nova-tools"
RUNNER_VERSION="$3"

log "detecting arch"
case "$(uname -m)" in
  x86_64) GOARCH=amd64; X64ARM=x64; SBCLARCH=x86-64 ;;
  arm64)  GOARCH=arm64; X64ARM=arm64; SBCLARCH=arm64 ;;
  *) log "unsupported arch $(uname -m)"; exit 1 ;;
esac

log "creating directories"
mkdir -p "$HOME/.local/bin" "$HOME/rowan-working/tmp" "$HOME/rowan-swarm-root" \
  "$HOME/nova-bench/mirror" "$HOME/nova-bench/src" "$HOME/nova-bench/cache/go-build" \
  "$HOME/nova-bench/cache/go-mod" "$HOME/sdk"
export PATH="$HOME/.local/bin:$PATH"

# --- jq: bootstrapped with grep/sed since jq itself isn't installed yet ---
jq_target_ver() {
  local json
  json=$(curl -fsSL https://api.github.com/repos/jqlang/jq/releases/latest)
  printf '%s' "$json" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"jq-([^"]+)".*/\1/'
}
JQ_VER=$(jq_target_ver)
if [ -x "$HOME/.local/bin/jq" ] && "$HOME/.local/bin/jq" --version 2>/dev/null | grep -q "jq-${JQ_VER}\$"; then
  log "jq ${JQ_VER} already installed, skipping"
else
  log "installing jq ${JQ_VER}"
  JQ_JSON=$(curl -fsSL https://api.github.com/repos/jqlang/jq/releases/latest)
  JQ_ASSET="jq-macos-${GOARCH}"
  JQ_URL=$(printf '%s' "$JQ_JSON" | grep -o '"browser_download_url": *"[^"]*'"$JQ_ASSET"'"' | head -1 | sed -E 's/.*"(https[^"]+)"/\1/')
  JQ_SUMS_URL=$(printf '%s' "$JQ_JSON" | grep -o '"browser_download_url": *"[^"]*sha256sum.txt"' | head -1 | sed -E 's/.*"(https[^"]+)"/\1/')
  [ -n "$JQ_URL" ] || { log "no jq asset for $JQ_ASSET"; exit 1; }
  curl -fsSL -o /tmp/jq-bin "$JQ_URL"
  curl -fsSL -o /tmp/jq-sums.txt "$JQ_SUMS_URL"
  JQ_SHA=$(grep "$JQ_ASSET\$" /tmp/jq-sums.txt | awk '{print $1}')
  echo "${JQ_SHA}  /tmp/jq-bin" | shasum -a 256 -c -
  cp /tmp/jq-bin "$HOME/.local/bin/jq"; chmod +x "$HOME/.local/bin/jq"
  rm -f /tmp/jq-bin /tmp/jq-sums.txt
fi

# --- nova-tools: clone/fetch dev (also gives us go.mod for the Go version) ---
NT_DIR="$HOME/nova-bench/src/nova-tools"
log "syncing nova-tools dev branch"
if [ -d "$NT_DIR/.git" ]; then
  git -C "$NT_DIR" fetch --quiet origin dev
  git -C "$NT_DIR" checkout --quiet dev
  git -C "$NT_DIR" reset --quiet --hard origin/dev
else
  git clone --quiet --branch dev "https://github.com/${REPO}.git" "$NT_DIR"
fi
NOVA_SHA=$(git -C "$NT_DIR" rev-parse HEAD | cut -c1-12)

# --- Go: version = highest stable release >= go.mod's pinned minimum ---
GO_MIN=$(awk '/^go /{print $2; exit}' "$NT_DIR/go.mod")
[ -n "$GO_MIN" ] || { log "could not read go directive from go.mod"; exit 1; }
GO_JSON=$(curl -fsSL "https://go.dev/dl/?mode=json&include=all")
GO_VERSION=$(printf '%s' "$GO_JSON" | jq -r '.[] | select(.stable==true) | .version' | sed 's/^go//' \
  | awk -v min="$GO_MIN" '
      BEGIN{split(min,m,"."); minmaj=m[1]+0; minmin=m[2]+0; bestmaj=-1; bestmin=-1; bestpat=-1; best=""}
      {
        split($0,v,".");
        maj=v[1]+0; mnr=(v[2]==""?0:v[2]+0); pat=(v[3]==""?0:v[3]+0);
        if (maj>minmaj || (maj==minmaj && mnr>=minmin)) {
          if (maj>bestmaj || (maj==bestmaj && mnr>bestmin) || (maj==bestmaj && mnr==bestmin && pat>bestpat)) {
            best=$0; bestmaj=maj; bestmin=mnr; bestpat=pat
          }
        }
      }
      END{print best}')
[ -n "$GO_VERSION" ] || { log "no stable go release satisfies go.mod minimum $GO_MIN"; exit 1; }
GO_DIR="$HOME/sdk/go${GO_VERSION}"
if [ -x "$GO_DIR/bin/go" ] && "$GO_DIR/bin/go" version | grep -q "go${GO_VERSION} "; then
  log "go ${GO_VERSION} already installed, skipping"
else
  log "installing go ${GO_VERSION} (arch ${GOARCH})"
  GO_TAR="go${GO_VERSION}.darwin-${GOARCH}.tar.gz"
  GO_SHA=$(printf '%s' "$GO_JSON" | jq -r --arg f "$GO_TAR" '.[] | .files[]? | select(.filename==$f) | .sha256' | head -1)
  [ -n "$GO_SHA" ] || { log "no sha256 for $GO_TAR"; exit 1; }
  curl -fsSL -o "/tmp/$GO_TAR" "https://go.dev/dl/${GO_TAR}"
  echo "${GO_SHA}  /tmp/${GO_TAR}" | shasum -a 256 -c -
  # Deletion guard (tools must be safe): only ever a versioned Go directory under $HOME/sdk, never a bare or short path.
  # Anchored regex, not a glob: a glob's * matches slashes, so "$HOME/sdk/go1.27.1/../.." passed the first version of this guard.
  if ! [[ "$GO_DIR" =~ ^"$HOME"/sdk/go[0-9]+(\.[0-9]+){1,2}$ ]]; then log "refusing to remove unexpected GO_DIR=$GO_DIR"; exit 1; fi
  rm -rf "$GO_DIR"
  mkdir -p "$GO_DIR"
  tar -C "$GO_DIR" --strip-components=1 -xzf "/tmp/$GO_TAR"
  rm -f "/tmp/$GO_TAR"
fi
export PATH="$GO_DIR/bin:$PATH"
export GOCACHE="$HOME/nova-bench/cache/go-build"
export GOMODCACHE="$HOME/nova-bench/cache/go-mod"

# --- gh ---
GH_JSON=$(curl -fsSL https://api.github.com/repos/cli/cli/releases/latest)
GH_VER=$(printf '%s' "$GH_JSON" | jq -r '.tag_name' | sed 's/^v//')
if [ -x "$HOME/.local/bin/gh" ] && "$HOME/.local/bin/gh" --version 2>/dev/null | head -1 | grep -q " ${GH_VER} "; then
  log "gh ${GH_VER} already installed, skipping"
else
  log "installing gh ${GH_VER}"
  GH_ASSET="gh_${GH_VER}_macOS_${GOARCH}.zip"
  GH_URL=$(printf '%s' "$GH_JSON" | jq -r --arg n "$GH_ASSET" '.assets[] | select(.name==$n) | .browser_download_url')
  GH_SUMS_URL=$(printf '%s' "$GH_JSON" | jq -r --arg n "gh_${GH_VER}_checksums.txt" '.assets[] | select(.name==$n) | .browser_download_url')
  [ -n "$GH_URL" ] || { log "no gh asset for $GH_ASSET"; exit 1; }
  curl -fsSL -o /tmp/gh.zip "$GH_URL"
  curl -fsSL -o /tmp/gh-sums.txt "$GH_SUMS_URL"
  GH_SHA=$(grep "$GH_ASSET\$" /tmp/gh-sums.txt | awk '{print $1}')
  echo "${GH_SHA}  /tmp/gh.zip" | shasum -a 256 -c -
  rm -rf /tmp/gh-extract && mkdir -p /tmp/gh-extract
  unzip -q -o /tmp/gh.zip -d /tmp/gh-extract
  cp /tmp/gh-extract/gh_*/bin/gh "$HOME/.local/bin/gh"; chmod +x "$HOME/.local/bin/gh"
  rm -rf /tmp/gh.zip /tmp/gh-sums.txt /tmp/gh-extract
fi

# --- sops ---
SOPS_JSON=$(curl -fsSL https://api.github.com/repos/getsops/sops/releases/latest)
SOPS_TAG=$(printf '%s' "$SOPS_JSON" | jq -r '.tag_name')
SOPS_VER=${SOPS_TAG#v}
if [ -x "$HOME/.local/bin/sops" ] && "$HOME/.local/bin/sops" --version 2>&1 | head -1 | grep -q " ${SOPS_VER} "; then
  log "sops ${SOPS_VER} already installed, skipping"
else
  log "installing sops ${SOPS_VER}"
  SOPS_ASSET="sops-${SOPS_TAG}.darwin.${GOARCH}"
  SOPS_URL=$(printf '%s' "$SOPS_JSON" | jq -r --arg n "$SOPS_ASSET" '.assets[] | select(.name==$n) | .browser_download_url')
  SOPS_SUMS_URL=$(printf '%s' "$SOPS_JSON" | jq -r --arg n "sops-${SOPS_TAG}.checksums.txt" '.assets[] | select(.name==$n) | .browser_download_url')
  [ -n "$SOPS_URL" ] || { log "no sops asset for $SOPS_ASSET"; exit 1; }
  curl -fsSL -o /tmp/sops-bin "$SOPS_URL"
  curl -fsSL -o /tmp/sops-sums.txt "$SOPS_SUMS_URL"
  SOPS_SHA=$(grep "$SOPS_ASSET\$" /tmp/sops-sums.txt | awk '{print $1}')
  echo "${SOPS_SHA}  /tmp/sops-bin" | shasum -a 256 -c -
  cp /tmp/sops-bin "$HOME/.local/bin/sops"; chmod +x "$HOME/.local/bin/sops"
  rm -f /tmp/sops-bin /tmp/sops-sums.txt
fi

# --- age: no published checksums.txt (sigsum proofs instead); HTTPS-only, sha recorded ---
AGE_JSON=$(curl -fsSL https://api.github.com/repos/FiloSottile/age/releases/latest)
AGE_TAG=$(printf '%s' "$AGE_JSON" | jq -r '.tag_name')
if [ -x "$HOME/.local/bin/age" ] && "$HOME/.local/bin/age" --version 2>&1 | grep -q "^${AGE_TAG}\$"; then
  log "age ${AGE_TAG} already installed, skipping"
else
  log "installing age ${AGE_TAG} (no published checksum; HTTPS from official release only)"
  AGE_ASSET="age-${AGE_TAG}-darwin-${GOARCH}.tar.gz"
  AGE_URL=$(printf '%s' "$AGE_JSON" | jq -r --arg n "$AGE_ASSET" '.assets[] | select(.name==$n) | .browser_download_url')
  [ -n "$AGE_URL" ] || { log "no age asset for $AGE_ASSET"; exit 1; }
  curl -fsSL -o /tmp/age.tar.gz "$AGE_URL"
  log "age.tar.gz sha256 (self-computed): $(shasum -a 256 /tmp/age.tar.gz | awk '{print $1}')"
  rm -rf /tmp/age-extract && mkdir -p /tmp/age-extract
  tar -xzf /tmp/age.tar.gz -C /tmp/age-extract
  cp /tmp/age-extract/age/age /tmp/age-extract/age/age-keygen "$HOME/.local/bin/"
  chmod +x "$HOME/.local/bin/age" "$HOME/.local/bin/age-keygen"
  rm -rf /tmp/age.tar.gz /tmp/age-extract
fi
AGE_VER="$AGE_TAG"

# --- sbcl: official binary from sbcl.org/platform-table if one exists for this arch ---
SBCL_HTML=$(curl -fsSL https://www.sbcl.org/platform-table.html || true)
SBCL_URL=$(printf '%s' "$SBCL_HTML" | grep -oE "https?://[^\"]*${SBCLARCH}-darwin-binary\.tar\.bz2" | head -1 || true)
if [ -z "$SBCL_URL" ]; then
  log "no official sbcl binary for arch ${SBCLARCH}: sbcl=pending"
  SBCL_VER=""
else
  SBCL_FILE=$(basename "$SBCL_URL")
  SBCL_VER=$(printf '%s' "$SBCL_FILE" | sed -E "s/sbcl-([0-9.]+)-.*/\1/")
  SBCL_DIR="$HOME/sdk/sbcl-${SBCL_VER}"
  if [ -x "$SBCL_DIR/bin/sbcl" ]; then
    log "sbcl ${SBCL_VER} already installed, skipping"
  else
    log "installing sbcl ${SBCL_VER} (no published checksum for this arch)"
    curl -fsSL -o /tmp/sbcl.tar.bz2 "$SBCL_URL"
    rm -rf /tmp/sbcl-extract && mkdir -p /tmp/sbcl-extract
    tar -xjf /tmp/sbcl.tar.bz2 -C /tmp/sbcl-extract
    (cd /tmp/sbcl-extract/sbcl-"${SBCL_VER}"-*-darwin && sh install.sh --prefix="$SBCL_DIR")
    rm -rf /tmp/sbcl.tar.bz2 /tmp/sbcl-extract
  fi
fi

# --- tailscale/tailscaled: go install, then system-daemon install (no tailnet join) ---
TS_JSON=$(curl -fsSL https://api.github.com/repos/tailscale/tailscale/releases/latest)
TS_VER=$(printf '%s' "$TS_JSON" | jq -r '.tag_name' | sed 's/^v//')
CUR_TS=""
if [ -x "$HOME/.local/bin/tailscale" ]; then
  CUR_TS=$("$HOME/.local/bin/tailscale" version 2>&1 | head -1 | sed 's/-.*//')
fi
if [ "$CUR_TS" = "$TS_VER" ]; then
  log "tailscale ${TS_VER} already built, skipping"
else
  log "building tailscale ${TS_VER} with go install"
  GOBIN="$HOME/.local/bin" go install tailscale.com/cmd/tailscale@latest tailscale.com/cmd/tailscaled@latest
fi
NEED_DAEMON=0
sudo launchctl print system/com.tailscale.tailscaled >/dev/null 2>&1 || NEED_DAEMON=1
if [ -x /usr/local/bin/tailscaled ] && [ -x "$HOME/.local/bin/tailscaled" ] && ! cmp -s /usr/local/bin/tailscaled "$HOME/.local/bin/tailscaled"; then
  NEED_DAEMON=1
fi
if [ "$NEED_DAEMON" = "1" ]; then
  log "installing tailscaled system daemon (not joining the tailnet)"
  sudo "$HOME/.local/bin/tailscaled" install-system-daemon
else
  log "tailscaled system daemon already current, skipping"
fi

# --- shell profiles: one guarded, marker-delimited block ---
log "checking shell profile PATH block"
MARK_BEGIN="# nova-provision:managed-block:begin"
MARK_END="# nova-provision:managed-block:end"
SBCL_PATH_SEG=""
[ -n "$SBCL_VER" ] && SBCL_PATH_SEG=":\$HOME/sdk/sbcl-${SBCL_VER}/bin"
BLOCK=$(cat <<BLOCKEOF
${MARK_BEGIN}
if [ -x /usr/local/bin/brew ]; then eval "\$(/usr/local/bin/brew shellenv)"; fi
export PATH="\$HOME/.local/bin:\$HOME/sdk/go${GO_VERSION}/bin${SBCL_PATH_SEG}:\$HOME/go/bin:\$PATH"
export GOCACHE="\$HOME/nova-bench/cache/go-build"
export GOMODCACHE="\$HOME/nova-bench/cache/go-mod"
${MARK_END}
BLOCKEOF
)
for f in "$HOME/.zprofile" "$HOME/.bash_profile" "$HOME/.zshenv"; do
  touch "$f"
  if grep -qF "$MARK_BEGIN" "$f"; then
    log "profile block already present in $f, skipping"
  else
    printf '\n%s\n' "$BLOCK" >> "$f"
    log "added profile block to $f"
  fi
done

# --- pmset: only touch what differs from target ---
# (no associative arrays: macOS ships bash 3.2, which lacks them)
log "checking pmset"
CUR_PMSET=$(pmset -g custom 2>/dev/null || true)
NEEDS_PMSET=0
for kv in "sleep 0" "disksleep 0" "displaysleep 1" "womp 1" "autorestart 1"; do
  set -- $kv
  k=$1; v=$2
  printf '%s\n' "$CUR_PMSET" | grep -qE "^[[:space:]]*${k}[[:space:]]+${v}\$" || NEEDS_PMSET=1
done
if [ "$NEEDS_PMSET" = "1" ]; then
  log "applying pmset targets"
  sudo pmset -a sleep 0 disksleep 0 displaysleep 1 womp 1 autorestart 1
else
  log "pmset already at target, skipping"
fi

# --- nova-tools: build every cmd/* once per new dev sha ---
CUR_SWARM_OUT=""
[ -x "$HOME/.local/bin/nova-swarm" ] && CUR_SWARM_OUT=$("$HOME/.local/bin/nova-swarm" version 2>&1 || true)
if printf '%s' "$CUR_SWARM_OUT" | grep -q "$NOVA_SHA"; then
  log "nova-tools cmd/* already built for sha $NOVA_SHA, skipping"
else
  log "building nova-tools cmd/* for sha $NOVA_SHA"
  for d in "$NT_DIR"/cmd/*/; do
    name=$(basename "$d")
    go -C "$NT_DIR" build -o "$HOME/.local/bin/$name" "./cmd/$name"
  done
fi

# --- mirrors: public repos only ---
mirror_repo() {
  local name="$1" url="$2" dir="$HOME/nova-bench/mirror/${1}.git"
  if GIT_TERMINAL_PROMPT=0 git ls-remote "$url" HEAD >/dev/null 2>&1; then
    if [ -d "$dir" ]; then
      log "refreshing mirror $name"
      git --git-dir="$dir" remote update --prune >/dev/null 2>&1
    else
      log "mirroring $name"
      GIT_TERMINAL_PROMPT=0 git clone --quiet --mirror "$url" "$dir"
    fi
  else
    log "$name not public/reachable without credentials, skipping mirror"
  fi
}
mirror_repo nova-tools "https://github.com/${REPO}.git"
mirror_repo schema "https://github.com/mas-bandwidth/schema.git"
mirror_repo nova-seed "https://github.com/mas-bandwidth/nova-seed.git"

# --- runner tarball: download once, unpack N dirs, find which need registering ---
log "checking actions-runner tarball"
CACHE_TAR="$HOME/nova-bench/cache/actions-runner-osx-${X64ARM}-${RUNNER_VERSION}.tar.gz"
RUNNER_REL_JSON=$(curl -fsSL "https://api.github.com/repos/actions/runner/releases/tags/v${RUNNER_VERSION}")
RUNNER_ASSET="actions-runner-osx-${X64ARM}-${RUNNER_VERSION}.tar.gz"
RUNNER_SHA=$(printf '%s' "$RUNNER_REL_JSON" | jq -r '.body' \
  | grep -oE "${RUNNER_ASSET} <!-- BEGIN SHA osx-${X64ARM} -->[0-9a-f]{64}" | grep -oE '[0-9a-f]{64}$')
RUNNER_URL=$(printf '%s' "$RUNNER_REL_JSON" | jq -r --arg n "$RUNNER_ASSET" '.assets[] | select(.name==$n) | .browser_download_url')
[ -n "$RUNNER_URL" ] && [ -n "$RUNNER_SHA" ] || { log "could not resolve runner asset/sha for $RUNNER_ASSET"; exit 1; }
if [ -f "$CACHE_TAR" ] && echo "${RUNNER_SHA}  ${CACHE_TAR}" | shasum -a 256 -c - >/dev/null 2>&1; then
  log "actions-runner ${RUNNER_VERSION} tarball already cached and verified, skipping download"
else
  log "downloading actions-runner ${RUNNER_VERSION} (${X64ARM})"
  curl -fsSL -o "$CACHE_TAR" "$RUNNER_URL"
  echo "${RUNNER_SHA}  ${CACHE_TAR}" | shasum -a 256 -c -
fi

NEED=""
for i in $(seq 1 "$RUNNER_COUNT"); do
  d="$HOME/runner-nova-tools-$i"
  if [ ! -d "$d" ] || [ -z "$(ls -A "$d" 2>/dev/null)" ]; then
    log "unpacking runner dir $i"
    mkdir -p "$d"
    tar -xzf "$CACHE_TAR" -C "$d"
  fi
  [ -f "$d/runsvc.sh" ] || { cp "$d/bin/runsvc.sh" "$d/runsvc.sh"; chmod +x "$d/runsvc.sh"; }
  mkdir -p "$d/_work" "$d/_diag"
  [ -f "$d/_work/.metadata_never_index" ] || touch "$d/_work/.metadata_never_index"
  if [ ! -f "$d/.runner" ]; then
    NEED="${NEED:+$NEED,}$i"
  fi
done
[ -f "$HOME/nova-bench/cache/.metadata_never_index" ] || touch "$HOME/nova-bench/cache/.metadata_never_index"

echo "VERSIONS:go=go${GO_VERSION};gh=${GH_VER};jq=${JQ_VER};sops=${SOPS_VER};age=${AGE_VER};sbcl=${SBCL_VER:-pending};tailscale=${TS_VER};nova_sha=${NOVA_SHA}"
echo "NEEDS_REGISTRATION:${NEED}"
REMOTE_EOF
}

# ---------------------------------------------------------------------------
# Phase 3 (remote, no secrets, always runs): LaunchDaemons for all N runners,
# Spotlight markers, probes. Rediscovers Go/SBCL install dirs by globbing
# since it is a separate ssh invocation from phase 1.
# ---------------------------------------------------------------------------
build_phase3() {
  cat <<'REMOTE_EOF'
set -euo pipefail
log() { printf 'provision: %s\n' "$*" >&2; }
HOST="$1"
RUNNER_COUNT="$2"
REPO="mas-bandwidth/nova-tools"

GO_DIR=$(ls -d "$HOME"/sdk/go*/ 2>/dev/null | head -1 | sed 's:/$::')
SBCL_DIR=$(ls -d "$HOME"/sdk/sbcl-*/ 2>/dev/null | head -1 | sed 's:/$::')
[ -n "$GO_DIR" ] || { log "no go install found under ~/sdk"; exit 1; }
export PATH="$GO_DIR/bin:$HOME/.local/bin:$PATH"
export GOCACHE="$HOME/nova-bench/cache/go-build"
export GOMODCACHE="$HOME/nova-bench/cache/go-mod"

log "checking LaunchDaemons for $RUNNER_COUNT runner(s)"
for i in $(seq 1 "$RUNNER_COUNT"); do
  RUNNER_DIR="$HOME/runner-nova-tools-$i"
  LABEL="com.nova.runner.${HOST}-nova-${i}"
  PLIST="/Library/LaunchDaemons/${LABEL}.plist"
  EXPECTED=$(cat <<PLISTEOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${LABEL}</string>
  <key>UserName</key>
  <string>nova</string>
  <key>WorkingDirectory</key>
  <string>${RUNNER_DIR}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${RUNNER_DIR}/runsvc.sh</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${RUNNER_DIR}/_diag/daemon.log</string>
  <key>StandardErrorPath</key>
  <string>${RUNNER_DIR}/_diag/daemon.log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>${HOME}/.local/bin:${GO_DIR}/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    <key>HOME</key>
    <string>${HOME}</string>
    <key>GOCACHE</key>
    <string>${HOME}/nova-bench/cache/go-build</string>
    <key>GOMODCACHE</key>
    <string>${HOME}/nova-bench/cache/go-mod</string>
  </dict>
</dict>
</plist>
PLISTEOF
)
  CUR=""
  [ -f "$PLIST" ] && CUR=$(sudo cat "$PLIST" 2>/dev/null || true)
  if [ "$CUR" = "$EXPECTED" ] && sudo launchctl print "system/${LABEL}" >/dev/null 2>&1; then
    log "runner $i LaunchDaemon already correct and loaded, skipping"
  else
    log "writing/loading runner $i LaunchDaemon"
    printf '%s\n' "$EXPECTED" | sudo tee "$PLIST" >/dev/null
    sudo chown root:wheel "$PLIST"
    sudo chmod 644 "$PLIST"
    sudo launchctl bootout system "$LABEL" >/dev/null 2>&1 || true
    sudo launchctl bootstrap system "$PLIST"
  fi
done

[ -f "$HOME/nova-bench/cache/.metadata_never_index" ] || touch "$HOME/nova-bench/cache/.metadata_never_index"

log "running probes"
NT_DIR="$HOME/nova-bench/src/nova-tools"
if go -C "$NT_DIR" build ./... 2>/tmp/probe-build.log && go -C "$NT_DIR" vet ./cmd/nova-bus/ 2>>/tmp/probe-build.log; then
  echo "PROBE go-build-vet: PASS"
else
  echo "PROBE go-build-vet: FAIL"
  tail -5 /tmp/probe-build.log >&2
fi
if go -C "$NT_DIR" test ./internal/ci/ ./internal/oneline/ >/tmp/probe-test.log 2>&1; then
  echo "PROBE go-test: PASS"
else
  echo "PROBE go-test: FAIL"
  tail -5 /tmp/probe-test.log >&2
fi
if [ -n "$SBCL_DIR" ] && "$SBCL_DIR/bin/sbcl" --non-interactive --eval '(progn (format t "sbcl-ok ~a~%" (lisp-implementation-version)) (sb-ext:exit))' 2>/tmp/probe-sbcl.log | grep -q sbcl-ok; then
  echo "PROBE sbcl: PASS"
else
  echo "PROBE sbcl: PENDING"
fi
REMOTE_EOF
}

run_remote() {
  # $1 = phase builder function name; remaining args passed to the remote script
  local body b64
  body=$("$1")
  b64=$(printf '%s' "$body" | base64 | tr -d '\n')
  ssh -n "$HOST" "echo $b64 | base64 -d | bash -s -- '$HOST' '$RUNNER_COUNT' '$RUNNER_VERSION'"
}

# Preflight: the developer command line tools (git, clang, make). A fresh Mac has none (superman, 2026-09-17; batman happened to
# have them, so the first version of this script never installed them). Headless install through softwareupdate; skipped when present.
log "preflight: command line tools on $HOST"
if ssh -n "$HOST" 'xcode-select -p >/dev/null 2>&1 && /usr/bin/git --version >/dev/null 2>&1'; then
  log "command line tools already installed, skipping"
else
  log "installing command line tools through softwareupdate (several minutes, a large download)"
  ssh -n "$HOST" 'set -e; sudo touch /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
    label=$(softwareupdate -l 2>/dev/null | grep -E "Label: Command Line Tools" | sed -E "s/^.*Label: //" | sort -V | tail -1)
    [ -n "$label" ] || { echo "no Command Line Tools label offered by softwareupdate"; exit 1; }
    echo "installing: $label"; sudo softwareupdate -i "$label" --verbose 2>&1 | grep -E "Installing|Installed|Done|Error|fail" | tail -5
    sudo rm -f /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
    xcode-select -p && /usr/bin/git --version' >&2 || { log "command line tools install FAILED on $HOST"; exit 1; }
fi
log "phase 1: toolchain, directories, nova-tools, mirrors, runner unpack on $HOST"
P1_OUT=$(run_remote build_phase1)
VERSIONS_LINE=$(printf '%s\n' "$P1_OUT" | grep '^VERSIONS:' || true)
NEEDS_LINE=$(printf '%s\n' "$P1_OUT" | grep '^NEEDS_REGISTRATION:' || true)
NEEDS=${NEEDS_LINE#NEEDS_REGISTRATION:}

if [ -n "$NEEDS" ]; then
  log "phase 2: registering runner(s) $NEEDS on $HOST (minting one token locally)"
  X64ARM=$(ssh -n "$HOST" 'case "$(uname -m)" in x86_64) echo x64;; arm64) echo arm64;; esac')
  [ -n "$X64ARM" ] || die "could not detect arch on $HOST"
  T=$(GH_CONFIG_DIR="$HOME/.config/gh-rowan" gh api -X POST "repos/${REPO}/actions/runners/registration-token" -q .token 2>/tmp/gh-token-err.$$) \
    || { grep -qi '403' /tmp/gh-token-err.$$ 2>/dev/null && die "registration-token API refused (403); stopping, not trying other credentials"; die "could not mint registration token"; }
  rm -f /tmp/gh-token-err.$$
  [ -n "$T" ] || die "registration token was empty"
  OLDIFS=$IFS
  IFS=','
  for i in $NEEDS; do
    IFS=$OLDIFS
    log "registering ${HOST}-nova-${i}"
    printf '%s' "$T" | ssh "$HOST" '
      read -r RTOK
      cd "$HOME/runner-nova-tools-'"$i"'"
      ./config.sh --unattended --url "https://github.com/'"$REPO"'" \
        --token "$RTOK" --name "'"$HOST"'-nova-'"$i"'" --labels "'"$HOST"',darwin-'"$X64ARM"'" \
        --work _work --replace >/tmp/config-'"$i"'.log 2>&1
      echo configured_'"$i"'=$?
    ' >&2
    IFS=','
  done
  IFS=$OLDIFS
  unset T
else
  log "phase 2: no runners need registration on $HOST, skipping"
fi

log "phase 3: LaunchDaemons and probes on $HOST"
P3_OUT=$(run_remote build_phase3)
printf '%s\n' "$P3_OUT"

log "querying GitHub for online runner count"
ONLINE=$(GH_CONFIG_DIR="$HOME/.config/gh-rowan" gh api "repos/${REPO}/actions/runners" --paginate \
  -q ".runners[] | select(.name | startswith(\"${HOST}-nova-\")) | select(.status==\"online\") | .name" \
  | wc -l | tr -d ' ')
echo "PROBE runners-online: ${ONLINE}/${RUNNER_COUNT}"

echo "provisioned ${HOST}: ${VERSIONS_LINE#VERSIONS:};runners=${ONLINE}/${RUNNER_COUNT}"
