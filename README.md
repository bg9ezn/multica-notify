# multica-notify

Event notification bridge for self-hosted [Multica](https://github.com/multica-ai/multica) —
receive signed plugin-hook events and fan out to Apprise, ntfy, or any webhook.

[![CI](https://github.com/bg9ezn/multica-notify/actions/workflows/ci.yml/badge.svg)](./actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](./LICENSE)
[![Go](https://img.shields.io/badge/go-1.22%2B-00ADD8)](./go.mod)

> **Unofficial community project** — not affiliated with Multica AI / Index Labs.
> Built entirely on Multica's published plugin contract (`plugincontract`).

**中文文档**：[README.zh-CN.md](./README.zh-CN.md)

## Why

Multica's inbox covers in-app notifications, and its chat integrations are
conversational — neither pushes "issue X is waiting for review" or "task Y
failed" to your phone or group chat. This bridge closes that gap using the
official plugin mechanism:

```
Multica (plugin hook, HMAC-signed POST)
   │  issue.status_changed / task.completed / task.failed / daily heartbeat
   ▼
multica-notify bridge (single Go binary)
   ├─ verify signature  (HMAC-SHA256, ±5 min window, constant-time, replay guard)
   ├─ filter            (statuses you care about; skip retrying tasks)
   ├─ debounce          (rapid flips collapse into one latest-state notification)
   ├─ dedupe            (delivery-id journal, survives restarts)
   └─ fan out           (per-channel, independent failures)
        ├─ apprise  → apprise-api → WeCom / DingTalk / Feishu / Telegram / email / …
        ├─ ntfy     → your phone, via your own server
        └─ webhook  → anything that speaks JSON
```

The bridge deliberately speaks only these three protocols. Channel-specific
knowledge (WeCom bot keys, DingTalk signing, …) stays in Apprise — adding a
channel never changes this codebase.

## Quick start

```bash
# 1. Build (Go 1.22+)
make build

# 2. Generate the annotated config, then edit the channels
./bin/multica-notify -init-config config.yaml

# 3. Run (plain HTTP for now; production must be HTTPS, see below)
MULTICA_NOTIFY_SIGNING_SECRET=whsec_... ./bin/multica-notify -config config.yaml

# 4. Fire a test delivery without touching Multica
go run ./cmd/mocksender -url http://127.0.0.1:9097/hooks/issue-status \
  -secret whsec_... -number 42 -title "Try me" -status in_review
```

`/healthz` answers `ok` for probes.

## Wiring it into Multica (self-hosted)

Three server-side prerequisites, all operator-controlled:

1. **Enable the plugin feature** (off by default): point
   `MULTICA_FEATURE_FLAGS_FILE` at a YAML file containing
   `plugins_v1: {default: true}` and restart the backend.
2. **Allow the private hook origin**: Multica refuses to dial private
   addresses (SSRF guard) and requires HTTPS. Generate a local CA + server
   cert with [`deploy/tls/gen-certs.sh`](./deploy/tls/gen-certs.sh), then set
   on the backend:
   ```dotenv
   MULTICA_PLUGIN_DEV_ORIGINS=https://<bridge-host>:9097
   MULTICA_PLUGIN_DEV_CA=/path/to/ca.crt
   ```
3. **Publish and install the plugin**: upload the bundle
   (`POST /plugins/packages`, admin), then install it in the workspace
   (preview → consent → install). Use
   [`manifest/multica.plugin.json`](./manifest/multica.plugin.json) with
   `BRIDGE_HOST_PLACEHOLDER` replaced by your bridge host. Rotating the
   plugin token shows the signing secret once — put it in
   `/etc/multica-notify/env` as `MULTICA_NOTIFY_SIGNING_SECRET`.

Full walkthrough (including flag file format and API calls) is in
[`docs/deploy-selfhost.md`](./docs/deploy-selfhost.md).

## Configuration

Generate the fully annotated reference any time with
`./bin/multica-notify -init-config <path>` (template:
`internal/config/example.yaml`). Highlights:

| Field | Default | Meaning |
|---|---|---|
| `enabled` | `true` | **Master switch.** `false` = global mute: deliveries are still accepted and journaled, fan-out suppressed (live via SIGHUP) |
| `listen` | `:9097` | hook server address |
| `tls` | — | cert/key; required in production (Multica only accepts https transports) |
| `filters.issue_statuses` | `[in_review, done]` | which issue statuses notify; empty = all |
| `filters.on_task_failed` | `true` | notify on terminal task failures |
| `filters.skip_retrying_tasks` | `true` | drop intermediate failures Multica marks `retry_pending` |
| `debounce.window` | `30s` | collapse rapid flips on one issue into the latest state |
| `idempotency_journal` | `data/journal.jsonl` | delivery-id journal (at-most-once) |
| `channels[]` | — | `apprise` / `ntfy` / `webhook` |

Channel options are documented in the example config; unknown options are
ignored, unknown channel types fail at startup with the known list.

## Security model

The hook contract's four disciplines, all implemented in
[`internal/hookserver/verify.go`](./internal/hookserver/verify.go):

1. verify the HMAC against the **raw bytes**, before parsing;
2. reject timestamps outside the **±5 min** window;
3. compare signatures in **constant time**;
4. remember accepted signatures inside the window — closing the replay gap
   the host cannot close for you.

The bridge holds only the signing secret, dials only the channels you
configure, and stores only delivery ids. Issue titles travel into
notifications by design — choose channels accordingly (self-hosted ntfy keeps
them on your network).

## Development

```bash
make help             # list targets
make clean build test # one-shot: clean, build, lint, unit tests (race on)
make test-integration # real ntfy via docker compose + signed deliveries
make package          # linux/arm64 + amd64 artifacts into dist/ + sha256sums
```

Layout (dependency direction points one way; interfaces only at the
`channel` boundary):

```
cmd/{multica-notify,mocksender}
internal/config      YAML load/validate/reload
internal/hookserver  HTTPS + signature verify + hook routing + send pool
internal/event       decode, filter, debounce, journal
internal/message     event → title/body templates
internal/channel     Channel interface + registry + apprise/ntfy/webhook
internal/httpx       shared JSON POST with bounded retries
```

Unit tests cover the rejection matrix, debounce semantics, journal restarts,
and every adapter against `httptest` receivers; integration tests run the
real pipeline against a real ntfy (including replay rejection and
channel-isolation while apprise is down).

## Deployment

- [`deploy/systemd/multica-notify.service`](./deploy/systemd/multica-notify.service) — hardened unit
- [`deploy/compose/deploy.yml`](./deploy/compose/deploy.yml) — optional apprise-api + ntfy (Docker hosts)
- [`deploy/tls/gen-certs.sh`](./deploy/tls/gen-certs.sh) — local CA + server cert for private origins
- [`docs/deploy-selfhost.md`](./docs/deploy-selfhost.md) — end-to-end walkthrough

## Release artifacts

Every tag produces one statically-linked binary per platform plus
`sha256sums.txt`. Naming: `multica-notify-<version>-<os>-<arch>[.exe]`.

| Artifact | For |
|---|---|
| `...-linux-amd64` | 64-bit x86 Linux (servers, PCs, NAS) |
| `...-linux-arm64` | 64-bit ARM Linux — **Raspberry Pi 3/4/5**, cloud ARM instances |
| `...-linux-armv6` | 32-bit ARM Linux — Raspberry Pi Zero/1, Pi 2/3 on a 32-bit OS (one binary covers ARMv6 through ARMv8 in 32-bit mode) |
| `...-linux-loong64` | Loongson (LoongArch64) Linux |
| `...-linux-riscv64` | RISC-V Linux (VisionFive 2, Lichee Pi 4A, ...) |
| `...-windows-amd64.exe` | 64-bit Windows |
| `...-windows-arm64.exe` | Windows on ARM |
| `...-darwin-amd64` | Intel Mac |
| `...-darwin-arm64` | Apple Silicon Mac |

Verify before running: `sha256sum -c sha256sums.txt`. Binaries are statically
linked (CGO disabled) — download, `chmod +x`, run. Darwin builds are unsigned:
`xattr -d com.apple.quarantine <binary>` on first run.

## Related projects

| Project | Relationship |
|---|---|
| [Multica](https://github.com/multica-ai/multica) | The self-hosted agent platform this bridge extends. The hook contract implemented here is Multica's published plugin contract; see upstream [RFC #1964](https://github.com/multica-ai/multica/issues/1964) (outbound webhooks) and the official `triage-notify` example — this project's signature handling follows that oracle. |
| [Apprise](https://github.com/caronc/apprise) / [apprise-api](https://github.com/caronc/apprise-api) | The 128+ service fan-out layer (WeCom, DingTalk, Feishu, Telegram, email, ...). The bridge speaks the apprise-api HTTP protocol. ([Shoutrrr](https://github.com/containrrr/shoutrrr) is the Go-library equivalent in this ecosystem — library/CLI only, no HTTP API — which is why it is not an integration path here.) |
| [ntfy](https://github.com/binwiederhier/ntfy) | Self-hosted HTTP push to your phone — the recommended personal channel. [Gotify](https://github.com/gotify/server) is a comparable alternative. |

## Versioning

SemVer (`vMAJOR.MINOR.PATCH`), tagged with a `v` prefix; every tag triggers a
release with artifacts for every `PLATFORMS` entry in the Makefile.

- **PATCH** — backwards-compatible bug fixes, tests, docs, build/packaging
  corrections. No new config surface, no behavior additions.
- **MINOR** — new functionality or config surface (channels, switches,
  platforms). The norm while in 0.x.
- **MAJOR** — breaking changes (config schema, hook contract usage). Not
  expected before 1.0.

## License

[Apache-2.0](./LICENSE). Contributions welcome — sign off your commits
(`git commit -s`), CI enforces DCO.
