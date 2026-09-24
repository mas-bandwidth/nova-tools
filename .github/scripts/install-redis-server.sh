#!/usr/bin/env bash
# install-redis-server.sh: put redis-server on PATH for nova-sprint controls (#3113).
#
# The controls start a private server. They do not dial the fleet store. A runner
# that already has the binary is unchanged. Linux uses apt, macOS uses Homebrew,
# and a machine with neither (the Intel benches have no Homebrew) builds the
# pinned source into ~/.local/bin, which persists on a self-hosted runner.
# Several runners share one machine, so the install takes a lock: apt does not
# survive a concurrent dpkg.
set -euo pipefail

have() { command -v redis-server >/dev/null 2>&1; }

publish() {
	local bin dir
	bin=$(command -v redis-server)
	dir=$(dirname "$bin")
	if [ -n "${GITHUB_PATH:-}" ]; then
		echo "$dir" >> "$GITHUB_PATH"
	fi
	echo "redis-server $bin"
}

if have; then
	publish
	exit 0
fi

lock=/tmp/nova-redis-server-install.lock
if [ -d "$lock" ]; then
	m=$(stat -c %Y "$lock" 2>/dev/null || stat -f %m "$lock")
	now=$(date +%s)
	if [ $((now - m)) -gt 600 ]; then
		rmdir "$lock" 2>/dev/null || true
	fi
fi
n=0
while ! mkdir "$lock" 2>/dev/null; do
	if have; then
		publish
		exit 0
	fi
	n=$((n + 1))
	if [ "$n" -gt 40 ]; then
		echo "timed out waiting to install redis-server" >&2
		exit 1
	fi
	sleep 3
done
cleanup() { rmdir "$lock" 2>/dev/null || true; }
trap cleanup EXIT

if have; then
	publish
	exit 0
fi

if command -v apt-get >/dev/null 2>&1; then
	if ! apt-get update -qq || ! apt-get install -y -qq redis-server; then
		sudo -n apt-get update -qq
		sudo -n apt-get install -y -qq redis-server
	fi
elif command -v brew >/dev/null 2>&1; then
	HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_UPGRADE=1 brew install redis
	brewbin="$(brew --prefix)/bin"
	export PATH="$brewbin:$PATH"
	if [ -n "${GITHUB_PATH:-}" ]; then
		echo "$brewbin" >> "$GITHUB_PATH"
	fi
else
	ver=8.0.5
	dest="${HOME:?}/.local/bin"
	mkdir -p "$dest"
	work=$(mktemp -d)
	trap 'cleanup; rm -rf "$work"' EXIT
	curl -fsSL "https://download.redis.io/releases/redis-${ver}.tar.gz" -o "$work/redis.tar.gz"
	tar -xzf "$work/redis.tar.gz" -C "$work"
	jobs=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)
	make -C "$work/redis-$ver" -j"$jobs" redis-server
	cp "$work/redis-$ver/src/redis-server" "$dest/redis-server"
	chmod 0755 "$dest/redis-server"
	export PATH="$dest:$PATH"
fi

have || { echo "redis-server still not on PATH after install" >&2; exit 1; }
publish
