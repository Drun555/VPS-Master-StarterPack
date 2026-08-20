# VPS Reality Master

Single-binary control plane for provisioning and managing
[VPS Reality Slave](https://github.com/Drun555/VPS-Slave-StarterPack) servers.

Master provides a Russian web interface, provisions clean Ubuntu 24.04 VPS
instances over SSH, synchronizes VLESS users through the Slave HTTPS API, and
serves an unguessable plain-text subscription URL for every user.

## Configuration

Copy `.env.example` to `.env` next to the binary and edit it:

```dotenv
LISTEN_ADDRESS=0.0.0.0
PORT=42345
BASE_URL=https://vpn.example.org
CERTBOT_EMAIL=admin@example.org
```

`BASE_URL` is the public HTTPS URL of your reverse proxy. Master itself serves
plain HTTP and intentionally has no admin authentication, so restrict its
network exposure or protect it at the reverse proxy.

On first start Master creates `master.json` beside the executable. The file
contains SSH keys, DuckDNS tokens, and Slave API passwords and is written with
mode `0600`. Back it up securely and never commit it.

## Build and run

```sh
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o vps-reality-master .
./vps-reality-master
```

Open `http://router-or-host:42345/`. The binary accepts no command-line flags;
all settings come from the adjacent `.env`.

`scripts/build.sh` creates static Linux binaries for amd64, arm64, armv7,
mips/softfloat, and mipsle/softfloat.

## init.d / Keenetic Entware

Place these files together:

- `vps-reality-master`
- `.env`
- `register-initd.sh`

Then run `sudo ./register-initd.sh`. The script detects Entware or SysV init.d
and writes a startup script for the adjacent bare binary. It does not start or
restart Master immediately.

## Synchronization behavior

- Adding a user records it in Master first and then registers it on every
  server. Partial failures are visible and retried manually.
- Deleting a user invalidates the subscription immediately and performs
  best-effort removal from every Slave.
- Full synchronization is authoritative and requires confirmation in the UI:
  it creates missing clients, adopts matching email records, removes
  duplicates, and removes all Slave clients absent from Master.
- No periodic or startup synchronization runs automatically.

Subscription responses are plain text with one `vless://` URI per line:

```text
https://vpn.example.org/subscribe/<random-token>
```
