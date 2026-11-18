# Triggers

English | [简体中文](zh-CN/triggers.md)

A task's **Trigger** controls when it runs automatically. You set one trigger per task in the
task editor (**New task** / **Edit task**). Regardless of the trigger setting, clicking **Run** on
a task card (or **Run all** in **More actions**) always starts it manually.

## Manual

The task runs only when you trigger it manually via **Run** or **Run all**. Nothing watches the
filesystem or runs in the background.

## Watch

The task monitors its source folder and triggers a run after changes settle:

- **Quiet period**: set between **1 and 600 seconds** (default **10** seconds).
- Every change event resets the quiet countdown so multi-file saves or large copies coalesce into
  a single run.
- **Network / non-local folders**: on UNC shares or mapped network drives where native Windows
  change notifications are unreliable, SyncMaid automatically falls back to polling scans and
  applies the same quiet period.
- **Self-trigger guard**: while a task is actively running, its own watcher is suspended and
  re-arms only after the run finishes, so Move tasks do not re-trigger on files they just moved out.
- If establishing the watcher fails (for example, a disconnected drive), the task shows **Trigger
  unavailable** / **Watch failed; retry in 30s** and retries automatically every 30 seconds.

## Scheduled

The task runs according to a standard **five-field cron expression** in your computer's **local
time**:

```text
minute  hour  day-of-month  month  weekday (0-6, 0 = Sunday)
```

| Expression | Runs |
|---|---|
| `*/5 * * * *` | Every 5 minutes |
| `0 * * * *` | Every hour, on the hour |
| `0 2 * * *` | Every day at 02:00 |
| `0 2 * * 1` | Every Monday at 02:00 |
| `30 22 1 * *` | 22:30 on the 1st of each month |

Restrictions and behavior:

- Seconds fields, year fields, `?`, and `TZ=` / `CRON_TZ=` prefixes are unsupported.
- While typing in the task editor, SyncMaid displays the calculated next run time (or warns if a
  valid expression such as `0 0 30 2 *` has no upcoming runs).
- **No catch-up**: if the machine is asleep, SyncMaid is closed, or a schedule notification is
  delayed by more than 5 seconds, that missed occurrence is skipped and the next future run is
  scheduled normally.

## Pausing and editing triggers

- **More actions → Pause auto triggers** pauses all Watch and Scheduled triggers for the current
  session; **Resume auto triggers** re-enables them and recalculates scheduled next runs.
- Editing an existing task pauses its automatic trigger until changes are saved or discarded.
- Tasks with **no destinations** do not register active watchers or scheduled timers.

## During a run

- The task card shows **Stop** while running and live progress per destination (`Copying`,
  `Verifying`, `Publishing`, or Mirror removal confirmation).
- Clicking **Stop** cancels remaining work safely: files already published at completed
  destinations are kept, in-flight temporary files are cleaned up without corrupting existing
  destination files, and any queued targets or **Run all** batch items are canceled.
- Two runs of the same task never overlap; duplicate trigger arrivals while running are coalesced.
