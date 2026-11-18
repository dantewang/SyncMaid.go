# Filters

English | [简体中文](zh-CN/filters.md)

Filters decide **which source files reach a particular destination**. They are configured per
destination under **File filters for this destination**.

Three modes are available in the destination editor:

- **All files** — everything under the source folder (default).
- **No matches** — receives no files; existing destination files are kept untouched.
- **Only matching** — build one or more include/exclude groups below.

> **Mirror destinations always sync all files.** You must set filters to **All files** before
> choosing Mirror, and filter controls are locked while Mirror is active.

## Include/exclude groups (`Only matching`)

Rules are organized into **groups**:

- Across multiple groups, choose **All groups** (AND: file must match every group) or **Any
  group** (OR: file matches at least one group).
- Within each group, choose **All rules** (AND) or **Any rule** (OR), and add rows with:
  - **Include** / **Exclude** toggle on each rule
  - Rule kind: **Path prefix**, **Extension**, or **Wildcard** (one item per line in the rule
    box; blank lines and leading/trailing spaces are ignored)

Each line matches as follows:

- **Path prefix** — folder prefix relative to the source root (`photos/2024` matches files under
  `<source>\photos\2024`).
- **Extension** — file extension without requiring a leading dot (`jpg` matches every `.jpg`
  anywhere under the source).
- **Wildcard** — glob pattern matched against the relative path (`\` is normalized to `/` and
  matching is case-insensitive):
  - `*` — any characters within a single folder (never crosses `/`)
  - `?` — any single character (never crosses `/`)
  - `**` — any number of folder levels, including zero

| Pattern | Matches |
|---|---|
| `**/ChatGPT*.png` | `ChatGPT 1.png`, `saved/ChatGPT-2.png` at any depth |
| `photos/*.raw` | `photos/a.raw`, not `photos/2024/a.raw` |
| `photos/**/*.raw` | every `.raw` under `photos\` at any depth |
| `photos/**` | everything under `photos\` |
| `IMG_????.jpg` | `IMG_0042.jpg` at the source root |

## Source preview

Inside the destination workspace, click **Source preview** (or **Refresh preview**) to scan the
current source draft read-only before running or saving:

- Shows total source file count (or whether the folder is empty / contains no regular files)
- Lists matched count and sample paths per destination, **Unmatched** files, and (for Move tasks)
  **Multiple matches (first wins)** showing which destination claimed a contested file
- Displays top **Source extensions** chips you can click to quickly append an extension filter
  into an open group

Preview is strictly read-only and never modifies files or refreshes regex confirmations.

## Filter summaries

On task cards and destination rows, SyncMaid shows a compact filter summary (such as `All files`,
`No filters`, or condition summaries like `All conditions: ...`). If **Only matching** is
selected with no rules yet, saving or previewing warns **Filter rules must not be empty**.

## Legacy regex rules & migration

The native destination editor does **not** accept new raw regular expression input directly.

If you imported or loaded a task from legacy `tasks.json` that already contains `regex` rules (or
complex rule structures the native editor cannot represent losslessly):

- Those rules are preserved unchanged and shown **read-only** in the destination editor so they
  are never silently lost or downgraded.
- Legacy unconfirmed regex rules **block execution** of that Sync destination (or the entire Move
  task) until reviewed.
- Open **More actions → Migrate legacy regex** with the task selected to inspect each legacy
  pattern, test it against up to 16 sample relative paths under Go's new regular expression
  semantics (**Preview new regex**), and explicitly save confirmation (**Confirm new semantics
  and save**).

Notes on Go regex semantics used after migration:

- Backed by Go's RE2 `regexp` engine: case-insensitive by default (`(?-i)` turns case sensitivity
  on), `\` in inspected relative paths is normalized to `/`, Perl character classes and word
  boundaries are ASCII-based, and look-ahead / look-behind / back-references are unsupported.
- Confirmations are bound to the specific task, rule, pattern, and Go profile version; editing
  the task or pattern later invalidates the confirmation until reviewed again.

## How filters interact with strategies

- **Add-only** — selects which source files get copied. Changing a filter later does not delete
  previously copied files at the destination.
- **Move** — routes each file to the **first** matching destination; unmatched files remain in the
  source.
- **Mirror** — requires **All files** by design.
