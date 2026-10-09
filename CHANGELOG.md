# Changelog

All notable changes to this project are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/) and the
project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `-init-config <path>` command: generates the annotated example
  configuration from an embedded template — always in lockstep with the
  binary's supported fields (drift-guarded by test) — refusing to overwrite
  an existing file unless `-force`. Release-artifact users no longer need
  the repository to obtain a starting config.
- README: release-artifact identification table (which file fits which
  device, sha256 verification, darwin unsigned note) and a related-projects
  section (Multica contract references, Apprise/apprise-api, ntfy).
- Deploy guide: download-a-release option with checksum verification —
  building from source is now optional.
- AGENTS.md for coding agents: contract facts, architecture, commands,
  conventions and release policy.

### Changed

- The standalone example file was removed in favor of the embedded template
  served by `-init-config`.
- Related-projects guidance clarified: Shoutrrr ships as a Go library/CLI
  only (no HTTP API) and is not an integration path for this bridge; a
  receiver without Apprise coverage is served by a thin native adapter
  instead.
- Local runtime configuration files (`config.yaml`, `.env`, ...) are
  git-ignored; the shipped template remains the embedded example.

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
