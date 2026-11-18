# Getting started

English | [简体中文](zh-CN/getting-started.md)

SyncMaid keeps one or more **destination** folders in sync with a **source** folder, one way:
the source is the truth, and the destinations follow it. It runs when you click **Run**, on a
cron schedule, or whenever the source changes.

## Install

SyncMaid ships as a single `SyncMaid.exe`. There is no installer and no runtime to install —
copy it to any writable local folder (such as your user directory or another local drive) and
run it.

It is **portable**: everything it saves lives in a `Data` folder beside `SyncMaid.exe` on a
local fixed or removable NTFS, ReFS, or FAT/exFAT drive, so keeping the executable and its `Data`
folder together carries your entire setup without leaving task configuration behind on the host
machine. See [Storage](settings.md#storage).

## Your first task

1. Click **New task**.
2. Choose **Sync** or **Move** under **What this task does** (Sync copies and leaves the source
   untouched; Move routes files out of the source and deletes each source file after verified
   publication).
3. Enter a **Task name** — anything clear such as `Photos to NAS`.
4. Pick the **Source folder** with **Browse** (or enter its absolute path).
5. Leave **Trigger** on **Manual** for now — you can switch to **Watch** or **Scheduled** once
   you have verified a manual run.
6. Click **Save task**.

The task appears as a card showing **Manual** and **No destinations**. Nothing touches disk yet
because no destination has been configured.

## Add a destination

1. Open the destination workspace on the task card (**Edit destination** / **Destinations**), and
   click **Add destination**.
2. Enter a **Destination name** and select an existing **Destination folder** with **Browse** (the
   destination folder must already exist and be separate from the source: not inside the source
   and not containing it).
3. Choose how that destination behaves:
   - In a **Sync** task, pick **Sync strategy**:
     - **Add-only** — copy new and changed files; never delete files at the destination.
     - **Mirror** — keep all source files synchronized; removing extra destination files asks for
       confirmation on every run before deleting anything.
   - In a **Move** task, every destination uses **Move** and you choose collision handling (**Keep
     source** or **Add a number**) and layout (**Keep folder structure** or **Flatten to
     destination root**).
4. Optionally adjust **File filters for this destination** (or check **Source preview** to verify
   which source files match before running).
5. Click **Apply changes** on the row, then **Save**.

If you are unsure where to start, create a **Sync** task with an **Add-only** destination: it
never deletes anything. See [Tasks and destinations](tasks-and-destinations.md#sync-strategies).

## Run it

Click the **Run** button (`▷`) on the task card. Each destination row shows progress as it runs
and updates with counts when finished (such as **Synced just now · N files**). The task card
summarises the overall state as **All synchronized**, **Some destinations completed**, or reports
any destination failure.

Next steps:

- Make it automatic → [Triggers](triggers.md)
- Send only some files to a destination → [Filters](filters.md)
- Understand what protects your files → [Safety](safety.md)
- Check logs or diagnose errors → [Troubleshooting](troubleshooting.md)

## Multiple destinations per task

Click **Add destination** again to fan the same source out to multiple locations — for example,
a full **Mirror** to a backup drive and an **Add-only** copy of selected folders or extensions to
another folder. Each destination keeps its own filters, strategy, and run log.

In a **Move** task, destinations act as an ordered list of routing rules: each file moves to the
first matching destination and is removed from the source.

## Where your configuration lives

Tasks, settings, language preference, and run history live in the `Data` folder beside
`SyncMaid.exe` (`tasks-go.json`, `settings-go.json`, `locale`, `run-history-go.json`). See
[Storage](settings.md#storage).
