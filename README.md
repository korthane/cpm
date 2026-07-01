# cpm — Claude Plugin Manager

A terminal UI for comparing and managing Claude Code configuration across
multiple **profiles** (distinct `CLAUDE_CONFIG_DIR` directories such as
`~/.claude`, `~/.claude-work`, `~/.claude-personal`).

The core view is a comparison table: one column per profile plus a pinned
rightmost identity column, one row per resource. Two tabs:

- **Plugins** — for every `plugin@marketplace` seen in any profile, its state
  in each profile (`vX.Y.Z`, `disabled (vX.Y.Z)`, or `—` when absent) and the
  latest available version in the pinned column. Versions behind latest are
  highlighted with a `↑` marker. Actions: enable, disable, update, uninstall,
  and install into a profile where the plugin is missing.
- **MCP** — the same layout for MCP servers (presence and target per profile).
  MCP has no update concept; v1 supports viewing and removing servers.

Each profile column header shows the directory path plus the account email and
subscription plan for that profile.

![screenshot placeholder](docs/screenshot.png)

cpm is a thin front end over the public `claude` CLI: all reads use
`claude ... --json` and all mutations use `claude plugin ...` / `claude mcp
remove`, each invoked with `CLAUDE_CONFIG_DIR` pointed at the target profile.
cpm never edits Claude's internal JSON files directly.

## Requirements

- The `claude` CLI must be on `PATH` — every read and action shells out to it.
- Go 1.26+ to build from source.

## Install / build

```sh
go install github.com/korthane/cpm/cmd/cpm@latest
```

Or from a checkout:

```sh
make build   # produces ./cpm
make test    # go test ./...
make lint    # golangci-lint run
make run     # go run ./cmd/cpm
```

## Usage

```sh
cpm                              # auto-discover ~/.claude* profiles
cpm ~/.claude ~/.claude-work     # show only these profiles, in this order
```

On start the table shell renders immediately and every profile column loads in
parallel (a per-column spinner shows until its data arrives). Loading a profile
also re-fetches its marketplaces so the pinned "latest" column never comes from
a stale cache; if a refresh fails, cpm falls back to the cached catalog and
marks the header `latest (stale)`. The MCP tab loads lazily on first view
because `claude mcp list` runs a health check per server and is slow.

### Profile discovery precedence

Highest wins; lower tiers are ignored entirely once a higher one is non-empty:

1. **CLI args** — `cpm <dir> [<dir>...]` shows exactly the given directories.
2. **Config file** — `~/.config/cpm/config.yaml` (see below) controls the set,
   order, and labels.
3. **Auto-discovery** — home-level `~/.claude*` directories, sorted by name.

Paths from CLI args and the config file support `~` expansion and are
de-duplicated (first occurrence wins). If no tier yields a profile, cpm exits
with an error.

### Config file

Optional, at `~/.config/cpm/config.yaml`:

```yaml
profiles:
  - path: ~/.claude
    label: personal
  - path: ~/.claude-work
    label: work
  - path: ~/.claude-experiments   # label defaults to the path basename
```

- `path` (required) — the profile's `CLAUDE_CONFIG_DIR`; `~` is expanded.
- `label` (optional) — column header text; defaults to the path basename.

Profiles are shown in file order. A missing config file is fine (auto-discovery
kicks in); malformed YAML is an error.

### Keybindings

| Key | Action |
| --- | --- |
| `←` / `→` | select profile column (the table scrolls to keep it visible) |
| `↑` / `↓` | select row |
| `tab` | switch between the Plugins and MCP tabs |
| `r` | reload the active tab's data |
| `q` / `ctrl+c` | quit |

Plugins tab, applied to the selected cell:

| Key | Action |
| --- | --- |
| `e` | enable (disabled plugin) |
| `d` | disable (enabled plugin) |
| `u` | update (installed plugin) |
| `x` | uninstall (installed plugin; asks `y/n`) |
| `i` | install into a profile where the plugin is absent |

MCP tab:

| Key | Action |
| --- | --- |
| `x` | remove the server from the selected profile (asks `y/n`) |

Action keys are validated against the cell state (e.g. `i` only works where
the plugin is absent) and show a hint on mismatch. Destructive actions
(`x` uninstall/remove) require a `y` confirmation; any other key cancels.
After an action succeeds, only the affected profile's data is reloaded.
