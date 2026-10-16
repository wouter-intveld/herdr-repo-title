# herdr-repo-title

A [Herdr](https://herdr.dev) plugin that names each tab after the git
repository its focused pane is in. That is all it does.

| Focused pane is in            | Tab label     |
| ----------------------------- | ------------- |
| `~/git/dashboard/src`         | `dashboard`   |
| `~/git/dashboard-wt/feat-x` (worktree of `dashboard`) | `dashboard` |
| `~/git/infra.git` worktree / `.bare` layout | `infra` |
| `~/Downloads` (no repo)       | `Downloads`   |

- The directory is the foreground process's (`foreground_cwd`), falling back to
  the pane's `cwd`, so a `cd` in the shell or an agent started elsewhere counts.
- A tab with several panes follows the focused one.
- Rename a tab yourself and the plugin leaves it alone. Rename it back to a
  number (Herdr's default) or to the name the plugin last gave it to hand it
  back.

Linux and macOS only.

## Install

Needs Herdr 0.9.0+ and Go 1.24+ (built at install time).

```sh
herdr plugin install def4ce/herdr-repo-title
herdr plugin action invoke def4ce.repo-title.restart
```

Herdr starts plugins only when its server starts; the second line starts this
one in the running server. Run it again after an update.

### From a checkout

```sh
go build -o herdr-repo-title .
herdr plugin link "$PWD"
herdr plugin action invoke def4ce.repo-title.restart
```

## How it works

Herdr emits no event when a shell changes directory, and an agent pane emits
dozens of `pane.updated` events a second from its title spinner, so event
hooks are no use here. The `[[startup]]` hook runs `herdr-repo-title run`,
which reads `session.snapshot` over `HERDR_SOCKET_PATH` once a second and calls
`tab.rename` when a label is out of date. Repository names come from walking up
to the nearest `.git` and resolving git's common dir, without spawning git.

The loop exits when its server's socket goes away (or is replaced), and after
30 consecutive failed polls. One loop runs per Herdr session, guarded by a
`flock`. Files live in `HERDR_PLUGIN_STATE_DIR`
(`~/.local/state/herdr/plugins/def4ce.repo-title/` on Linux), one set per
session socket:

- `<hash>.log`: start, renames, errors
- `<hash>.json`: tab id → label the plugin last wrote (manual-rename detection)
- `<hash>.lock`: pid of the running loop

## Development

```sh
go test ./...
```
