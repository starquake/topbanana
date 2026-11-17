# shellcheck shell=sh
# deploy.sh runs on the server, as the environment's own user (topbanana-<env>,
# rootless Podman, lingering on), started by deploy-ssh.sh. That has copied this
# folder's units, this script and the env file write-env.sh made into
# ~/$DEPLOY_DIR, a folder of the run's own. It reads, none of them a secret:
#   DEPLOY_ENV   production, staging, development, development-preview or demo
#   DEPLOY_DIR   that folder, under the home: topbanana-deploy/<id>
#   IMAGE        the image pinned to its digest: ghcr.io/...@sha256:...
#   PR_NUMBER    the pull request, for development-preview
#
# It checks the units with the server's own Quadlet, pulls the image, installs
# the env file (~/.config/topbanana/topbanana.env) and the unit
# (~/.config/containers/systemd/topbanana.container, and topbanana-seed.container
# for the demo), restarts topbanana.service and waits until the app answers on
# its port. Anything failing before the restart leaves the running app as it
# was. The run's folder is removed in any case; no value is ever printed.
# POSIX sh: the login shell is the server's.
set -eu
set -f

HEALTH_TRIES="${HEALTH_TRIES:-36}"
HEALTH_SLEEP="${HEALTH_SLEEP:-5}"
UNITS="$HOME/.config/containers/systemd"
CONF="$HOME/.config/topbanana"
DATA="$HOME/data"
QUADLET="${QUADLET:-/usr/libexec/podman/quadlet}"

die() {
  echo "error: $*" >&2
  exit 1
}

# Only a run's own folder, so the trap below never removes anything else.
case "${DEPLOY_DIR:-}" in
  topbanana-deploy/*) ;;
  *) die "DEPLOY_DIR is not topbanana-deploy/<id>: '${DEPLOY_DIR:-}'" ;;
esac
case "${DEPLOY_DIR#topbanana-deploy/}" in
  "" | *[!A-Za-z0-9-]*) die "DEPLOY_DIR is not topbanana-deploy/<id>: '$DEPLOY_DIR'" ;;
esac
SRC="$HOME/$DEPLOY_DIR"
# The upload holds secrets: it goes whatever happens.
trap 'rm -rf "$SRC"' EXIT

case "${DEPLOY_ENV:-}" in
  production | staging | development | demo) ;;
  development-preview)
    case "${PR_NUMBER:-}" in
      "" | *[!0-9]*) die "PR_NUMBER is not a number: '${PR_NUMBER:-}'" ;;
    esac
    ;;
  *) die "DEPLOY_ENV is not production, staging, development, development-preview or demo: '${DEPLOY_ENV:-}'" ;;
esac
case "${IMAGE:-}" in
  ghcr.io/*@sha256:*) ;;
  *) die "IMAGE is not ghcr.io/...@sha256:...: '${IMAGE:-}'" ;;
esac
digest="${IMAGE#*@sha256:}"
[ "${#digest}" -eq 64 ] || die "IMAGE has no sha256 digest: '$IMAGE'"
case "$digest${IMAGE%%@*}" in
  *[!a-z0-9./_-]*) die "IMAGE holds an unexpected character: '$IMAGE'" ;;
esac

unit="$DEPLOY_ENV.container"
for file in "$unit" topbanana.env; do
  [ -f "$SRC/$file" ] || die "$SRC/$file is missing"
done
if [ "$DEPLOY_ENV" = demo ]; then
  [ -f "$SRC/demo-seed.container" ] || die "$SRC/demo-seed.container is missing"
  [ -d "$SRC/seed" ] || die "$SRC/seed (the demo quiz archives) is missing"
fi

: "${XDG_RUNTIME_DIR:=/run/user/$(id -u)}"
export XDG_RUNTIME_DIR
cd "$HOME"
# A deploy waits for the one running (on development: main's and a preview's).
exec 9> .deploy.lock
flock -w 900 9
# A run whose deploy never started leaves its upload; none is a day old.
find "$HOME/topbanana-deploy" -mindepth 1 -maxdepth 1 -type d -mmin +1440 -exec rm -rf {} + 2>/dev/null || true

umask 077
tmp="$(mktemp -d "$HOME/.deploy.XXXXXX")"
trap 'rm -rf "$tmp" "$SRC"' EXIT
mkdir "$tmp/units"

# render FILE NAME: the unit FILE as NAME, with the image filled in.
render() {
  grep -qx 'Image=@IMAGE@' "$SRC/$1" || die "no Image=@IMAGE@ in $1"
  sed "s|^Image=@IMAGE@\$|Image=$IMAGE|" "$SRC/$1" > "$tmp/units/$2"
  if grep -q '@[A-Z_]*@' "$tmp/units/$2"; then
    die "$1 has a value deploy.sh does not fill in"
  fi
}
render "$unit" topbanana.container
if [ "$DEPLOY_ENV" = demo ]; then
  render demo-seed.container topbanana-seed.container
fi
port="$(sed -n 's/^PublishPort=127\.0\.0\.1:\([0-9]*\):8080$/\1/p' "$tmp/units/topbanana.container")"
[ -n "$port" ] || die "no PublishPort=127.0.0.1:<port>:8080 in $unit"
# The server's Podman must accept the units before anything else happens.
if ! QUADLET_UNIT_DIRS="$tmp/units" "$QUADLET" -dryrun -user > "$tmp/quadlet.out" 2>&1; then
  cat "$tmp/quadlet.out" >&2
  die "this server's Quadlet rejects the units; nothing was changed"
fi

# Public on GHCR: pulled without a login. Already here (a redeploy): no pull.
if ! podman image exists "$IMAGE"; then
  podman pull --quiet "$IMAGE" > /dev/null
fi

# The data. A preview gets its own folder, emptied when the slot switches to
# another pull request; main's folder is never touched by a preview.
case "$DEPLOY_ENV" in
  development-preview)
    if [ "$(cat .pr 2> /dev/null || true)" != "$PR_NUMBER" ]; then
      echo "The development slot switches to pull request #$PR_NUMBER: its preview starts from an empty database."
      systemctl --user stop topbanana.service 2> /dev/null || true
      rm -rf "$DATA/preview"
    fi
    mkdir -p "$DATA/preview"
    ;;
  development)
    # Takes the slot back; that pull request's next preview starts empty.
    rm -f .pr
    mkdir -p "$DATA/topbanana"
    ;;
  demo)
    mkdir -p "$DATA/topbanana"
    rm -rf "$DATA/seed"
    mkdir "$DATA/seed"
    set +f
    cp "$SRC"/seed/*.zip "$DATA/seed/"
    set -f
    ;;
  *)
    mkdir -p "$DATA/topbanana"
    ;;
esac

mkdir -p "$UNITS" "$CONF"
chmod 700 "$CONF"
install -m 600 "$SRC/topbanana.env" "$CONF/topbanana.env"
set +f
chmod 644 "$tmp/units/"*.container
mv -f "$tmp/units/"*.container "$UNITS/"
set -f
if [ "$DEPLOY_ENV" != demo ]; then
  rm -f "$UNITS/topbanana-seed.container"
fi
if [ "$DEPLOY_ENV" = development-preview ]; then
  echo "$PR_NUMBER" > .pr
fi
systemctl --user daemon-reload

# A demo without a database gets the demo baseline before it starts.
if [ "$DEPLOY_ENV" = demo ] && [ ! -f "$DATA/topbanana/topbanana.sqlite" ]; then
  echo "No demo database yet: loading the demo baseline."
  systemctl --user stop topbanana.service 2> /dev/null || true
  systemctl --user start topbanana-seed.service ||
    die "loading the demo baseline failed: journalctl --user -u topbanana-seed on the server"
fi

systemctl --user restart topbanana.service

i=0
until curl -fsS --max-time 5 "http://127.0.0.1:$port/healthz" > /dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -ge "$HEALTH_TRIES" ]; then
    systemctl --user status topbanana.service --no-pager --lines=0 >&2 || true
    die "the app is not healthy on 127.0.0.1:$port after $((HEALTH_TRIES * HEALTH_SLEEP))s: journalctl --user -u topbanana on the server"
  fi
  sleep "$HEALTH_SLEEP"
done
echo "Top Banana! $DEPLOY_ENV is healthy on 127.0.0.1:$port ($IMAGE)."
