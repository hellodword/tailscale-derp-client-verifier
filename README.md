# tailscale-derp-client-verifier

`tailscale-derp-client-verifier` lets a custom Tailscale DERP server verify
tailnet clients without enrolling the DERP host itself as a Tailscale node.

## Why

1. Tailscale traffic is end-to-end encrypted by design.[^1]
2. An untrusted DERP server cannot decrypt relayed traffic.[^2]
3. derper's `--verify-clients` mode requires a local Tailscale node on the DERP
   host.
4. `--verify-client-url` delegates client admission to an external HTTP
   service.[^3]

This verifier implements that HTTP service and admits node public keys found in
a local `nodes.json` file.

## Build

```sh
# With the Go version declared in go.mod.
go build -x -v -trimpath -ldflags "-s -w" -buildvcs=false -o tailscale-derp-client-verifier .
```

## Update `nodes.json`

Generate the allowlist on a trusted machine that is already connected to the
tailnet and can see every node that should use the DERP server. Tailscale
documents `tailscale status --json` as machine-readable output suitable for
automation.[^4]

```sh
tailscale status --json |
  jq -c '[.Self.PublicKey, (.Peer[]?.PublicKey)]
    | map(select(type == "string" and startswith("nodekey:")))
    | sort
    | unique' > nodes.json.new

jq -e '
  type == "array" and
  length > 0 and
  all(.[]; type == "string" and startswith("nodekey:"))
' nodes.json.new > /dev/null
```

Review the generated keys, then deliver `nodes.json.new` to the DERP host using
an authenticated mechanism already standard in your environment. Place the
temporary file in the same directory as the live file and rename it there so
readers see an atomic replacement:

```sh
mv -- /path/to/nodes.json.new /path/to/nodes.json
```

Run this update periodically and after adding, removing, or reauthenticating
nodes. Node public keys can change when a device reauthenticates.[^5]

The verifier reloads the file in the background every 30 seconds by default,
even when it is not receiving requests. `-path` itself remains fixed for the
lifetime of the process; replace the file at that path atomically, or restart
the process to use a different path. A missing, malformed, oversized, or empty
runtime update leaves the last valid in-memory list active and is retried on the
next interval. Node files larger than 16 MiB are considered oversized. Startup
fails for the same conditions. An empty list is accepted only when the verifier
is started with `-allow-empty`.

## Run

The nodes file path is required:

```sh
/path/to/tailscale-derp-client-verifier -path /path/to/nodes.json
```

Use `-reload-interval` to change the polling period; values shorter than one
second are rejected:

```sh
/path/to/tailscale-derp-client-verifier \
  -path /path/to/nodes.json \
  -reload-interval 10s
```

The verifier listens on `localhost:3000` by default; change this with `-addr`.
Configure derper to use it:

```sh
derper <... other args> --verify-clients=false --verify-client-url-fail-open=false --verify-client-url=http://127.0.0.1:3000
```

[^1]: https://tailscale.com/security
[^2]: https://github.com/tailscale/tailscale/issues/12107#issuecomment-2106233579
[^3]: https://github.com/tailscale/tailscale/pull/11193
[^4]: https://tailscale.com/docs/reference/tailscale-cli#status
[^5]: https://tailscale.com/docs/concepts/node-keys
