# Deployments

Top Banana!'s hosted environments run on **zoot** (`zoot.linuxeverywhere.link`), a
Fedora server with Caddy in front and rootless Podman behind it. Each environment
is a Linux user of its own running one container as a systemd user service
([Quadlet](https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html)).
[`.github/workflows/deploy.yml`](../.github/workflows/deploy.yml) deploys to them
over SSH; when each one deploys is in the `deploys` skill
([`.claude/skills/deploys/SKILL.md`](../.claude/skills/deploys/SKILL.md)).

The server side lives in the infrastructure repository,
[linuxeverywhere-infra](https://github.com/starquake/linuxeverywhere-infra)
(`hosts/zoot/README.md`, Ansible roles `topbanana` and `app_instance`). It makes
the users, the ports, the Caddy sites with their certificates and the deploy keys.
This repository only brings the container and its settings.

| Environment | User | Port (127.0.0.1) | Name | Unit |
|---|---|---|---|---|
| production | `topbanana-production` | 8100 | topbanana.app (www., play.) | `production.container` |
| staging | `topbanana-staging` | 8101 | topbanana-staging.bananajuice.net | `staging.container` |
| development | `topbanana-development` | 8102 | topbanana-development.bananajuice.net | `development.container`, or `development-preview.container` for a `deploy:dev` pull request |
| demo | `topbanana-demo` | 8103 | demo.topbanana.app | `demo.container` and `demo-seed.container` |

LAN Medium (`topbanana-lanmedium`, 8104, lanmedium.topbanana.app) runs the
`:latest` image too, but Ansible runs it: nothing here deploys it.

Staging and development run unreleased code, so they live on `bananajuice.net`,
the domain for unreleased code, and never on `topbanana.app`, where they could set
cookies for production. The development user's systemd slice is capped (memory,
CPU, tasks) because it runs pull requests' code.

## What's here

[`podman/`](podman/):

- `<environment>.container`: the Quadlet unit of each environment. `Image=@IMAGE@` is
  filled in with the image pinned to its digest; everything else is literal.
- `deploy.sh`: runs on the server as the environment's user (below).
- `deploy-ssh.sh`: runs on the GitHub runner. It sends the units, `deploy.sh` and
  the env file over SSH's standard input, then runs `deploy.sh`.
- `write-env.sh`: runs on the runner. It writes the env file from the job's
  secrets and variables.

## A deploy

1. The job resolves the image tag to its digest and checks its cosign signature
   (not for a pull request preview, which it builds itself).
2. `write-env.sh` writes `topbanana.env` (`NAME=value` lines, no quoting) from the
   GitHub environment's secrets and variables. The SSH settings stay out of it.
3. `deploy-ssh.sh` logs in as `SSH_USER` with `SSH_KEY`. Its key is in that user's
   `authorized_keys` with `restrict`: commands work, but there is no terminal and
   no forwarding. It unpacks the upload into `~/topbanana-deploy/<run>` and
   runs `deploy.sh`. Secrets only travel over standard input. The command line,
   which every user on the server can read, carries only the environment, the
   image and the pull request number.
4. `deploy.sh` takes `~/.deploy.lock`, so a preview and a main deploy never
   overlap. It checks the unit with the server's Quadlet (`-dryrun`), then pulls
   the image. It installs the env file as `~/.config/topbanana/topbanana.env`
   (0600) and the unit as `~/.config/containers/systemd/topbanana.container`.
   Then it runs `systemctl --user daemon-reload`, restarts `topbanana.service`
   and waits for `/healthz` on `127.0.0.1:<port>`. If anything fails before the
   restart, the running app is left as it was. The upload is removed either way.
5. The job checks `HEALTH_URL` through Caddy from outside.

The container runs with `ReadOnly=true`, `DropCapability=ALL` and
`NoNewPrivileges=true`. Its network is `pasta:--host-lo-to-ns-lo`, so Caddy's
requests reach the app from `127.0.0.1`. The units therefore set
`TRUSTED_PROXY_IPS=127.0.0.1/32`, and the per-IP rate limiters read the client's
address from `X-Forwarded-For`. The image runs as `nonroot` (65532), which
`UserNS=keep-id:uid=65532,gid=65532` maps to the environment's user, so the
files in `~/data` stay that user's.

### Data

| Path | What |
|---|---|
| `~/data/topbanana/` | `topbanana.sqlite` (WAL) and `media/` |
| `~/data/preview/` | development only: the current pull request preview's database. It is emptied when the slot switches to another pull request; main's data is never touched |
| `~/data/seed/` | demo only: the quiz archives from `dev/fixtures/demo/`, mounted read-only |
| `~/.config/topbanana/topbanana.env` | the settings and secrets, rewritten on every deploy |
| `~/.pr` | development only: the pull request that holds the slot |

On the server, `~/data` has the SELinux type `container_file_t`, and the units
mount with `:Z`. The server's nightly restic backup includes the homes.

### The demo

On a server with no demo database yet, `deploy.sh` runs `topbanana-seed.service`
before the app starts. That one-shot unit loads the demo baseline (`-seed-demo`).
[`demo-reset.yml`](../.github/workflows/demo-reset.yml) brings the demo back to
the baseline every night: it stops the app, empties `~/data/topbanana`, runs
the seed unit and starts the app again.

## By hand on the server

Log in as an admin and get a shell as the environment's user, with its own systemd:

```bash
sudo machinectl shell topbanana-staging@
```

```bash
systemctl --user status topbanana
```

```bash
journalctl --user -u topbanana -f
```

```bash
podman ps
```

```bash
podman exec -it topbanana /home/nonroot/server -healthcheck
```

The image's maintenance commands (`-create-admin`, `-reset-password` and so on)
run in the running container. With `-i` they read their input from the terminal:

```bash
podman exec -it topbanana /home/nonroot/server -create-admin
```

To run an older build: in GitHub, **Actions → Deploy → Run workflow**, with the
environment and the version.

## Setting up an environment

1. In linuxeverywhere-infra, add the environment to `topbanana_instances`
   (`ansible/roles/topbanana/defaults/main.yml`) with its port, its names and the
   public half of its deploy key. Then run `ansible-playbook zoot.yml`.
2. In GitHub (**Settings → Environments**), give the environment these secrets:
   - `SSH_HOST` (`zoot.linuxeverywhere.link`)
   - `SSH_USER` (`topbanana-<env>`)
   - `SSH_KEY` (the private half of the deploy key, without a passphrase)
   - `HEALTH_URL` (`https://<name>/healthz`)
   - `SESSION_KEY` (`openssl rand -hex 32`)
   - the app's own settings, as each job in `deploy.yml` lists them

   It also needs the variables `BASE_URL` and `SERVER_URL`, plus `REGISTRATION_ENABLED`
   and `ADMIN_EMAILS` where the job reads them.
3. Run the deploy (**Actions → Deploy → Run workflow**).
