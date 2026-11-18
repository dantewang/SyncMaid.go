# Troubleshooting

English | [简体中文](zh-CN/troubleshooting.md)

## Reading statuses

Each destination row and task card shows its latest state:

| Status | Meaning |
|---|---|
| **Never run** | Destination has not completed a run yet. |
| **Scanning source folder** / **Syncing** / **Moving** | Active run stages with current file and counts. |
| **Waiting for deletion confirmation** | A Mirror run found extra files and is waiting for your **Confirm removal** / **Cancel removal** decision. |
| **All synchronized** / **Completed** | Every destination finished cleanly. |
| **Some destinations completed** | Multiple destinations had mixed outcomes (or one had no matching files / retained files). |
| **Failed · `<reason>`** | That destination failed with a specific diagnostic reason; other independent destinations still ran. |
| **Stopped** | You canceled the run; already-published destinations are kept and temporary files cleaned up. |
| **Interrupted / unknown (no end recorded)** | Loaded from run history where a previous run started without recording completion (for instance, power loss or forced termination). Verify files before assuming success. |

## Common situations

### "Trigger unavailable" on a task card

The task's **Watch** or **Scheduled** trigger could not start or hit a source error (such as an
unplugged drive or disconnected UNC share). Fix the folder accessibility; Watch retries
automatically every 30 seconds, or click **Edit task → Save task** after fixing. Manual **Run**
still works anytime.

### Scheduled or Watch tasks do not trigger automatically

- Confirm the task has at least one destination (`No destinations` tasks do not register automatic
  triggers).
- Check whether **More actions → Pause auto triggers** is active (a banner **Automatic triggers
  paused** will show **Resume auto triggers**).
- Make sure **Settings → Window → Close to tray** is turned **On** if you close the main window
  and expect background triggers to continue.
- For **Watch**, note the quiet countdown (default 10s): syncing starts only after changes stop
  for the full quiet period.
- For **Scheduled**, missed triggers (while asleep or closed) are never replayed retroactively.

### Files are not arriving at a destination

- Check **File filters for this destination** and click **Source preview** to see which files match
  or whether **No matches** is selected.
- For **Move** tasks, check destination order: an earlier destination may have claimed the file
  first, or **Keep source** may have retained a file that collided with an existing target file.
- Files currently locked/open in another application cannot be moved and fail/skip that item
  without deleting the source.

### Editor or run refuses a folder path

SyncMaid rejects overlapping, identical, or nested paths within a destination, across destinations
in a task, or across different tasks (including Windows directory junctions/aliases). Check each
task's source and destination paths and ensure no folder contains another. See [Path rules](tasks-and-destinations.md#path-rules-syncmaid-enforces).

### "This task cannot run or be edited" / Read-only filters

- Imported tasks containing **legacy regex rules** or complex rule combinations show as read-only
  in the filter editor and block execution until reviewed. Use **More actions → Migrate legacy
  regex** to review and confirm them against Go's regex engine.
- If `tasks-go.json` was damaged or externally modified, fix or restore from backup (**Restore
  task backup**) and click **More actions → Reload**.

## Logs & run history

SyncMaid stores structured per-run diagnostics in `Data\run-history-go.json` and runtime logs in
`Data\logs\`:

- Click a destination row's status text (for example `Synced ... · N files`) to open the **Sync
  log** window for that destination:
  - Displays up to the **latest 20 recorded runs** for that destination (counts of source,
    matched, published, moved, removed, retained, unchanged, failed, unmatched, bytes transferred).
  - Toggle **Diagnostic details** to reveal task/destination IDs and exact `run-history-go.json`
    path.
  - Click **Refresh** to reload diagnostics from disk without re-running sync.
  - Click **Open log file** to open `run-history-go.json` in your system viewer.
- Use **More actions → Run history** in the main window to view recent runs across all tasks.

## Starting over

- Deleting a task only removes its record from `tasks-go.json` — **files at both ends stay
  untouched**.
- To completely reset SyncMaid's state, **Exit** the app completely, then inspect or remove
  `tasks-go.json` (and `tasks-go.json.bak`), `settings-go.json`, `locale`, and
  `run-history-go.json` inside `Data\` beside `SyncMaid.exe`.
