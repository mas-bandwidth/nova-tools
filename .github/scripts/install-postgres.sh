#!/usr/bin/env bash
# install-postgres.sh: put initdb, pg_ctl and postgres on PATH for the
# nova-config functional tests (internal/nsprint/testutil/pg), the way
# install-redis-server.sh puts redis-server there.
#
# The tests start a private server on loopback under the test's directory.
# They do not dial the fleet Postgres. A runner that already has the binaries
# is unchanged. Linux uses apt (postgresql-16 where the archive has it, else
# the distribution's postgresql), macOS uses Homebrew's postgresql@16. Debian
# and Ubuntu install the binaries under /usr/lib/postgresql/<v>/bin, off
# PATH, so the directory is published to GITHUB_PATH. Several runners share
# one machine, so the install takes a lock: apt does not survive a
# concurrent dpkg.
set -euo pipefail

have() {
	command -v pg_ctl >/dev/null 2>&1 && command -v initdb >/dev/null 2>&1 && command -v postgres >/dev/null 2>&1
}

# find_bin prints the directory of a versioned install that is off PATH.
find_bin() {
	local d
	for d in /usr/lib/postgresql/16/bin /usr/lib/postgresql/17/bin /usr/lib/postgresql/15/bin /usr/lib/postgresql/14/bin \
		/opt/homebrew/opt/postgresql@16/bin /usr/local/opt/postgresql@16/bin /usr/pgsql-16/bin; do
		if [ -x "$d/pg_ctl" ] && [ -x "$d/initdb" ] && [ -x "$d/postgres" ]; then
			echo "$d"
			return 0
		fi
	done
	for d in /usr/lib/postgresql/*/bin; do
		if [ -x "$d/pg_ctl" ] && [ -x "$d/initdb" ] && [ -x "$d/postgres" ]; then
			echo "$d"
			return 0
		fi
	done
	return 1
}

publish() {
	local dir
	if have; then
		dir=$(dirname "$(command -v pg_ctl)")
	else
		dir=$(find_bin) || { echo "postgres binaries still not found after install" >&2; exit 1; }
		export PATH="$dir:$PATH"
	fi
	if [ -n "${GITHUB_PATH:-}" ]; then
		echo "$dir" >> "$GITHUB_PATH"
	fi
	echo "postgres $dir/postgres $("$dir/postgres" --version)"
}

if have || find_bin >/dev/null; then
	publish
	exit 0
fi

lock=/tmp/nova-postgres-install.lock
if [ -d "$lock" ]; then
	m=$(stat -c %Y "$lock" 2>/dev/null || stat -f %m "$lock")
	now=$(date +%s)
	if [ $((now - m)) -gt 600 ]; then
		rmdir "$lock" 2>/dev/null || true
	fi
fi
n=0
while ! mkdir "$lock" 2>/dev/null; do
	if have || find_bin >/dev/null; then
		publish
		exit 0
	fi
	n=$((n + 1))
	if [ "$n" -gt 40 ]; then
		echo "timed out waiting to install postgres" >&2
		exit 1
	fi
	sleep 3
done
cleanup() { rmdir "$lock" 2>/dev/null || true; }
trap cleanup EXIT

if have || find_bin >/dev/null; then
	publish
	exit 0
fi

if command -v apt-get >/dev/null 2>&1; then
	pkg=postgresql-16
	if ! apt-cache show "$pkg" >/dev/null 2>&1; then
		pkg=postgresql
	fi
	if ! apt-get update -qq || ! apt-get install -y -qq "$pkg"; then
		sudo -n apt-get update -qq
		sudo -n apt-get install -y -qq "$pkg"
	fi
elif command -v brew >/dev/null 2>&1; then
	HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_UPGRADE=1 brew install postgresql@16
else
	echo "no apt-get and no brew: install postgresql (16) by hand and put initdb, pg_ctl and postgres on PATH, or set NOVA_PG_BIN to their directory" >&2
	exit 1
fi

publish
