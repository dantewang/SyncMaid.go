# Tasks and destinations

English | [简体中文](zh-CN/tasks-and-destinations.md)

A **task** pairs one source folder with a trigger and task kind. A **destination** defines one
target folder along with its own strategy, options, and file filters. Every task can have one or
more destinations (or be saved with zero destinations as a draft before adding targets).

## What a task does

You choose the task kind when creating the task, and it is locked once the task has destinations
(switching kinds requires zero destinations because Sync and Move use different target semantics):

**Sync** copies matching files from the source and leaves the source untouched. Each destination
runs **Add-only** or **Mirror** and evaluates the full source independently, so the same source
file can be copied to multiple destinations. Each destination has its own status and run log.

**Move** files the source away. Every destination belongs to an *ordered routing list*: each
source file goes to the **first destination whose filters match it** and nowhere else. Files that
match no rule remain in the source. If a destination fails, files are **not** passed down to
later destinations.

Because first match wins, **Move destinations can overlap intentionally**, and reordering them
with the **Up** / **Down** buttons directly changes which destination claims matching files.
Putting an **All files** destination last acts as a catch-all for remaining files.

## The task card

Each saved task appears in the sidebar and as a card in the main pane:

- **Task name** and **source folder**
- **Trigger badge**: **Manual**, **Watch**, or **Scheduled (`0 2 * * *`)** (with **Next run**
  or live watch/trigger status)
- **Health summary**: **All synchronized**, **Some destinations completed**, **N destinations, M
  failed**, or **No destinations**
- **Task controls**: **Run** / **Stop**, **Destinations**, **Edit task**, **Delete task**, and a
  collapse/expand toggle
- **Destination rows**: strategy/filter summary, path, latest sync status (clickable to open the
  **Sync log** window), and destination edit/delete actions

The top bar provides **Collapse all** / **Expand all**, **Run all** (runs saved tasks
sequentially in list order; **Stop** cancels the active run and discards the remaining queue), and
**New task**. The sidebar's **More actions** menu provides **Run all**, **Run history**,
**Reload**, **Import legacy tasks**, **Migrate legacy regex**, and **Pause auto triggers** /
**Resume auto triggers**.

## Sync strategies

### Add-only

> Copy new and changed files; never delete destination files.

New files and files whose size or whole-second modification time differ are copied cleanly to the
destination. Files deleted from the source remain untouched at the destination, making Add-only
safe for append-style backups and shared folders.

### Mirror

> Copy new and changed files; removing extra destination files requires confirmation each time.

Mirror reproduces the source files at the destination:

- **Mirror only accepts `All files` filters.** The destination editor requires **All files** before
  switching to Mirror and disables filter editing for Mirror destinations.
- **Every run with extra files asks for confirmation.** SyncMaid scans the extras and displays
  a confirmation view listing the target path, deletion mode (**Move to Recycle Bin** or **Delete
  permanently (cannot undo)**), file count, and file paths. Click **Confirm removal** to proceed
  or **Cancel removal** to keep everything untouched. Confirmation applies only to that run.
- **Empty-source guard & empty directories.** Mirror refuses all deletions if the source is empty
  or unavailable, and it keeps empty directories at the destination (`Empty directories are kept`).

### Move

> Route each file to its first matching destination, then remove it from the source.

Key Move details:

- **Source safety:** A source file is deleted only after its destination file is staged, flushed,
  and published, and only after verifying the original source file identity has not changed.
- **Directory structure vs. Flatten:** By default (**Keep folder structure**), relative subfolders
  are preserved under the destination. Choosing **Flatten to destination root** places every moved
  file directly into the destination folder root.
- **Collision handling:** Neither option ever overwrites an existing file at the destination:
  - **Keep source** (default) — if a file with the same name already exists, leave the source file
    where it is and record it as retained.
  - **Add a number** — publish to a numbered sibling name before the extension (`report (2).pdf`,
    `archive.tar (2).gz`) using bounded retries and atomic non-replacing publication.

## Path rules SyncMaid enforces

Layout rules are checked on save and re-verified with real Windows directory handles/identities
(catching junctions and directory aliases) before any file operation begins:

1. **Source and destination cannot overlap or nest.** A destination cannot be the same as,
   inside, or an ancestor of its own source (`D:\Photos` → `D:\Photos\Backup` is rejected).
2. **Two tasks never share or nest sources or destinations.** Tasks can run on independent
   schedules and watchers, so overlapping paths across tasks are blocked.
3. **Two destinations of the same task never overlap or nest.** Sibling folders under a common
   parent are allowed; overlapping subtrees are rejected.
4. **Chaining is allowed.** Task A's destination *may* serve as Task B's source — SyncMaid does
   not forbid multi-stage pipelines across tasks.

## Editing and deleting

- Clicking **Edit task** edits only task-level fields (**What this task does** if empty, **Task
  name**, **Source folder**, **Trigger**).
- Clicking **Destinations** / **Edit destination** opens the destination workspace, where per-row
  edits can be applied, reordered, copied, or canceled individually before clicking **Save**.
- Deleting a task or destination asks for confirmation and removes only the configuration record
  in `tasks-go.json`. **Files at both source and destination are never deleted when removing a
  task or destination.**
