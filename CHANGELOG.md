# Changelog

All notable changes to this project are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/) and the
project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Release matrix extended to `linux/loong64` (Loongson) and `darwin/amd64`
  (Intel Mac) + `darwin/arm64` (Apple Silicon); darwin binaries are unsigned
  (Gatekeeper: `xattr -d com.apple.quarantine` on first run).
- Versioning policy documented in the README.

## [0.2.0] - 2026-10-10

### Added

- Per-channel `enabled` flag — keep a channel declared but dormant.
- Top-level master switch (`enabled: false` = global mute: deliveries still
  accepted and journaled, fan-out suppressed; live via SIGHUP reload).
- Release artifacts now cover `linux/amd64`, `linux/arm64` (Raspberry Pi and
  any 64-bit Linux), `windows/amd64` and `windows/arm64`.

### Changed

- Version resolution falls back through Go build info (module version from
  `go install`, then embedded VCS revision with dirty marker) instead of a
  bare `dev`; dev builds keep debug symbols — only release artifacts are
  stripped.
- Tests migrated to testify (assert/require, Eventually/Never async
  assertions).

## [0.1.0] - 2026-10-10

### Added

- Hook server verifying Multica plugin-hook deliveries
  (HMAC-SHA256, ±5 min timestamp window, constant-time compare, in-window
  signature replay protection).
- Event pipeline: issue-status filtering, latest-state debounce, delivery-id
  idempotency journal.
- Notification channels: `apprise` (apprise-api HTTP), `ntfy` (JSON publish),
  `webhook` (generic JSON POST).
- Multica plugin manifest (`manifest/multica.plugin.json`) subscribing to
  `issue.status_changed`, `task.completed`, `task.failed`, plus a daily
  schedule heartbeat.
- Deployment assets: systemd unit, TLS local-CA script, compose files for
  apprise-api/ntfy, example configuration.
- Tooling: `cmd/mocksender` (signed test-event sender), Makefile
  (clean/build/test/test-integration/lint/package/run), GitHub Actions CI and
  release workflows.
