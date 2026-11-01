#!/bin/sh
# Installs herdr-github for the herdr TUI, the Herdr app, or both.
#
#   sh install.sh          whichever of the two is on this computer (both: both)
#   sh install.sh --tui    the herdr plugin (herdr plugin install)
#   sh install.sh --app    the Herdr app's GitHub mod
#
# From anywhere, in one line:
#   curl -fsSL https://raw.githubusercontent.com/mrolafsson/herdr-github/main/install.sh | sh -s -- --app
#
# The app's mod needs nothing else installed but gh, signed in: it fetches the
# herdr-github it's part of onto each machine it works on the first time,
# checked against the release's checksums.
set -eu
REPO=mrolafsson/herdr-github

tui() {
	command -v herdr >/dev/null 2>&1 || { echo "herdr isn't installed: https://herdr.dev" >&2; return 1; }
	herdr plugin install "$REPO"
}

app() {
	command -v herdr-app >/dev/null 2>&1 || { echo "the Herdr app isn't installed: https://herdr.dev" >&2; return 1; }
	home=${HERDR_APP_HOME:-$HOME/.config/herdr-app}
	here=$(cd "$(dirname "$0")" 2>/dev/null && pwd || true)
	src="$here/app"
	if [ ! -d "$src/mods/github" ]; then
		# Run through curl: the latest release's copy.
		tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
		[ -n "$tag" ] || { echo "couldn't find herdr-github's latest release" >&2; return 1; }
		tmp=$(mktemp -d)
		trap 'rm -rf "$tmp"' EXIT
		curl -fsSL "https://github.com/$REPO/archive/refs/tags/$tag.tar.gz" | tar -xz -C "$tmp"
		src=$(echo "$tmp"/*/app)
		[ -d "$src/mods/github" ] || { echo "herdr-github $tag has no app mod: it's in 0.10.0 and later" >&2; return 1; }
	fi
	mkdir -p "$home/mods/github"
	cp -R "$src/mods/github/." "$home/mods/github/"
	keys "$src/keys.json" "$home/keys.json"
	herdr-app check "$home/mods/github"
	echo "GitHub is installed: ⌘G opens it."
}

# Adds the mod's keys to the person's keys.json: its sections and keys, never
# replacing a key already bound to something else (those are listed instead).
keys() {
	if [ ! -f "$2" ]; then
		cp "$1" "$2"
		return
	fi
	if ! command -v python3 >/dev/null 2>&1; then
		echo "Add these to $2 by hand:" >&2
		cat "$1" >&2
		return
	fi
	python3 - "$1" "$2" <<'PY'
import json, sys
new, path = json.load(open(sys.argv[1])), sys.argv[2]
have = json.load(open(path))
kept = []
for section, binds in new.items():
    mine = have.setdefault(section, {})
    for key, cmd in binds.items():
        if key in mine and mine[key] != cmd:
            kept.append(f"{section} {key}: yours ({mine[key]}) kept, GitHub's would be {cmd}")
        else:
            mine[key] = cmd
json.dump(have, open(path + ".tmp", "w"), indent=2, ensure_ascii=False)
import os; os.replace(path + ".tmp", path)
for k in kept: print("keys.json:", k, file=sys.stderr)
PY
}

case "${1:-}" in
--tui) tui ;;
--app) app ;;
"")
	did=
	command -v herdr >/dev/null 2>&1 && { tui; did=1; }
	command -v herdr-app >/dev/null 2>&1 && { app; did=1; }
	[ -n "$did" ] || { echo "Neither herdr nor the Herdr app is installed: https://herdr.dev" >&2; exit 1; }
	;;
*) echo "usage: sh install.sh [--tui | --app]" >&2; exit 2 ;;
esac
