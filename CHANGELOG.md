# Changelog

All notable changes to this project are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/) and the
project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed

- Version resolution now falls back through Go build info (module version
  from `go install`, then embedded VCS revision) instead of reporting a bare
  `dev` for binaries built outside the Makefile; dev builds keep debug symbols
  (only release artifacts are stripped).

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
