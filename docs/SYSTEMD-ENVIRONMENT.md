# InstaEditLogin systemd environment

`instaeditlogin.service` runs the VPS development BFF as the `pierone` user. OAuth
credentials and encryption material must not be embedded in the unit file or
checked into this repository. The persistent host-only file is:

```text
/etc/instaeditlogin-youtube.env
```

The non-secret template is
[`ops/systemd/instaeditlogin-youtube.env.example`](../ops/systemd/instaeditlogin-youtube.env.example).
The populated file is intentionally not part of Git.

> **Current host note:** the live unit loads these files in this order:
> `.env.dev`, `/etc/instaeditlogin-overrides.env`, and finally
> `/etc/instaeditlogin-youtube.env`. The dedicated file is therefore the
> last-file-wins source for the variables it owns. The first two files remain
> separate runtime/legacy secret sources and must be protected independently;
> they are not repository artifacts.
>
> The host migration removes YouTube/provider credentials and other sensitive
> `Environment=` assignments from the unit itself. The populated
> `/etc/instaeditlogin-youtube.env` remains host-only; its values are copied
> from the existing protected source without ever being printed. During the
> gradual migration, `ENCRYPTION_KEY` may still be duplicated in `.env.dev` so
> existing deployments continue to boot; the dedicated file takes precedence.
>
> The current host has empty `YOUTUBE_CLIENT_ID` and
> `YOUTUBE_CLIENT_SECRET` values, so YouTube remains disabled until real OAuth
> credentials are provisioned from the password manager. An `active` systemd
> state alone does not prove YouTube availability.

## Required permissions

The file must be owned by `root:root` and have mode `0600`:

```bash
sudo chown root:root /etc/instaeditlogin-youtube.env
sudo chmod 600 /etc/instaeditlogin-youtube.env
```

Do not put JSON credentials, client secrets, refresh tokens, JWT secrets, or
encryption keys in `/etc/systemd/system/instaeditlogin.service`.

## File contents

Use direct `KEY=value` assignments. Keep JSON compact and on one line. The
application currently supports either encryption form, but they must not be
mixed:

```dotenv
YOUTUBE_CLIENT_ID=<Google OAuth client id>
YOUTUBE_CLIENT_SECRET=<Google OAuth client secret>
YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback

# Legacy single-key form, retained for existing ciphertext during migration:
ENCRYPTION_KEY=<base64-encoded 32-byte key>

# Or, instead of ENCRYPTION_KEY, use the key ring form:
# ENCRYPTION_KEYS=1:<base64-key>[,2:<base64-key>]
# ACTIVE_ENCRYPTION_KEY_ID=1
```

Add other secret variables required by this specific unit to the same protected
file rather than to `Environment=` lines. Never print the file with `cat`, put
its contents in shell history, or include it in a support ticket.

## Install/update procedure

Run on the VPS. Obtain values from the password manager or existing protected
secret source without echoing them:

```bash
sudo install -o root -g root -m 600 /path/to/populated/instaeditlogin-youtube.env \
  /etc/instaeditlogin-youtube.env
sudo systemctl daemon-reload
sudo systemctl restart instaeditlogin.service
sudo systemctl is-active instaeditlogin.service
```

For this host, distinguish the systemd process from the Docker API process:

```bash
systemctl show instaeditlogin.service -p MainPID -p ActiveState -p SubState
sudo ss -ltnp | grep ':8080'
curl -fsS http://127.0.0.1:8080/ready
```

A successful `systemctl is-active` is insufficient if another process already
owns `:8080`; the systemd journal must also be free of a **current**
`listen tcp :8080: bind: address already in use` failure. Compare the timestamp
of any bind message with:

```bash
systemctl show instaeditlogin.service -p ActiveEnterTimestamp
journalctl -u instaeditlogin.service -b --no-pager | grep -Ei 'bind:|address already in use'
```

A message from an earlier failed/restarted process is historical only; verify
that the current `MainPID` is running and that the expected listener/health
probe responds. Do not change the port or stop the Docker API as part of secret
persistence without a separate deployment decision.

If values are already present in the existing unit, migrate them using a
root-only procedure and then remove the corresponding `Environment=` entries
from the unit. The same applies to `ADMIN_INVITE_TOKEN`,
`METRICS_BASIC_AUTH_PASS`, provider client IDs/secrets, and encryption keys:
only variable names belong in documentation; values belong in the protected
host file. Do not manually copy secrets into a terminal transcript.

After migration, verify names rather than values:

```bash
systemctl cat instaeditlogin.service \
  | grep -E '^Environment=' \
  | grep -E 'YOUTUBE_|ENCRYPTION_|ADMIN_INVITE_TOKEN|CLIENT_SECRET|METRICS_BASIC_AUTH_PASS' \
  && echo 'unexpected direct secret assignment' || true
```

## Verification without secret disclosure

```bash
systemctl show instaeditlogin.service \
  -p ActiveState -p SubState -p Result -p ExecMainStatus \
  -p EnvironmentFiles -p DropInPaths

stat -c '%n mode=%a owner=%U group=%G' \
  /etc/instaeditlogin-youtube.env

curl -fsS https://api.instaedit.org/api/v1/health
curl -fsS https://api.instaedit.org/ready
journalctl -u instaeditlogin.service -b --no-pager \
  | grep -Ei 'secret|token|password|key|client_secret|refresh_token' \
  | sed -E 's/(=|:)[^ ]+/\1[REDACTED]/g'
```

The log check is only a heuristic. Review startup logs for successful vault /
encryption initialization and YouTube registration, but do not expect the
health endpoint to prove that an OAuth refresh token is usable. A real upload
requires an authorized sandbox channel, non-empty YouTube OAuth credentials,
a valid stored OAuth grant, and an explicit operator-approved E2E.
With empty `YOUTUBE_CLIENT_ID`/`YOUTUBE_CLIENT_SECRET`, the expected health
response is `platforms: []` and an upload cannot be used to validate this
configuration. Treat historical bind errors before the latest
`ActiveEnterTimestamp` separately from failures emitted by the current process;
do not declare the service healthy solely because systemd reports `active`.

## Rollback

Before changing the unit, save a root-only backup outside the repository:

```bash
sudo install -o root -g root -m 600 \
  /etc/systemd/system/instaeditlogin.service \
  /etc/systemd/system/instaeditlogin.service.bak.$(date -u +%Y%m%dT%H%M%SZ)
```

To roll back the host-side unit, restore the intended backup, run
`systemctl daemon-reload`, and restart the service. Do not use Git to restore
host files: the live unit and its secrets are not repository artifacts.

## Git policy

Commit only this documentation and the non-secret `.example` template. The
populated `/etc/instaeditlogin-youtube.env` stays on the host and must never be
committed. When the working tree contains unrelated changes, stage only the
explicit documentation/template paths for the atomic commit.
