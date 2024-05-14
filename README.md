# tailscale-derp-client-verifier

## Why

Because I want to run a Tailscale DERP server without having to trust it.

1. `Tailscale` is `end-to-end encrypted` by design[^1]

2. An insecure DERP server or connection is still secure for DERP users; there are no MITM issues[^2]

3. `--verify-clients` requires adding this DERP server as one of your Tailscale nodes. In my opinion, this is risky, even with ACLs.

4. `--verify-client-url` allows derper to check an external URL to permit access[^3]

## Build

```sh
# with Go 1.22.3+
go build -x -v -trimpath -ldflags "-s -w" -buildvcs=false -o tailscale-derp-client-verifier .
```

## Usage

1. **On the trusted Tailscale node machine**: get the nodes list from your trusted homelab server or laptop, and sync the `nodes.json` to the DERP server machine:

You can achieve this in many ways. I do this with rclone, systemd timer, and S3 object storage::

```sh
#! /usr/bin/env bash
set -e
set -x

[ -f "$RCLONE_CONFIG_FILE" ]
[ -d "$WORK_DIR" ]
[ -n "$TAILSCALE_S3_REMOTE_NAME" ]
[ -n "$TAILSCALE_S3_BUCKET" ]

cd $WORK_DIR

tailscale status --json | jq '[recurse | objects | with_entries(select(.key == "PublicKey")) | .[]] | sort' > "temp.nodes.json"

jq -e . "temp.nodes.json"

if [ "$(cat "nodes.json" || true)" != "$(cat "temp.nodes.json")" ]; then
  rclone --config "$RCLONE_CONFIG_FILE" --contimeout=3m --timeout=10m --checksum copyto \
    "temp.nodes.json" "$TAILSCALE_S3_REMOTE_NAME:$TAILSCALE_S3_BUCKET/nodes.json"
  mv "temp.nodes.json" "nodes.json"
fi
```

2. **On the DERP server machine**: run `tailscale-derp-client-verifier`

If you're not syncing the `nodes.json` with S3 object storage, use the `-path` argument:

```sh
/path/to/tailscale-derp-client-verifier -path /path/to/nodes.json
```

I use S3, so I:

```sh
. .env

/path/to/tailscale-derp-client-verifier
```

```ini
# .env file
S3_ACCESS_KEY_ID=
S3_SECRET_ACCESS_KEY=
S3_ENDPOINT=
S3_REGION=
S3_BUCKET=
S3_FILE=nodes.json
S3_FORCE_PATH_STYLE=
```

3. **On the DERP server machine**: deploy the derper

```sh
derper <... other args> --verify-clients=false --verify-client-url-fail-open=false --verify-client-url=http://127.0.0.1:3000
```

[^1]: https://tailscale.com/security
[^2]: https://github.com/tailscale/tailscale/issues/12107#issuecomment-2106233579
[^3]: https://github.com/tailscale/tailscale/pull/11193
