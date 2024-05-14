# tailscale-client-verifier

```sh
# On trusted machine
tailscale status --json | jq '[recurse | objects | with_entries(select(.key == "PublicKey")) | .[]] | sort' > nodes.json

# sync nodes.json from trusted machine to DERP server

# run tailscale-client-verifier locally
./tailscale-client-verifier -path /path/to/nodes.json

derper <... other args> --verify-clients=false --verify-client-url-fail-open=false --verify-client-url=http://127.0.0.1:3000
```
