#!/usr/bin/env bash
# write-env.sh FILE NAME...: on the runner (deploy.yml), writes one NAME=value
# line for each NAME from the environment into FILE, private (umask 077): the
# env file deploy.sh installs as ~/.config/topbanana/topbanana.env, which the
# unit reads (EnvironmentFile=). Every name in REQUIRED (space-separated) must
# be set and not empty; an optional one may be empty. Podman takes each value
# as it is, up to the end of the line, with no quoting: a value with a line
# break is refused, not guessed at. No value is ever printed.
set -euo pipefail

die() {
  echo "error: $*" >&2
  exit 1
}

[[ $# -ge 2 ]] || die "usage: write-env.sh FILE NAME..."
file="$1"
shift
for name in "$@"; do
  [[ "$name" =~ ^[A-Z][A-Z0-9_]*$ ]] || die "'$name' is not a variable name"
done
for name in ${REQUIRED:-}; do
  [[ -n "${!name:-}" ]] ||
    die "$name is empty or unset; refusing to deploy (set it on the GitHub environment)"
done
for name in "$@"; do
  case "${!name:-}" in
    *$'\n'* | *$'\r'*) die "$name holds a line break, which an env file cannot hold; refusing to deploy" ;;
  esac
done

umask 077
mkdir -p "$(dirname "$file")"
for name in "$@"; do
  printf '%s=%s\n' "$name" "${!name:-}"
done > "$file"
