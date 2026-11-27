<div align="center">

<img src="assets/syncmaid.png" alt="SyncMaid" width="128" />

# SyncMaid

**One-way file sync for Windows, done for you.**

English | [简体中文](README.zh-CN.md)

SyncMaid watches a source folder and keeps one or more destinations in sync with it — each
destination with its own filters and strategy (Mirror, Add-only, or Move), triggered manually,
on a cron schedule, or by watching for source changes. It lives in the system tray and keeps
working while you don't.

</div>

SyncMaid is written in Go using [MyGo](https://github.com/egoist/mygo) native Windows UI, and it
is **portable only**: everything it saves lives in a `Data` folder beside the executable, so the
whole app is a folder you can move without leaving anything behind on the host machine.

## Documentation

- **User Guide (English)** — [`docs/`](docs/README.md)
- **使用指南（简体中文）** — [`docs/zh-CN/`](docs/zh-CN/README.md)
- **Contributing / working with agents** — [AGENT.md](AGENT.md)

## Requirements

- Windows 10 or later (the app is Windows-only)
- Go 1.27.4 or later (`go.mod` also declares `tool github.com/egoist/mygo/cmd/mygo` v0.10.0)
- `CGO_ENABLED=0` (pure-Go native Win32/MyGo build; no C compiler required)

## Build & Run

Run in development:

```powershell
$env:CGO_ENABLED = "0"
go run .
```

Build the standalone executable (`build/windows-amd64/SyncMaid.exe`):

```powershell
$env:CGO_ENABLED = "0"
go tool mygo build -skip-installer
```

## Tests

Run `go vet` and the full test suite:

```powershell
go vet ./...
go test -count=1 ./...
```

Run just the transfer and filter engine tests (`internal/syncengine`):

```powershell
go test ./internal/syncengine
```

Tests verify atomic temporary-file staging and publication, directory handle pinning, overlap
and symlink/junction rejection, cancellation cleanup, Mirror confirmation guards, Move
first-match routing and numbered suffix handling, bounded run history persistence, and native
UI session state transitions.

## Packaging & Release

The compiled executable (`build/windows-amd64/SyncMaid.exe`, produced by
`go tool mygo build -skip-installer`) embeds the application icon, icons/SVGs, translations, and
metadata from [`mygo.json`](mygo.json). Copy `SyncMaid.exe` into any writable local folder; it
creates its `Data` directory right beside itself on first run.
