# cpm — developer notes

Terminal UI (Bubble Tea) comparing Claude Code plugins and MCP servers across
profiles (`CLAUDE_CONFIG_DIR` directories). See README.md for user-facing
behavior.

## Commands

- `make build` / `make test` (with `-race`) / `make lint` (golangci-lint) /
  `make run`
- Coverage bar: 80%+ on non-UI packages (`claudecli`, `cli`, `config`,
  `model`).

## Architecture

- `internal/claudecli` — wraps the `claude` CLI behind the `Runner` interface;
  every invocation sets `CLAUDE_CONFIG_DIR` to the target profile. cpm never
  edits Claude's JSON files directly; all mutations go through the CLI. The
  one Claude file it reads itself is `installed_plugins.json` (see below).
- `internal/config` — profile resolution: CLI args > `~/.config/cpm/config.yaml`
  > auto-discovered `~/.claude*` directories.
- `internal/model` — pure aggregation of per-profile CLI data into comparison
  matrices; no I/O. Also the pure halves of change links (`links.go`:
  `GitHubWebURL`, `ChangeLinks`) and changelog excerpts (`changelog.go`:
  `ChangelogExcerpt`); the file read is `claudecli.ReadChangelog`.
- `internal/ui` — Bubble Tea app: one `column` of state per profile, loads run
  async per profile, the MCP tab loads lazily on first view.
- `internal/cli` — non-interactive commands (`outdated`, `refresh`); no Bubble
  Tea imports. `Run` returns the exit code (`ExitOK`/`ExitFailure`/
  `ExitUsage`, shared with `cmd/cpm`) and loads profiles in parallel under
  `loadTimeout` (`claudecli.CommandTimeout`, also the UI's `cmdTimeout`
  default); `outdated` reuses `model.MergeLatestVersions` /
  `IsOutdated` / `ComparePluginIDs` so it agrees with the TUI. A profile
  with `MarketplacesUnknown` is `incomplete` (its own catalogs went unread,
  so its installs met only other profiles' catalogs): stderr error / JSON
  `"incomplete": true`, exit 1, no `all plugins up to date` claim; installs
  found behind another profile's catalog are still listed. Links come from
  `model.LatestSource` + `ChangeLinks`: history once per plugin, compare per
  install from its `CommitSHA`; `--changelog`'s lower bound is the oldest
  install (by `IsOutdated`). JSON `changelog` is three-state: absent without
  the flag (`omitzero` + `changelogJSON.IsZero`), `null` when nothing was
  found, else an object (`MarshalJSON` encodes the `found` pointer).
- `cmd/cpm` — `launcher.run` routes `args[0]` that `cli.IsCommand` accepts to
  `cli.ParseArgs` (usage error → exit 2) + `resolveProfiles` + `cli.Run`,
  before the global `-h` scan so `cpm outdated --help` reaches command help;
  anything else takes the unchanged TUI path (where a bad flag exits 1).

## Testing conventions

- `claudecli.FakeRunner` (in `fake.go`, not a `_test.go` file, so `config` and
  `ui` tests can inject it) returns canned responses keyed by the space-joined
  args and records every call. `ResponsesByDir` (consulted before `Responses`)
  lets a test vary one command's answer per profile dir — needed when the same
  args run against different dirs, e.g. the default-profile auth fallback.
  `Run` is safe for concurrent calls (the CLI loads profiles in parallel);
  read `Calls` only after the calling goroutines have joined.
- Marketplace git lookups are stubbed by swapping the package var
  `gitCommitInfo` (`internal/claudecli/gitinfo.go`, `stubGitCommitInfo`
  helper); tests doing so must not use `t.Parallel()`. Any test whose fake
  marketplace list carries a non-empty `installLocation` must stub it, or the
  load's commit-info pass execs the real `git` against that path. The
  external `claudecli_test` package uses `StubGitCommitInfo`
  (`export_test.go`, so it never ships in the build), which returns a
  restore func for `t.Cleanup`. Other packages cannot stub it: `internal/cli`
  tests keep `installLocation` empty and feed latest versions through canned
  `available` entries.
- `claudecli`, `cli` and `ui` each have a `TestMain` (`main_test.go`) that
  points `HOME` at an empty temp dir: a default-profile (`""`) load reads
  `~/.claude/plugins/installed_plugins.json`, which must never be the
  developer's real file. The `ui` one also replaces `openURL` with a stub
  that counts calls and fails the whole run when any test reached it, so an
  `o` press without `stubOpener` cannot pass silently or open a browser.
  The HOME setup is copied, not shared, to keep it test-only; keep the
  three copies in sync.
- `LatestVersions.Sources` and `InstalledPlugin.CommitSHA` come from files
  and git, not from `FakeRunner`, so link and changelog tests in `cli` (a
  `package cli` test) build `[]profileLoad` values directly; changelog tests
  point `Sources[id].CloneDir` at a temp dir (no git needed).
- Real CLI output is captured as fixtures under `internal/claudecli/testdata/`.
- UI behavior is tested by driving `Model.Update` directly with key/load
  messages and asserting on `View()` output; no TTY needed.
- Styling (reverse video, bold) is invisible in `View()` under `go test`:
  without a TTY lipgloss picks the Ascii profile and strips SGR. Tests
  asserting on styling force the 16-color profile via the `forceANSI` helper
  (`internal/ui/row_highlight_test.go`); the profile is package-global, so
  such tests must not use `t.Parallel()`.

## Non-obvious constraints

- `claude plugin list --available --json`: the `source` field is polymorphic —
  a plain string path or an object whose `ref` may be a branch name, not a
  version.
- `claude plugin list --available --json` leaves *installed* plugins out of
  `available`, so `LoadPluginsCached` seeds every installed ID into the
  version map with `""` and then applies each marketplace's on-disk
  `marketplace.json` — otherwise installed plugins never get a latest
  version. The precedence rule lives in the `LoadPluginsCached` doc comment:
  a plugin's own `.claude-plugin/plugin.json` (relative string `source`
  inside the clone) beats the catalog entry `version`, which beats a
  version-like `source.ref` (`isVersionRef`). The manifest overrides even a
  version from `available` — catalogs bump `plugin.json` and forget the
  entry, and Claude Code itself resolves in-repo plugins from the manifest.
  Known limitation: a plugin whose catalog `source` is remote (a `url`,
  `git-subdir` or `github` object) has no in-clone `plugin.json`; installed,
  it gets a latest version only from an entry `version` or a version-like
  `source.ref`, and without either it is never reported outdated.
- Marketplace clones are third-party git content, so catalog and manifest
  reads go through `os.OpenRoot(installLocation)` (`readConfinedFile`): paths
  and symlinks escaping the clone are refused, only regular files are read
  (`Stat` before `Open` — opening a FIFO blocks), capped at 1 MiB. These
  reads take no ctx, so they must never block. `ReadChangelog` (plugin-dir
  `CHANGELOG.md`, then the clone root) goes through the same path.
- `plugin list --json` reports `installPath` but no commit, so
  `LoadPluginsCached` fills `InstalledPlugin.CommitSHA` from
  `<profile>/plugins/installed_plugins.json` (`installed_commits.go`;
  `""` profile → `~/.claude`). Read-only and best-effort: any failure leaves
  SHAs empty and only drops links. Only `"version": 2` is understood. The
  read reuses `readConfinedFile` under `os.OpenRoot(<profile>/plugins)`. A
  record counts only within the install's scope (a project install must not
  take a user record's SHA at the same path): records at the same install
  path (symlinks resolved) yield the SHA their non-empty values agree on —
  a SHA-less record hides nothing, conflicting SHAs give `""` — else the
  only record of that id and scope.
- `LatestVersions.Sources` says where each latest version lives
  (`PluginSource{RepoURL, Commit, Path, CloneDir}`). `setLatest` writes the
  version and the source of the *same* entry, so the source always follows
  whichever rule won the version (manifest / entry / ref); an empty version
  records none. Relative sources always keep `Path` + `CloneDir` (the
  changelog needs no git) and set `RepoURL` + `Commit` (marketplace repo,
  clone HEAD = `Marketplace.HeadSHA`, full SHA from `%H %cs`) only as a
  pair. Remote sources carry their repo, `sha` (else `ref`, best-effort: a
  branch can move) and `path`, no `CloneDir`. A relative source loaded while
  the marketplace list failed has `Path` only (clone not locatable), so
  `--changelog` reports it as `no local changelog`, like a remote one.
  `Marketplace` keeps only the full `HeadSHA`; the 7-char form is cut in
  `model.BuildPluginGroups` for display.
- `model.LatestSource` picks the source of a profile whose own version
  equals the merged latest (version compare, not string equality), so link
  and version agree; ties prefer a source that can build links (a GitHub
  `RepoURL` and a valid `Commit`, the `ChangeLinks` rules), then a
  non-stale profile, then profile order. `--changelog` reads from
  `model.ChangelogSource` instead (same rules, but a source with a
  `CloneDir` wins the tie), so an equal-version remote source picked for
  links cannot hide another profile's readable clone.
  Matrix/group builders keep their signatures — the UI calls it for the
  selected row only.
- Link parts are third-party data and the URL may reach the system opener,
  so `model/links.go` validates everything: host exactly `github.com`
  (case-insensitive; no userinfo but the ssh forms' `git@`, no port, query
  or fragment), owner/repo `[A-Za-z0-9._-]+` and not `.`/`..`, a commit
  is a hex SHA or slash-free ref without `..` (a `/` would escape to `%2F`,
  dots break `a...b`);
  a `refs/tags/` / `refs/heads/` prefix is stripped first. Path segments
  allow any characters but are each `url.PathEscape`d (`%2e%2e` cannot
  traverse); absolute paths and literal `..` segments are refused, empty and
  `.` segments dropped; the repo root gives no history link. No compare
  link when installed and latest commits are the same (short SHA prefix
  counts). Known limitation (README): both commits are assumed to be in the
  same repo — a fork in another profile or a plugin moved between in-clone
  and remote yields a compare link GitHub cannot resolve.
- `ChangelogExcerpt` headings: ATX `#`–`###` (≤3 spaces indent, then a
  space) whose first word is a version (*plain*) or a name then a version
  (*scoped*; the name is cut at `@`, and `version`/`release`/`v` count as
  plain); `[1.2.0]` / `[1.2.0](link)` are unwrapped. `Unreleased` in the
  version slot is a boundary only: never collected, never makes the file
  scoped. *Every* version
  heading ends the section above it, but only this plugin's are collected:
  scoped ones (name case-insensitive) if the file has any, else plain ones —
  so an interleaved multi-plugin file never leaks another plugin's body.
  Every collected section with version in (installed, latest] is taken,
  regardless of file order, and sorted newest first (stable), so
  oldest-first files work and truncation keeps the newest. No latest
  heading → `ok=false`. `ReadChangelog` falls back to the clone-root file
  for a subdirectory plugin; with plain headings that file may version
  something else (README known limitation). Lines inside ``` / ~~~ fences
  are never headings; a ``` line whose info string contains a backtick is
  an inline code span, not a fence (CommonMark). Capped at 200 lines.
- The TUI opener (`internal/ui/open.go`: `openURL` is the only test hook,
  `openWith` the testable core) refuses any URL not starting with
  `https://github.com/` — on macOS `open` also launches files and apps —
  and `Start`s `open`/`xdg-open` without a shell, stdio detached so it
  cannot draw over the screen. It waits up to 5s for an exit status; an
  opener still running then counts as success and is reaped in the
  background (`xdg-open` may block until the browser exits; killing it
  would report a failure for a page that opened). The `changes:` status
  text renders in the existing status slot, so `chromeLines` is unchanged;
  `View` computes the link once and width-caps both help lines, which
  `chromeLines` budgets one row each. The `o: open changes` hint is hidden
  while a confirmation is pending: `o` would answer the prompt.
- `claude mcp list` has no `--json` mode and health-checks every server, so it
  is slow — hence the lazy MCP tab and tab-scoped reload. Its output includes
  project/local-scope servers (cwd-dependent) and plugin-provided servers
  (`plugin:<plugin>:<name>`), which `claude mcp remove` cannot remove.
- `claude auth status --json` may exit non-zero for logged-out profiles while
  still printing valid JSON; parseable object output wins over the exit code.
- Every UI-fired CLI call carries a timeout (`cmdTimeout` in
  `internal/ui/app.go`) so a hung `claude` degrades to a per-column error. The
  marketplace refresh gets its own 30s sub-budget (`refreshTimeout`, applied
  by `LoadPluginsFresh` in `internal/claudecli/latest.go`) so a hung git
  remote degrades to a stale catalog instead of eating the whole load
  budget. `RefreshMarketplaces` itself is uncapped: `cpm refresh` exists to
  run the update, so it gets the full per-profile `loadTimeout`. A
  timed-out *action* is "uncertain" — the write may have partially
  applied — and forces a column reload.
- Killing a timed-out `claude` is not enough: children it spawned (stdio MCP
  servers from `mcp list`, git from `marketplace update`) inherit the output
  pipes and keep `cmd.Run` blocked past the timeout. The runner starts each
  command in its own process group and SIGKILLs the group on cancel
  (`runner_unix.go`; `runner_other.go` is a no-op), with `WaitDelay` as the
  pipe-closing backstop.
- Only one writer per config dir at a time: a fresh profile load runs
  `plugin marketplace update` (a write), so action keys are busy-gated, reload
  skips busy/loading columns, and MCP remove is blocked during a plugin load.
  Generation stamps on load messages only drop superseded results — they
  cannot cancel an in-flight process.
  This holds within one process only: a CLI `refresh` (or `outdated
  --refresh`) is not coordinated with a TUI or `claude` on the same profile —
  documented, not locked.
- All mutations pin `--scope user`: the CLI auto-detects scope otherwise, so
  acting on a project/local-scope row (cwd-dependent, identical in every
  column) would edit config shared by all profiles. The UI additionally
  refuses plugin actions on cells whose reported scope is not `user`.
- Plugin IDs and MCP server names are third-party data passed to `claude` as
  positional args; the UI refuses names starting with `-` so they cannot be
  parsed as CLI flags.
- `claude plugin marketplace remove` without `--scope user` removes the
  marketplace from ALL scopes, not just the profile's config — cpm always
  passes it (`add` pins it too; `marketplace update` has no scope flag).
- Marketplaces have no version field, so the freshness signal is the commit
  hash/date of the clone, read by direct `git -C <installLocation> log -1`
  (not through Runner) during load. Git failure → blank cells so the UI can
  tell "unknown" from a git-less directory source (shown as `local`). The
  lookup sets `GIT_CEILING_DIRECTORIES` so a directory-source marketplace
  nested inside a larger repo does not report the enclosing repo's HEAD.
- A failed `plugin marketplace list` never fails the load, but it leaves the
  profile's configured set unknown (`PluginData.MarketplacesUnknown`), which
  is not the same as "none configured": the UI renders blank header cells
  instead of `—` and refuses marketplace actions and implicit adds there —
  a blind `marketplace add` could duplicate an existing marketplace.
- macOS Keychain namespaces `claude` credentials by whether
  `CLAUDE_CONFIG_DIR` was set at login, so the default `~/.claude` profile
  can read as logged-out under cpm even though a plain `claude` login is
  active. `loadAuth` (`internal/ui/app.go`) re-checks a clean logged-out
  answer for `IsDefault` profiles with an empty profile dir — the runner
  strips the ambient env var when the dir is empty — and a clean logged-in
  fallback wins.
- The `/` name filter is applied inside the `pluginGroups`/`mcpRows` accessors
  (`internal/ui/app.go`), so every consumer — view, row count, folding,
  selection, actions — sees the same filtered set through one choke point. Two
  consequences: a filter forces the fold map to `nil` (`activeFolds`), because
  a folded group would otherwise hide matches, and the indicator's match-count
  denominator must come from the *unfiltered* `allPluginGroups`/`allMCPRows`.
  Both sides of that count are matchable *entries* — plugins plus marketplaces
  (`model.CountPluginMatches`/`CountPluginEntries`, `internal/model/filter.go`)
  — not rows: a header kept because its group holds a matching plugin is not
  itself a match, and counting rows would report `(2/5)` where one plugin of
  three matched. Marketplaces are entries, not chrome: a marketplace name is
  matchable and a plugin-less marketplace whose name matches renders as an
  actionable header row, so counting plugins alone would report `(0/N)` beside
  a visible result. A marketplace-name match counts its whole group, matching
  what `FilterPluginGroups` keeps. The one marketplace that is not an entry is
  the synthetic empty-named group `BuildPluginGroups` makes for plugin IDs with
  no `@marketplace`: an empty name can never match, so counting it would report
  `(1/2)` for a lone bare plugin. The no-match empty state still gates on the
  *row* total, so a profile with marketplaces but no plugins installed still
  has rows for the query to exclude.
  Matching is `sahilm/fuzzy`'s order-preserving `FindNoSort` — plain `Find`
  sorts by score and would re-rank rows under a grouped table. The query is
  trimmed (`model.NormalizeQuery`, `internal/model/filter.go`): fuzzy treats a
  space as a literal rune to find, and no name contains one, so an accidental
  trailing space would empty the table and read as an over-narrow filter. The
  UI stores the query already normalized (`setQuery`), not just matching on the
  normalized form: every "is a filter active" test keys off the stored query
  being non-empty, so a whitespace-only query would otherwise be active to the
  UI and empty to the filters — suspending folds and drawing an indicator for a
  filter that hides nothing.
- Because `activeFolds` is `nil` under a filter, `toggleFold` is a no-op there
  (and the help line drops `enter: fold`): a fold recorded while filtering
  would be invisible until the filter is cleared, and would then swallow rows
  the user never folded.
- The same `nil` fold map means a filtered marketplace header renders through
  the *unfolded* branch of `pinnedRowText`, which would make a group narrowed
  from five plugins to one look like a marketplace holding one plugin — while
  `x` on that header still removes the whole marketplace, hidden plugins
  included. So `FilterPluginGroups` records what it dropped per group
  (`PluginGroup.HiddenPlugins`) and the header renders it as `(+N hidden)`,
  the filter's analog of the count a folded header shows. A marketplace-name
  match keeps its group whole, so it hides nothing and the marker is absent.
- The `no plugins match` / `no MCP servers match` empty state (`noMatchLine`) is
  gated on the *unfiltered* row set being non-empty **and** on no column still
  *loading* (`tabLoading`, not the spinner-scoped `anyLoading`). Loading columns
  are skipped by `allPluginGroups`/`allMCPRows`, so they produce zero rows too —
  blaming that on the query would report "no matches" over a table of spinners.
  The row-set check alone is not enough: with one column loaded and another
  still loading, the total is non-zero, so a no-match query would claim the
  filter emptied a table that is merely not filled in yet.
  The gate is on *loading*, not on `statusLoaded` for every column: an errored
  column contributes no rows either, but its state is settled, and waiting on it
  would suppress the empty state and the counts until that profile loads — which
  it may never do, leaving an over-narrow query to render as a silent empty
  table for good. So the line is drawn *below* the table instead of in place of
  it: the table carries the per-column spinners and `error:` lines, which an
  errored profile still needs while its rows are gone. It costs no body row —
  it only ever appears when the filtered row count is zero. The indicator's
  `(match/total)` counts share the same gate: built from the loaded columns
  only, mid-reload both sides are zero and `filter: gam (0/0)` would tell the
  user their query matched nothing over a table of spinners. `filterLine` drops
  the parenthetical until no column is loading, keeping the query visible so the
  filter is never silent. The key that clears it is advertised in the help line
  (`esc: clear filter`, only while a query is applied), not in the indicator:
  users look for keys where every other key hint already lives.
- `rowWindow` (`internal/ui/app.go`) sizes the scroll window as
  `height - chromeLines()`, where `chromeLines` is the count of non-body lines.
  It is not a constant: the filter line adds one, and focusing the filter input
  removes one (the action help line is suppressed in that mode). Getting it
  wrong does not fail loudly — it silently scrolls header chrome off-screen.
- Outdated flags use a custom segment-wise numeric version compare in
  `internal/model` (leading `v` ignored, missing segment = 0, pre-release <
  release, empty never outdated, lexical fallback for non-numeric segments) —
  not a semver library, which would reject real-world refs like `1.2.3.4`.
  `model.IsOutdated` is the single "outdated" rule (matrix cells and `cpm
  outdated`); it treats a commit-hash side (hex with a letter) as unknown,
  because `0a1b2c3d` would otherwise compare as `0.<suffix>` and read as
  behind every release. `MergeLatestVersions` drops hash latests for the
  same reason: a hash sorts lexically above any release, so it would
  displace a real version from another profile and mask the upgrade.
