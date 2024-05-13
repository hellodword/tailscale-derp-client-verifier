# tailscale-client-verifier

```sh
# On trusted machine
tailscale status --json | jq '[recurse | objects | with_entries(select(.key == "PublicKey")) | .[]] | sort' > temp.verifier.json
jq -e . temp.verifier.json
if [ "$(cat verifier.json || true)" != "$(cat temp.verifier.json)" ]; then
  mv temp.verifier.json verifier.json
  rclone --config rclone.conf --contimeout=3m --timeout=10m --checksum copyto ./verifier.json $S3_REMOTE_NAME:$S3_BUCKET/verifier.json
fi

# prepare and update /path/to/verifier.json with cronjob or systemd unit
rclone --config rclone.conf --contimeout=3m --timeout=10m --checksum copyto $S3_REMOTE_NAME:$S3_BUCKET/verifier.json /path/to/verifier.json

# run tailscale-client-verifier locally
./tailscale-client-verifier -path /path/to/verifier.json

derper <... other args> --verify-clients=false --verify-client-url-fail-open=false --verify-client-url=http://127.0.0.1:3000
```
