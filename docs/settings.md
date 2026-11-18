# Settings & Storage

English | [简体中文](zh-CN/settings.md)

Open **Settings** via the gear icon (`⚙`) at the bottom of the left task sidebar. Changes apply
immediately unless noted.

## General

### Interface language

- **System default** follows Windows.
- Or choose **English**, **简体中文**, **繁體中文**, or **日本語** directly.
- Switches the entire UI immediately without restarting and persists to `Data/locale` without
  altering task names, paths, drafts, or regex confirmations.

## Startup

- **Start at login**: registers a per-user startup entry for the current folder's `SyncMaid.exe`
  under Windows Registry (`HKCU\...\Run`, value name `SyncMaid.Go.<folder-key>`), visible in
  **Windows Task Manager → Startup apps**.
- No administrator privileges are required.
- If disabled by the user in Windows Task Manager (`StartupApproved`), SyncMaid reports
  **Registered, disabled by system** and respects the OS setting without overriding it.
- Moving `SyncMaid.exe` to a different folder requires toggling **Start at login** again so the
  entry points to the new path.

## Window

- **Close to tray** (default: off):
  - **On**: clicking the window close button (`×`) hides SyncMaid to the system tray and keeps
    running and scheduled/watch tasks active. Click the tray icon or right-click → **Show main
    window** to restore; use tray → **Exit** (or **More actions → Exit**) to quit.
  - **Off**: closing the window requests exit (canceling any active run cleanly before closing).
- **Start minimized** (default: off): launches directly to the system tray with only the tray icon
  visible (effective on next launch).

## Storage

SyncMaid is **portable**: all state lives inside a `Data` folder next to `SyncMaid.exe` on a local
fixed or removable NTFS, ReFS, or FAT/exFAT volume:

| Path inside `Data\` | Purpose |
|---|---|
| `tasks-go.json` | Saved tasks, destinations, strategies, triggers, and confirmed Go regex approvals |
| `tasks-go.json.bak` | Automatic backup managed by `taskstore` |
| `settings-go.json` | **Close to tray** and **Start minimized** settings |
| `locale` | Saved interface language preference (`Data/locale`) |
| `run-history-go.json` | Latest result per task and bounded recent run history (up to 100 runs) |
| `logs\` | Application log files |
| `cache\` | Runtime cache directory |

Click **Open data folder** to reveal `Data` in Explorer.

### Single-instance & safety notes

- **Writable location**: place `SyncMaid.exe` in a writable directory (not under `Program Files`
  without admin rights) so it can create and lock `Data`.
- **One instance per `Data` folder**: launching `SyncMaid.exe` a second time from the same folder
  brings the existing window to the front instead of opening a second writer over the same files.
- **Two separate folders**: copies in two distinct folders manage separate `Data` directories
  independently.

## Migrating from the legacy Rust version (`tasks.json` / settings)

SyncMaid.go keeps its own files (`tasks-go.json`, `settings-go.json`) and **never automatically
modifies or overwrites** legacy `tasks.json` or old `settings.json`:

- **Import legacy tasks**: use **More actions → Import legacy tasks** to pick an existing
  `tasks.json` (or backup), inspect the preview, and append imported tasks as **Manual** tasks
  into `tasks-go.json`. Exit the legacy application before running imported tasks so both copies
  do not touch the same folders simultaneously.
- **Open legacy settings or records**: in **Settings → Storage**, click **Open legacy settings
  or records** to optionally import only **Close to tray** and **Start minimized** from a legacy
  `settings.json`.

## About

Shows the application version from [`mygo.json`](../mygo.json).
