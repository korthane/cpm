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
cpm never edits Claude's internal JSON files directly. The one file it reads
itself is `<profile>/plugins/installed_plugins.json`, for the commit each
plugin was installed from (the CLI does not report it); the read is
best-effort, and without it change links are simply left out.

## Requirements

- The `claude` CLI must be on `PATH` — every read and action shells out to it.
- `git` on `PATH` is optional but recommended: marketplace freshness (commit
  hash and date) is read from each clone with `git log`; without it those
  header cells stay blank, and plugins stored in the clone get no change
  links.
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
cpm outdated                     # non-interactive: see Command-line mode
```

Without a command, cpm takes no flags other than `-h`/`--help`; any other
dashed argument is rejected as a typo rather than treated as a profile
directory.

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
that matches nothing keeps the table and its column headers in place and adds a
`no plugins match "…"` (or `no MCP servers match "…"`) line below it, so an
over-narrow filter never reads as an empty profile while a still-loading or
errored profile keeps its spinner or `error:` line.

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
above the table shows the query and the match count, and the help line gains
`esc: clear filter`, so an active filter is never invisible. Each tab keeps its
own query, so switching tabs does not disturb the other's filter.

Plugins tab, on a plugin row, applied to the selected cell:

| Key | Action |
| --- | --- |
| `e` | enable (disabled plugin) |
| `d` | disable (enabled plugin) |
| `u` | update (installed plugin) |
| `x` | uninstall (installed plugin; asks `y/n`) |
| `i` | install into a profile where the plugin is absent |
| `o` | open the change link of an outdated install in the browser |

When the selected cell is an outdated install and cpm can build a link for
it, the status line below the table shows `changes: <url>` — a GitHub
compare view from the installed commit to the latest one, or, when the
installed commit is unknown, the plugin's commit history (see
[Change links](#change-links)). `o` opens exactly that URL with the system
opener (`open` on macOS, `xdg-open` elsewhere) and is listed in the help line
whenever the selected cell has a link. A status message takes the status
line first, hiding the link but not the key; a pending confirmation hides
both, since any key but `y` answers it with "no". cpm
opens only `https://github.com/` URLs; an opener still running after 5
seconds (`xdg-open` may wait for the browser to exit) counts as success.

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

## Command-line mode

A command as the first argument makes cpm print a result and exit instead of
starting the TUI — for scripts, agents and quick checks:

```sh
cpm outdated [--refresh] [--changelog] [--text|--json] [<profile-dir> ...]
cpm refresh  [--text|--json] [<profile-dir> ...]
cpm <command> --help
```

Profiles resolve exactly as for the TUI (args > config file >
auto-discovery). Flags and profile dirs may be interleaved; `--text` (the
default) and `--json` are mutually exclusive. A profile dir named like a
command is reached as `./outdated`. Profiles load in parallel, each bounded
by the same two-minute timeout as the TUI, and one failing profile does not
stop the others. Within that budget the marketplace update run by
`outdated --refresh` is capped at 30 seconds, after which the cached catalog
is used; `cpm refresh` gives the update the full two minutes.

### `cpm outdated`

Lists every installed plugin whose version is behind the newest version
found in any profile's marketplace catalog, with the profiles holding the
old version. It reads the cached catalogs; for a plugin stored inside the
marketplace clone, its own `plugin.json` version wins over the catalog entry
(as in Claude Code), since catalogs often lag it. `--refresh`
runs `claude plugin marketplace update` in each profile first (if that fails,
the cached catalog is used: text mode prints a warning to stderr, JSON
reports `"refresh": "failed"`). Every installed entry is
checked, so a plugin installed at both user and project scope in one profile
is reported if either install is behind. Installs whose version the CLI
reports as `unknown`, or as a commit hash, are never reported — there is no
release version to compare; a commit hash in a catalog never counts as the
latest version either. Neither are installed plugins whose catalog entry
points outside the marketplace clone (a remote `url`, `git-subdir` or
GitHub source) and carries neither a `version` nor a version-like `ref`:
with no `plugin.json` in the clone, no latest version is known for them.

```text
$ cpm outdated
bar@acme  latest 6.4.1
  work  6.3.0   (disabled)
    changes: https://github.com/acme/plugins/compare/1a2b3c4...5d6e7f8
  history: https://github.com/acme/plugins/commits/5d6e7f8/plugins/bar
foo@acme  latest 0.35.1
  home  0.34.0
  work  0.34.0
    changes: https://github.com/acme/foo/compare/9a8b7c6...0f1e2d3
```

(Commit SHAs are shortened in these samples; cpm prints them as recorded,
usually in full.)

Plugins are sorted by marketplace, then name; installs follow profile order.
Profiles are shown by their config.yaml label, or by the directory name
(e.g. `.claude-work`) when none is set; two profiles sharing a label are
shown by path instead. An install at a non-`user` scope is marked
`(scope: project)`. With nothing outdated it
prints `all plugins up to date` — unless a profile failed to load or could
not be fully checked, in which case stdout stays empty, since nothing proves
that profile is current.

If a profile's `claude plugin marketplace list` fails, its catalog files
cannot be located, so its installed plugins (which the CLI's `available`
list leaves out) are checked only against other profiles' catalogs. Text
mode reports `error: work: marketplace list failed; its catalogs were not
read, so outdated plugins may be missed` on stderr, JSON marks the profile
`"incomplete": true`, and the exit code is `1`. Outdated installs that were
found — in that profile too — are still listed.

#### Change links

Each install can be followed by an indented `changes:` line: a GitHub
compare page from the commit that install came from to the commit the latest
version comes from, i.e. exactly what an update would bring in. A plugin
living in a subdirectory of its repository (a multi-plugin marketplace)
also gets one `history:` line after its installs: the commit history of
that directory at the latest commit. A compare page cannot be narrowed to a
path, so in a shared repository it also lists other plugins' commits; the
history link shows only this plugin's. A line is left out when its link
cannot be built — e.g. the installed commit is unknown (some installs, such
as older ones or directory sources, record none), or the two commits are
the same.

Links are built offline from data already on disk:

- The installed commit comes from the profile's
  `plugins/installed_plugins.json`.
- For a plugin stored in the marketplace clone, the latest commit is the
  clone's `HEAD` and the repository is the marketplace's GitHub repo.
- For a plugin whose catalog entry points elsewhere (a remote `url`,
  `git-subdir` or GitHub source), they come from that entry: its pinned
  `sha`, else its `ref`. A `ref` is best-effort — a branch can move after
  the catalog was fetched — so such links are exact only when both ends are
  pinned commits.
- The latest commit is taken from the same profile that supplied the
  latest version, so link and version always agree. When several profiles
  have it, one whose source names both a repository and a commit is
  preferred, then one whose catalog refresh did not fail.
- A `refs/tags/` or `refs/heads/` prefix on a `ref` is dropped; a ref with
  any other `/` (e.g. `release/1.x`) gets no links.

Only GitHub repositories get links. Marketplaces added from a local
directory, clones without git, and repositories hosted anywhere else get
none; neither do plugins whose version is a commit hash, since they are
never reported outdated.

Known limitation: the installed commit and the latest one are assumed to
live in the same repository. If profiles point a same-named marketplace at
different forks, or a plugin moved between its marketplace clone and a
remote source, the compare link pairs commits from two repositories and
GitHub shows an error page; the history link is unaffected.

#### `--changelog`

`--changelog` adds, once per plugin, the entries of its `CHANGELOG.md` from
the latest version (inclusive) down to the oldest version installed in any
profile (exclusive), so every install's missing entries are covered:

```text
$ cpm outdated --changelog
foo@acme  latest 0.35.1
  home  0.34.0
  work  0.34.0
    changes: https://github.com/acme/foo/compare/9a8b7c6...0f1e2d3
  changelog (CHANGELOG.md):
    ## v0.35.1 - 2026-01-01
    - Fix the widget refresh.

    ## v0.35.0 - 2025-12-15
    - Add the widget panel.
```

The file is read from the local marketplace clone — the plugin's own
directory first, then the clone root — and its path is shown in the header
line. When several profiles have the latest version, one with a local clone
is read even if the links come from another profile's remote source. Version headings such as `## 1.2.0`, `## v1.2.0 - date`,
`## [1.2.0]`, `## [1.2.0](link)`, `## Version 1.2.0` and `## Release 1.2.0`
are recognized; in a changelog shared by several plugins, headings prefixed
with the plugin name (`## foo v0.35.1`, or `## foo@acme v0.35.1`) select
that plugin's sections only. Sections are shown newest first whatever the
file's order, so an oldest-first changelog works too. Headings inside code
blocks are ignored and long excerpts are cut at 200 lines.

Known limitation: a plugin in a subdirectory without its own `CHANGELOG.md`
falls back to the clone-root file. If that file versions something else
with plain headings and one of them happens to equal the plugin's latest
version, that entry is shown as the plugin's; the header line names the
file, so the source stays visible.

When there is nothing to show, a single line says why:

- `changelog: no entry for 0.35.1` — the file has no heading for the latest
  version (it may track something else, e.g. the app rather than the
  plugin);
- `changelog: no CHANGELOG.md` — the clone has no readable one (missing,
  not a regular file, a link out of the clone, or over 1 MiB);
- `changelog: no local changelog` — the plugin lives outside the
  marketplace clone, or cpm could not locate the clone the latest version
  came from (that profile's marketplace list failed, or no profile said
  where the latest version lives), so there is nothing local to read.

The changelog is informational and never changes the exit code. Only
`outdated` accepts `--changelog`; `cpm refresh --changelog` is a usage
error.

```sh
$ cpm outdated --json
```

```json
{
  "profiles": [
    {"label": "home", "path": "/home/me/.claude", "refresh": "skipped",
     "error": "", "incomplete": false},
    {"label": "work", "path": "/home/me/.claude-work", "refresh": "skipped",
     "error": "", "incomplete": false}
  ],
  "outdated": [
    {"plugin": "foo@acme", "latest": "0.35.1",
     "installs": [
       {"label": "home", "path": "/home/me/.claude", "version": "0.34.0",
        "scope": "user", "enabled": true,
        "compare_url": "https://github.com/acme/foo/compare/9a8b7c6...0f1e2d3"}
     ],
     "history_url": ""}
  ]
}
```

(Pretty-printed here; the actual output is one line.) `outdated` is always
an array, never `null`. `refresh` is `skipped` (no `--refresh`, or the
profile failed to load), `ok`, or `failed` (stale catalog used); `error` is
empty unless the profile failed to load; `incomplete` is `true` when the
profile loaded but its marketplace list failed, so its installed plugins
were checked only against other profiles' catalogs. `compare_url` (per
install) and `history_url` (per plugin) are the links described above, `""`
when unknown. With `--changelog` each plugin also carries `"changelog":
{"file": "CHANGELOG.md", "since": "0.34.0", "text": "## v0.35.1 …"}` —
`file` is the path inside the clone, `since` the exclusive lower bound,
`text` the raw excerpt — or `"changelog": null` in any of the three
nothing-to-show cases; without the flag the key is absent.

JSON is the stable interface for scripts: new data is only ever added as new
fields. The text layout is for people and may gain lines — such as the indented
`changes:`, `history:` and `changelog` lines — that a line-parsing script
could trip over.

### `cpm refresh`

Runs `claude plugin marketplace update` in every profile and reports each
result:

```text
$ cpm refresh
home  ok
work  ok
```

A failed profile goes to stderr as `error: work: <message>`. With `--json`:
`{"profiles":[{"label":"home","path":"/home/me/.claude","error":""}, ...]}`.

A refresh writes to the profile's marketplace clones and is not coordinated
with other processes: avoid running it (or `outdated --refresh`) while a cpm
TUI or `claude` is working on the same profile.

### Streams and exit codes

Results go to stdout. In text mode errors and warnings go to stderr, and a
value holding control characters (a plugin ID, version, label, error
message, link, changelog file name or changelog line) is printed Go-quoted
so it cannot forge lines; tabs in changelog lines are expanded to four
spaces instead, so indented Markdown stays readable. In JSON
mode command results and per-profile errors are carried inside the JSON and
stderr stays empty; usage errors, profile-resolution errors and a failed
stdout write are still plain stderr text.

| Code | Meaning |
| --- | --- |
| `0` | success — including when outdated plugins are found, and when a `--refresh` failed but the cached catalog was used |
| `1` | a profile failed to load (for `cpm refresh`: failed to refresh) or, for `cpm outdated`, could not be fully checked (`incomplete`), profiles could not be resolved (none found, a path that is not a directory, a malformed `config.yaml`, an unresolvable `$HOME`), or stdout could not be written |
| `2` | usage error in a command: unknown flag (`cpm outdated --bogus`), `--text` with `--json`, a profile dir starting with `-` |

Without a command, a bad argument such as `cpm --bogus` keeps the TUI
launcher's behavior and exits `1` — so does a flag placed before the command
(`cpm --json outdated`), which is not a command line.
