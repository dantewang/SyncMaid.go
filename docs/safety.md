# Safety

English | [简体中文](zh-CN/safety.md)

SyncMaid is designed around one primary goal: **never lose or corrupt a file during transfer or
configuration writes**. The mechanisms below are always enabled.

## Atomic staging and replacement

Files are never overwritten in place:

1. Data is copied to a hidden temporary file (`.syncmaid-*.tmp`) in the target file's own parent
   directory, with timestamps applied and buffers flushed (`Sync`).
2. Only after the temporary file finishes writing and passes verification is it atomically
   published over the target (or published to a numbered name in Move `Add a number` mode).
3. If power is lost, disk space runs out, or you click **Stop** mid-copy, the previous complete
   destination file stays intact and leftover temporary files are cleaned up automatically.

For **Move** tasks, the source file is removed **only after** verified target publication and only
after checking that the open source file handle still points to the same unchanged source file.

## Size, timestamp, and content verification

- During sync, SyncMaid avoids recopying files when size and whole-second modification timestamp
  match (`TransferNeeded`).
- Every write checks exact written byte length against the source size and flushes to disk before
  publishing.
- Destinations marked with content verification (`VerifyContents` in `tasks-go.json`) compute and
  compare **SHA-256** digests on read-back before publishing the temporary file, guarding against
  silent I/O or RAM/storage corruption.

## Path, symlink, and directory-handle protection

- Before transferring files, SyncMaid opens and pins real Windows directory handles, resolving
  volume and 128-bit file IDs so directory junctions or aliases cannot trick two paths into
  overlapping silently.
- Source and destination scans refuse to follow reparse points / junctions / symlinks or read
  corrupted directory listings: if scanning fails partway through, no partial inventory is used to
  authorize writes or deletions.

## Mirror deletion guards

Because Mirror deletes destination files that no longer exist in the source, three safeguards sit
in front of deletions:

1. **All-files contract**: Mirror cannot be combined with file filters (excluding part of the
   source cannot authorize deleting from the destination).
2. **Missing or empty source protection**: if the source folder is missing, inaccessible, or
   contains no files at all (e.g., an unplugged USB drive or unmounted share), Mirror refuses all
   deletions so an empty mount point never wipes a backup.
3. **Interactive per-run confirmation**: whenever a Mirror run detects extra files to remove, it
   pauses and presents the **Confirm this Mirror run** view listing the exact file count,
   destination path, deletion mode (**Move to Recycle Bin** or **Delete permanently (cannot
   undo)**), and paths. Deletions occur only if you click **Confirm removal** for that run.

Mirror does **not** remove empty directories at the destination (`Empty directories are kept`),
and Recycle Bin deletions are attempted only when supported by Windows Shell (for example, network
shares or unsupported targets do not silently fall back to permanent deletion if Recycle Bin is
selected and fails).

## Configuration safety

- `tasks-go.json`, `settings-go.json`, `locale`, and `run-history-go.json` are read with strict
  validation and published atomically.
- If `tasks-go.json` is unreadable or corrupted, saving and running are blocked rather than
  overwriting your tasks with an empty list; when a valid `.bak` backup is present, SyncMaid offers
  **Restore task backup** with confirmation.
- Unknown metadata fields are preserved where possible rather than silently dropped; tasks or
  rules with unsupported fields/profiles fail closed rather than executing with partial rules.

## What SyncMaid does not protect against

- **Deleting files in the source before syncing:** under **Mirror**, confirmed deletions remove
  extra files from the destination too. Use **Add-only** for append-only archives.
- **Bit-rot after a completed sync:** SyncMaid verifies data while transferring; it does not
  continuously scrub idle files afterward.
- **Replacing proper backups:** keep multiple backup copies or versioned snapshots for
  irreplaceable data.
