# AGENTS.md

Guidance for AI coding agents (and new contributors) working in this
repository.

## What this is

multica-notify is a standalone Go service that bridges self-hosted
[Multica](https://github.com/multica-ai/multica) to notification channels.
Multica calls it through the official plugin-hook contract (HMAC-signed HTTP
POSTs on issue/task events); the bridge verifies, filters, debounces,
deduplicates, and fans out to exactly three protocols: apprise-api, ntfy, and
generic webhook. Channel-specific knowledge (WeCom, DingTalk, Feishu, ...)
lives in Apprise — never in this codebase.

## Commands

| Task | Command |
|---|---|
| Everything CI runs | `make verify` (lint + build + race unit tests) |
| Integration tests (real ntfy) | `make test-integration` (needs docker) |
| Build / lint / package | `make build` / `make lint` / `make package` |
| Single test | `go test -race -count=1 -run TestName ./internal/hookserver/` |
| CLI surface | `multica-notify serve \| init-config \| test \| version` (cobra) |

Windows note: a 32-bit gcc breaks `-race` locally; CI enforces race anyway.

## Architecture

Dependency direction is one-way; interfaces exist only at the channel
boundary. See README for the diagram.

- `internal/config` — YAML load/defaults/validate/SIGHUP reload
- `internal/hookserver` — HTTPS endpoint, signature verification, hook_key
  routing, bounded async send pool (8 workers / 256 queue; overflow drops
  loudly instead of growing memory)
- `internal/event` — tolerant envelope decode, filter, latest-state
  debouncer, idempotency journal (at-most-once)
- `internal/message` — built-in template sets + validated config overrides
- `internal/channel` — `Channel` interface + `Registry` (register-a-factory);
  adapters are thin HTTP POSTs with bounded retries via `internal/httpx`
- `internal/version` — ldflags injection with `debug.ReadBuildInfo` fallback

## Contract facts you must not break

Verified against Multica source @5063fc907; the official example
`examples/plugins/triage-notify` (server/handler.mjs) is the behavioral
oracle.

1. **Signature**: `HMAC-SHA256(secret, timestamp + "." + rawBody)`, headers
   `x-multica-timestamp` (unix seconds, part of the signed material) and
   `x-multica-signature` (`v1=` + hex). Verify RAW bytes before parsing;
   ±5 min window; constant-time compare; remember accepted signatures inside
   the window (the replay guard the host cannot do for you).
2. **`delivery_id` is schedule-only on the wire.** Event deliveries arrive
   without it, and `invocation_id` changes per attempt. Idempotency must use
   `event.Envelope.IDKey()` (delivery_id, else type+workspace+installation+
   issue+occurred_at).
3. **Transport must be HTTPS** and covered by an exact-host `net:` scope;
   the host refuses private addresses unless the operator lists the origin
   in `MULTICA_PLUGIN_DEV_ORIGINS` (trust via `MULTICA_PLUGIN_DEV_CA`).
4. **Scope/content coupling**: hooks subscribing to issue/task events must
   declare `issues:read` / `tasks:read` in the manifest.
5. **Muted bridge still answers 200** and journals (top-level
   `enabled: false` suppresses fan-out only) — the host must never see a
   muted bridge as failing.
6. **ntfy 2.29**: root-level `/json?poll=1` serves the web SPA; read back
   via `/<topic>/json?poll=1`. Publishing JSON to the server root works.

## Conventions

- Runtime dependencies: stdlib + `gopkg.in/yaml.v3` + `spf13/cobra` (CLI
  structure). Test dependencies: testify. Anything else needs strong
  justification recorded in the commit.
- Assertions: testify `assert`/`require`; async waits = `assert.Eventually`;
  negative timing = `assert.Never`. No hand-rolled sleeps.
- LF everywhere (`.gitattributes`); the Makefile requires real tabs.
- Commit messages: English, conventional prefix (`feat`/`fix`/`chore`/`docs`/
  `build`/`test`/`ci`), body as Problem / Cause / Solution / Leftovers when
  non-trivial; DCO sign-off (`git commit -s`) enforced on PRs.
- Changelog discipline: every user-observable change (features, fixes, config
  surface, docs that affect users) adds an entry under CHANGELOG
  `[Unreleased]` in the same commit; internal refactors need not. At release
  time `[Unreleased]` becomes the version section.
- Bilingual README: `README.md` (English, primary) and `README.zh-CN.md` are
  kept in sync — user-facing changes (features, config, artifacts, policies)
  land in both in the same commit. AGENTS.md, CHANGELOG and deploy docs are
  English-only.
- Tests live next to their packages; every adapter is tested against
  `httptest`; cross-platform changes are verified with a local
  cross-compile before tagging.
- `internal/config/example.yaml` is embedded (go:embed) and served by
  `init-config`; `TestEmbeddedExampleIsValid` guards drift — update the
  example in the same commit as config field changes.

## Versioning and release

- SemVer, documented in the README: PATCH = fixes only; MINOR = features or
  new config surface/platforms; MAJOR = breaking.
- Release: move CHANGELOG `[Unreleased]` into the version section, tag
  `vX.Y.Z`, push the tag — the release workflow builds every `PLATFORMS`
  entry with checksums and publishes. Artifacts are named
  `multica-notify-<version>-<os>-<arch>[.exe]`; `linux/arm` ships as
  `armv6` (GOARM=6, covers all 32-bit ARM).
- Version string precedence: ldflags injection → build-info module version →
  embedded VCS revision → `dev` (see `internal/version`).

## Adding things

- **Channel**: implement `Name`/`Send` under `internal/channel/<name>`,
  register in `NewRegistry`, unit-test against `httptest` (success, 5xx
  retry, 4xx no-retry). Keep it thin: receiver-specific knowledge belongs in
  Apprise.
  - Ruled out: a `shoutrrr` channel type. Shoutrrr ships only as a Go
    library and a CLI — there is no official HTTP API service (unlike
    apprise-api). The library form breaks the zero-runtime-dependency rule;
    the CLI form adds an external tool plus subprocess orchestration with
    none of apprise-api's service qualities (config keys, stateful configs,
    health endpoint). If a receiver lacks Apprise coverage, add a thin
    native adapter for it instead (the way ntfy was done).
- **Platform**: one entry in Makefile `PLATFORMS`; verify with a local
  cross-compile; note any Gatekeeper/GOARM caveats in the comment.
- **Event behavior**: extend `event.Filter` + a message template set; decode
  only fields you consume (tolerant of upstream payload growth).
