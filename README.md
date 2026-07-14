# cpm — Claude Plugin Manager

A terminal UI for comparing and managing Claude Code configuration across
multiple **profiles** (distinct `CLAUDE_CONFIG_DIR` directories such as
`~/.claude`, `~/.claude-work`, `~/.claude-personal`).

The core view is a comparison table: one column per profile plus a pinned
rightmost identity column, one row per resource. Two tabs:

- **Plugins** — rows grouped by marketplace. Each group starts with a
  marketplace header row (a NerdFont chevron shows its fold state; `enter`
  folds/unfolds) whose cells show the git commit hash and date of the
  marketplace clone per profile — marketplaces have no version, so commit
  freshness is the signal (`local` for a directory source without git info,
  `—` where the marketplace is not configured, blank when the state could
  not be read — the git lookup or the marketplace listing failed). Below it,
  every
  `plugin@marketplace` seen in any profile: its state in each profile
  (`vX.Y.Z`, `disabled (vX.Y.Z)`, or `—` when absent) and the latest
  available version in the pinned column. Versions behind latest are
  highlighted with a `↑` marker. Plugin actions: enable, disable, update,
  uninstall, and install into a profile where the plugin is missing.
  Marketplace actions: add, update, remove.
- **MCP** — the same layout for MCP servers (presence and target per profile).
  MCP has no update concept; v1 supports viewing and removing servers.

Each profile column header shows the directory path plus the account email and
subscription plan for that profile. A logged-out profile shows `not logged in`;
if the auth status cannot be read at all, the line stays blank. For the default
`~/.claude` profile a logged-out answer is double-checked without
`CLAUDE_CONFIG_DIR` set: macOS Keychain stores credentials under a different
name depending on whether the variable was set at login, so a plain `claude`
login would otherwise show up as logged out.

cpm is a thin front end over the public `claude` CLI: all reads use
`claude ... --json` (except `claude mcp list`, which has no JSON mode and is
parsed as plain text) and all mutations use `claude plugin ...` / `claude mcp
remove`, each invoked with `CLAUDE_CONFIG_DIR` pointed at the target profile.
cpm never edits Claude's internal JSON files directly.

## Requirements

- The `claude` CLI must be on `PATH` — every read and action shells out to it.
- `git` on `PATH` is optional but recommended: marketplace freshness (commit
  hash and date) is read from each clone with `git log`; without it those
  header cells stay blank.
- The fold chevrons are NerdFont glyphs; without a NerdFont-patched terminal
  font they render as replacement boxes (cosmetic only).
- Go 1.26.4+ to build from source.

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
cpm -h                           # print usage
```

cpm takes no flags other than `-h`/`--help`; any other dashed argument is
rejected as a typo rather than treated as a profile directory.

On start the table shell renders immediately and every profile column loads in
parallel (a per-column spinner shows until its data arrives). Loading a profile
also re-fetches its marketplaces so the pinned "latest" column never comes from
a stale cache; if a refresh fails, cpm falls back to the cached catalog and
marks the header `latest (stale)`. The MCP tab loads lazily on first view
because `claude mcp list` runs a health check per server and is slow.

Every CLI call cpm fires is time-bounded (two minutes; the marketplace refresh
alone at 30 seconds), so a hung `claude` degrades to a per-column error — or,
for the refresh, to the `latest (stale)` fallback — instead of freezing the UI.
If an action times out, the CLI is killed and the profile is reloaded, since
the change may have partially applied.

### Profile discovery precedence

Highest wins; lower tiers are ignored entirely once a higher one is non-empty:

1. **CLI args** — `cpm <dir> [<dir>...]` shows exactly the given directories.
2. **Config file** — `~/.config/cpm/config.yaml` (see below) controls the set,
   order, and labels.
3. **Auto-discovery** — home-level `~/.claude*` directories, sorted by name.

Paths from CLI args and the config file support `~` expansion. Profiles are
de-duplicated by resolved path — trailing slashes and symlinked aliases
collapse into one column, first occurrence wins — so two columns can never
point at the same config dir. If no tier yields a profile, cpm exits
with an error. Every resolved path must be an existing directory; cpm exits
with an error otherwise, so typos fail fast instead of surfacing as a column
error inside the TUI.

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
kicks in); malformed YAML or an unknown key (e.g. `profile:` instead of
`profiles:`) is an error rather than a silent fallback to auto-discovery.

### Keybindings

| Key | Action |
| --- | --- |
| `←` / `→` / `h` / `l` | select profile column (the table scrolls to keep it visible) |
| `↑` / `↓` / `j` / `k` | select row (tall tables scroll to keep it visible) |
| `tab` / `shift+tab` | cycle between the Plugins and MCP tabs |
| `/` | filter rows by name (see below) |
| `esc` | clear the active name filter |
| `r` | reload the active tab's data |
| `q` / `ctrl+c` | quit |

Besides the selected cell, the selected row's pinned name cell (marketplace,
plugin, or MCP server name) is shown in reverse video, so the current row
stays findable on wide tables.

### Filtering by name

`/` opens a text input above the table and narrows the rows as you type. The
match is fuzzy (a case-insensitive subsequence, so `fb` matches `foo-bar`) and
literal — regex and glob characters have no special meaning. On the Plugins tab
it is tried against both the plugin name and the marketplace name: a matching
marketplace keeps its whole group, a marketplace with no matching plugins and a
non-matching name drops out. Rows keep their marketplace grouping and
alphabetical order; the filter never re-ranks them. Folded groups unfold while a
filter is active — otherwise a fold would hide matches — and their fold state
comes back once the filter is cleared; `enter` does not fold while filtering.
Changing the query moves the selection back to the first matching row. A query
that matches nothing replaces the table with `no plugins match "…"` (or
`no MCP servers match "…"`), so an over-narrow filter never reads as an empty
profile.

A marketplace header whose group the filter narrowed carries the count of what
it is hiding (`mp (+2 hidden)`): the header's actions — `x` above all, which can
drop the marketplace's installed plugins — still reach the whole marketplace,
not just the rows on screen.

`enter` closes the input but keeps the filter applied, so navigation and action
keys operate on the visible subset. `esc` clears the filter and restores the
full list, both from inside the input and while navigating an already-filtered
list. While the input is focused every rune goes into it: `q` does not quit and
`e`/`d`/`u`/`x`/`i` do not fire actions, and the help line shows only the keys
that work in that mode. Only `ctrl+c` (quit) and `tab`/`shift+tab` (which close
the input, keep the query, and switch tabs) still act, since neither is useful
as literal text. With the input closed and a filter still applied, an indicator
above the table shows the query and the match count, so an active filter is
never invisible. Each tab keeps its own query, so switching tabs does not
disturb the other's filter.

Plugins tab, on a plugin row, applied to the selected cell:

| Key | Action |
| --- | --- |
| `e` | enable (disabled plugin) |
| `d` | disable (enabled plugin) |
| `u` | update (installed plugin) |
| `x` | uninstall (installed plugin; asks `y/n`) |
| `i` | install into a profile where the plugin is absent |

Plugins tab, on a marketplace header row:

| Key | Action |
| --- | --- |
| `enter` / `space` | fold or unfold the group (folded headers show `(n plugins)`) |
| `i` | add the marketplace to a profile where it is missing |
| `u` | update the marketplace clone in the selected profile |
| `x` | remove the marketplace from the selected profile (asks `y/n`) |

Adding needs a usable source: it is refused when no profile knows the
marketplace's source or when profiles disagree about it.

MCP tab:

| Key | Action |
| --- | --- |
| `x` | remove the server from the selected profile (asks `y/n`) |
| `i` | not supported in v1 — shows a hint to use `claude mcp add` directly |

Action keys are validated against the cell state (e.g. `i` only works where
the plugin is absent) and show a hint on mismatch. Installing a plugin into a
profile that lacks its marketplace adds the marketplace there first (when its
source is known from another profile), then installs; without a usable source
the install is refused with a hint. Destructive actions
(`x` uninstall/remove) require a `y` confirmation; any other key cancels
(`ctrl+c` still quits). After an action succeeds, only the affected profile's
data is reloaded.

All plugin and marketplace mutations run with `--scope user`, so they only ever
edit the selected profile's own config (`marketplace update` has no scope flag;
for `marketplace remove` the flag is what keeps the removal from hitting every
scope). A plugin installed at project or local scope
(cwd-dependent, shown identically in every column) cannot be managed from cpm —
actions on such rows are refused with a hint; use `claude plugin` in the owning
directory instead.

### MCP caveats

- `claude mcp list` reports servers from every scope, including project/local
  scope servers tied to the directory cpm is launched from and servers
  provided by plugins (`plugin:<plugin>:<name>`). Those rows look identical
  in every profile column.
- Removal runs `claude mcp remove --scope user <name>`, so it only ever edits
  the selected profile's own config. Removing a project/local-scope row fails
  with the CLI's error; use `claude mcp remove` in the owning directory for
  those. Plugin-provided servers cannot be removed this way either; cpm blocks
  the action and suggests uninstalling the plugin.
