# SyncMaid User Guide

English | [简体中文](zh-CN/README.md)

How to use SyncMaid — one-way file sync for Windows: one source folder, one or more
destinations, each with its own filters and strategy, run manually, on a local cron schedule, or
whenever the source changes.

New here? Start with [Getting started](getting-started.md); it takes a few minutes to set up
your first working task.

## Guides

- **[Getting started](getting-started.md)** — run the portable executable, create your first
  task, add a destination, and run it.
- **[Tasks and destinations](tasks-and-destinations.md)** — Sync vs. Move tasks, Mirror,
  Add-only and Move strategies, multiple destinations per task, and the path rules SyncMaid
  enforces.
- **[Filters](filters.md)** — choosing which files reach a destination (`All files`,
  `No matches`, or `Only matching` include/exclude groups), source preview, and migrating legacy
  regex rules.
- **[Triggers](triggers.md)** — running manually, on a local five-field cron schedule, or by
  watching the source for changes.
- **[Settings](settings.md)** — interface language, starting with Windows, system tray
  behavior, portable `Data` storage, and importing legacy tasks.
- **[Safety](safety.md)** — what protects your files during copy/move/mirror runs, how Mirror
  deletion confirmation works, and what SyncMaid intentionally does not do.
- **[Troubleshooting](troubleshooting.md)** — reading task/destination statuses, viewing the
  sync log and run history, common error messages, and resetting configuration safely.

## Elsewhere

Development and safety invariants are documented in [AGENT.md](../AGENT.md).
