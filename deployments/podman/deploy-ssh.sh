#!/usr/bin/env bash
# deploy-ssh.sh DIR: from the runner (deploy.yml), or by hand, runs a deploy on
# the server. It sends DIR (deploy.sh, the units, topbanana.env and for the demo
# seed/) over SSH's standard input into a folder of the run's own under the
# deploy user's home, ~/topbanana-deploy/<id>, then runs deploy.sh there. The
# secrets only travel in that stream: the command line, which every user on the
# server can read, holds DEPLOY_ENV, DEPLOY_DIR, IMAGE and PR_NUMBER only.
#
# It reads from its environment:
#   SSH_HOST, SSH_USER, SSH_KEY   the server, the environment's user and its
#                                 deploy key (in authorized_keys with restrict:
#                                 commands work, no terminal, no forwarding)
#   SSH_PORT                      22 unless set
#   DEPLOY_ENV, IMAGE, PR_NUMBER  passed on to deploy.sh (see there)
#   DEPLOY_ID                     the folder's name; GitHub's run id, attempt
#                                 and DEPLOY_ENV unless set
# The server's host key is taken as it comes on first contact, as appleboy's
# actions did before.
set -euo pipefail
export LC_ALL=C
umask 077

die() {
  echo "error: $*" >&2
  exit 1
}

[[ $# -eq 1 && -f "$1/deploy.sh" ]] || die "usage: deploy-ssh.sh DIR (a folder with deploy.sh)"
dir="$1"
for name in SSH_HOST SSH_USER SSH_KEY DEPLOY_ENV IMAGE; do
  [[ -n "${!name:-}" ]] || die "$name is not set"
done
port="${SSH_PORT:-22}"
id="${DEPLOY_ID:-${GITHUB_RUN_ID:-manual}-${GITHUB_RUN_ATTEMPT:-0}-$DEPLOY_ENV}"
# Each goes into a command line: plain words only, never an option.
[[ "$SSH_HOST" =~ ^[A-Za-z0-9][A-Za-z0-9.:-]*$ ]] || die "SSH_HOST is not a host name or an address"
[[ "$SSH_USER" =~ ^[a-z_][a-z0-9_-]*$ ]] || die "SSH_USER is not a user name"
[[ "$port" =~ ^[0-9]+$ ]] || die "SSH_PORT is not a number"
[[ "$id" =~ ^[A-Za-z0-9-]+$ ]] || die "DEPLOY_ID holds an unexpected character"
[[ "$DEPLOY_ENV" =~ ^[a-z-]+$ ]] || die "DEPLOY_ENV holds an unexpected character"
[[ "$IMAGE" =~ ^[a-z0-9./_-]+@sha256:[0-9a-f]{64}$ ]] || die "IMAGE is not pinned to a digest"
[[ "${PR_NUMBER:-}" =~ ^[0-9]*$ ]] || die "PR_NUMBER is not a number"

work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/deploy-ssh.XXXXXX")"
trap 'rm -rf "$work"' EXIT
# OpenSSH wants the last line break, which a pasted secret can lack.
printf '%s\n' "$SSH_KEY" | tr -d '\r' > "$work/key"
ssh_opts=(
  -i "$work/key" -p "$port"
  -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes
  -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="$work/known_hosts" -o GlobalKnownHostsFile=/dev/null
  -o ConnectTimeout=30 -o ServerAliveInterval=30 -o ServerAliveCountMax=4 -o LogLevel=ERROR
)
target="$SSH_USER@$SSH_HOST"
deploy_dir="topbanana-deploy/$id"

# The folder is private before anything is in it. No extended attributes or
# macOS metadata: the server's GNU tar would warn about each.
# shellcheck disable=SC2029 # the folder's name is meant to be filled in here
COPYFILE_DISABLE=1 tar -cf - --no-xattrs -C "$dir" . |
  ssh "${ssh_opts[@]}" "$target" \
    "umask 077 && mkdir -p \"\$HOME/$deploy_dir\" && tar -xf - -C \"\$HOME/$deploy_dir\""

# The server's login shell runs it, and its exit status is this one's.
# shellcheck disable=SC2029 # the values are checked above and meant to be filled in here
ssh "${ssh_opts[@]}" "$target" \
  "DEPLOY_ENV='$DEPLOY_ENV' DEPLOY_DIR='$deploy_dir' IMAGE='$IMAGE' PR_NUMBER='${PR_NUMBER:-}' sh \"\$HOME/$deploy_dir/deploy.sh\"" \
  < /dev/null
