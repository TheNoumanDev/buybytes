---
name: buybytes
description: Free disk space on a developer's Mac with the buybytes CLI. Use when the user says their disk or storage is full or low, asks what is using space, or wants to clean build folders, old simulators/emulators, AI models, agent worktrees, package caches or forgotten downloads.
---

# buybytes

`buybytes` scans the machine and returns everything reclaimable as JSON. You
read it, explain it, the user decides, then you remove exactly what they chose.

If `buybytes` isn't installed, use `npx buybytes@latest` in its place
(macOS only for now).

## 1. Scan

```bash
buybytes scan --json --min 100MB
```

Pick the mode from what the user wants: `--mode dev` (default) for developer
leftovers, `--mode media` for screen recordings, videos, archives and apps,
`--mode all` for everything. Apps, app caches and leftovers from deleted apps
are in both.

The report has `free_bytes`, `recommended_bytes`, `reclaimable_bytes`,
`skipped_folders` and `items[]`. Each item has `id`, `category`, `title`,
`detail`, `size_bytes`, `last_used`, `recommended`, `regenerable`,
`read_only`, `verdict` and sometimes `in_use`. Use `verdict` (remove,
probably remove, keep, check yourself, or an action like "commit first") as
your recommendation for each item.

## 2. Explain

Show the user a short table grouped like this:

- **Recommended**: `recommended: true`. These are safe to remove: they rebuild
  themselves or haven't been used in a while.
- **Your call**: everything else that isn't read-only. Say what each one costs
  the user to get back, using `detail`.
- **In use**: `in_use: true` (for example a Flutter version a project pins).
  Mention them so the user knows what stays. Only remove one if the user names it.
- **Needs you first**: `read_only: true`. Tell the user the `verdict` step (commit, close the emulator) that would make it removable. Never offer to remove these. Examples
  are dirty worktrees, running emulators and device backups.

If `skipped_folders` is not empty, tell the user macOS privacy settings kept
buybytes out of those folders. They can grant Full Disk Access to their
terminal and run the scan again.

## 3. Remove only what the user picked

```bash
buybytes clean --ids <id1,id2,...> --yes --json
```

- Removal moves things to the Trash by default. Add `--purge` only if the user
  asks for permanent deletion.
- Use `--dry-run` first if the user wants a preview.
- Never pass ids for items the user didn't approve. Never act on
  `--recommended` unless the user explicitly says to remove everything
  recommended.
- Report `freed_bytes` and any `error` per item. Space that went to the Trash
  comes back when the user empties it.
- To schedule cleanups, suggest
  `buybytes clean --recommended --yes --quiet --if-free-below 30GB --notify`
  in cron or launchd, but only set it up if the user asks.
- Afterwards you can offer `buybytes share` for a short, fun post about the
  cleanup. It contains only sizes and categories, never paths.
