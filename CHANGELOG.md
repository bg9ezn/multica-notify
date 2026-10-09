# Changelog

All notable changes to this project are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/) and the
project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Friendly CLI help: bare `multica-notify` prints the help with exit 0,
  every command carries copy-pasteable examples (`--help`), `serve`
  documents logging semantics and exit codes, and `init-config` gained
  `-f/--force` as a shorthand.
- README (EN+ZH): command-line reference section (commands, flags, exit
  codes, environment variables) and an explicit AI-agent pointer to
  AGENTS.md — the README is now self-sufficient for both humans and agents.

## [0.6.1] - 2026-10-10

### Added

- Release-time changelog audit: before tagging, every user-observable commit
  since the previous tag must be represented under `[Unreleased]` — same-
  commit entries are the ideal, this boundary audit is the safety net
  (documented in AGENTS.md).

## [0.6.0] - 2026-10-10

### Added

- `test` accepts `--channel <name>` (repeatable, config-order report,
  unknown names rejected with the configured list), `--title` / `--message`
  (custom text) and `--type info|success|warning|error` — the type rides the
  message to receivers (apprise notify type; ntfy priority/tags escalate for
  warning/error) and is included in the webhook payload.
- Event simulation on `test`: `--event
  issue.status_changed|task.completed|task.failed` (with `--status`,
  `--retry-pending`) runs a synthetic Multica-shaped delivery through the
  configured filter pipeline and reports the decision — filtered events
  exit 0 with the reason; `--ignore-filters` delivers anyway.
- `init-plugin --bridge-host <host>` command: generates the Multica plugin
  manifest (transport URLs and `net:` scope filled in) from the embedded
  template; `make manifest-pack BRIDGE_HOST=...` zips it for upload. The
  standalone `manifest/` directory was removed — the template is embedded.

## [0.5.0] - 2026-10-10

### Added

- `test` command: sends one test message to every enabled channel from the
  current configuration and reports per-channel results (exit 1 on any
  failure). Exercises the channel egress only — no hook server, journal or
  signing secret involved; mocksender remains the tool for the full signed
  path.

### Fixed

- CI integration tests install ntfy from its GitHub release binary instead
  of pulling `binwiederhier/ntfy:latest` from Docker Hub — shared runner IPs
  trip the unauthenticated pull rate limit; the compose test dependency is
  pinned to v2.29.0 (the release verified against the suite).

## [0.4.0] - 2026-10-10

### Added

- Cobra-based command structure: `multica-notify serve` (run the bridge) and
  `init-config <path>` (generate the annotated configuration, `--force` to
  overwrite). Help output and shell completion come with it. **Breaking:**
  the service entry point moved from `multica-notify -config ...` to
  `multica-notify serve -c ...` — update the systemd `ExecStart` (the
  shipped unit already does).
- Log level control and optional file logging on `serve`: `-q/--quiet`
  (errors only), default info, `-v/--verbose` (debug), and `--log-file`
  (mirror logs into a file; default off — stderr only). Built on log/slog.
- README: related-projects section (Multica contract references,
  Apprise/apprise-api, ntfy) and AGENTS.md additions: changelog discipline,
  bilingual-README rule, shoutrrr deferral decision, CLI surface.

### Changed

- Dependency policy amended: runtime dependencies are now stdlib +
  `gopkg.in/yaml.v3` + `spf13/cobra`.

## [0.1.0] - 2026-10-10

Initial stable release.

### Added

- Hook server verifying Multica plugin-hook deliveries (HMAC-SHA256, ±5 min
  timestamp window, constant-time compare, in-window signature replay
  protection).
- Event pipeline: issue-status filtering, latest-state debounce (window
  closes on the newest state), idempotency journal covering both scheduled
  delivery ids and the composite event key.
- Notification channels — `apprise` (apprise-api HTTP), `ntfy` (JSON
  publish), `webhook` (generic JSON POST) — each with an `enabled` flag,
  plus a top-level master switch (global mute: deliveries still accepted
  and journaled, fan-out suppressed, live via SIGHUP reload).
- Plugin manifest subscribing to `issue.status_changed`, `task.completed`,
  `task.failed`, and a daily schedule heartbeat.
- Deployment assets: hardened systemd unit, TLS local-CA script, compose
  files for apprise-api/ntfy, annotated example configuration.
- Release matrix across nine platforms — linux amd64/arm64/loong64/riscv64/
  armv6, windows amd64/arm64, darwin amd64/arm64 — with sha256 checksums and
  a tag-triggered release workflow.
- Version resolution via ldflags injection with Go build-info fallback
  (module version, VCS revision + dirty marker); dev builds keep debug
  symbols.
- Versioning policy (documented in the README) and tooling: Makefile
  one-command targets, `mocksender` signed test-event sender, bilingual
  README, self-host deployment guide, AGENTS.md.
- Test suites: testify-based unit tests (signature rejection matrix, debounce
  semantics, journal restarts, adapters) and integration tests against a real
  ntfy (signed deliveries, replay rejection, channel isolation).

[0.1.0]: https://github.com/bg9ezn/multica-notify/releases/tag/v0.1.0
