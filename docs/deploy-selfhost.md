# Deploying multica-notify against a self-hosted Multica

End-to-end walkthrough: from a bare bridge binary to "issue moves to
in_review, my WeCom group pings". Commands assume Debian/Ubuntu on the same
host as Multica (a Raspberry Pi 5 is plenty); adjust paths to your layout.

Multica-side facts referenced here were verified against multica source
@5063fc907 (see the project plan for file:line citations).

## 0. Prerequisites

- Multica backend reachable, workspace admin access (JWT via
  `POST /auth/send-code` + `POST /auth/verify-code`, or an existing session)
- Go 1.22+ on the build host (the Pi is fine: `apt install golang-go` gives
  1.22 on Ubuntu 24.04)
- A notification receiver this doc uses: ntfy (native package or
  `deploy/compose/deploy.yml`), and optionally an apprise-api instance

## 1. Get and install the bridge

Option A — download a release artifact (no build toolchain needed; see the
README's artifact table, e.g. `linux-arm64` for a Raspberry Pi 5):

```bash
VER=v0.1.0
BASE="https://github.com/bg9ezn/multica-notify/releases/download/$VER"
curl -fsSLO "$BASE/multica-notify-$VER-linux-arm64"
curl -fsSLO "$BASE/sha256sums.txt"
grep linux-arm64 sha256sums.txt | sha256sum -c -
sudo install -m 0755 "multica-notify-$VER-linux-arm64" /usr/local/bin/multica-notify
```

Option B — build from source:

```bash
make build
sudo install -m 0755 bin/multica-notify /usr/local/bin/
```

Either way, finish the install layout:

```bash
sudo useradd --system --home /var/lib/multica-notify --shell /usr/sbin/nologin multica-notify
sudo mkdir -p /etc/multica-notify /var/lib/multica-notify
sudo chown multica-notify:multica-notify /var/lib/multica-notify
```

## 2. TLS: local CA + server certificate

Multica's manifest validator only accepts `https://` transport URLs, and for
private origins the backend dials with whatever CA the operator supplies.

```bash
sudo deploy/tls/gen-certs.sh <bridge-host-or-ip> /etc/multica-notify/tls
# -> /etc/multica-notify/tls/{ca.crt,tls.crt,tls.key}
sudo chown -R multica-notify:multica-notify /etc/multica-notify/tls
```

## 3. Bridge configuration and service

```bash
sudo install -m 0644 deploy/examples/config.example.yaml /etc/multica-notify/config.yaml
sudoedit /etc/multica-notify/config.yaml   # set listen/tls/channels
sudo install -m 0644 deploy/systemd/multica-notify.service /etc/systemd/system/
sudo systemctl daemon-reload
```

The signing secret comes later (step 6); pre-create the env file so the unit
can load it:

```bash
echo 'MULTICA_NOTIFY_SIGNING_SECRET=placeholder' | sudo tee /etc/multica-notify/env
sudo chmod 600 /etc/multica-notify/env
```

Start it (HTTP errors about the placeholder secret are expected until step 6
completes):

```bash
sudo systemctl enable --now multica-notify
curl -k https://<bridge-host>:9097/healthz   # -> ok
```

## 4. Multica backend: enable plugins and trust the bridge origin

Append to the backend env file (e.g. `/opt/multica/backend.env`), then
restart the backend:

```dotenv
# Feature flag via the env provider's static file
MULTICA_FEATURE_FLAGS_FILE=/opt/multica/feature-flags.yaml
# SSRF-guard opt-in for the bridge origin + trust of its local CA
MULTICA_PLUGIN_DEV_ORIGINS=https://<bridge-host>:9097
MULTICA_PLUGIN_DEV_CA=/opt/multica/multica-notify-ca.crt
```

```bash
sudo tee /opt/multica/feature-flags.yaml >/dev/null <<'EOF'
plugins_v1:
  default: true
EOF
sudo cp /etc/multica-notify/tls/ca.crt /opt/multica/multica-notify-ca.crt
sudo systemctl restart multica-backend   # adjust unit name to your deploy
```

## 5. Publish the plugin manifest

Build the bundle and publish it via the admin API (JWT `<token>` from the
auth flow; `BRIDGE_HOST_PLACEHOLDER` in
`manifest/multica.plugin.json` must already be replaced with the bridge host):

```bash
make manifest-pack   # dist/multica.plugin.json.zip
curl -sS -X POST "https://<multica-host>/api/plugins/packages" \
  -H "Authorization: Bearer <token>" \
  -F "package=@dist/multica.plugin.json.zip"
```

If your deployment exposes different route prefixes, list the local routes
with the workspace admin UI (Settings → Plugins) — the API mirrors it.

## 6. Install the plugin and obtain the signing secret

1. Workspace Settings → Plugins → the published **Multica Notify** →
   preview (scopes shown: `net:<bridge-host>`) → install.
2. Rotate the plugin token once — the UI shows the signing secret exactly
   once, next to the install token.
3. Write it into the bridge env and restart the bridge:

```bash
echo 'MULTICA_NOTIFY_SIGNING_SECRET=whsec_...' | sudo tee /etc/multica-notify/env
sudo systemctl restart multica-notify
```

## 7. Prove the path

Without touching real Multica state:

```bash
go run ./cmd/mocksender -url https://<bridge-host>:9097/hooks/issue-status \
  -secret whsec_... -number 1 -title "path check" -status in_review
# -> your configured channels fire; Multica logs nothing
```

With real state: move any issue to **In Review** (or let an agent deliver —
`delivering` moves it to in_review) and watch the channels.

## 8. Verify the full loop from Multica's side

- The hook invocation log lives in Multica (Settings → Plugins →
  invocations; 7-day retention) — check it if channels stay silent.
- Backend log lines to grep for: `plugins: event dispatch queue full` (lost
  events under backpressure) and `hook circuit open` (your endpoint was
  failing and is being skipped temporarily).

## Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| Bridge logs `signature does not match` right after token rotation | Old secret still in `/etc/multica-notify/env` — update and restart the bridge |
| Backend logs refuse to dial the hook | `MULTICA_PLUGIN_DEV_ORIGINS` origin mismatch (must match scheme+host+port exactly) or CA not the one that signed `tls.crt` |
| Manifest upload rejected: transport not HTTPS | You left an `http://` URL in the manifest, or kept `BRIDGE_HOST_PLACEHOLDER` |
| Channels silent, no bridge log | Check the filter: `issue_statuses` may not include the status you moved to |
| Duplicate notifications after bridge restarts | Journal path not writable by the service user (`ReadWritePaths=/var/lib/multica-notify`) |
