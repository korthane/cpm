# cpm — developer notes

Terminal UI (Bubble Tea) comparing Claude Code plugins and MCP servers across
profiles (`CLAUDE_CONFIG_DIR` directories). See README.md for user-facing
behavior.

## Commands

- `make build` / `make test` / `make lint` (golangci-lint) / `make run`
- Coverage bar: 80%+ on non-UI packages (`claudecli`, `config`, `model`).

## Architecture

- `internal/claudecli` — wraps the `claude` CLI behind the `Runner` interface;
  every invocation sets `CLAUDE_CONFIG_DIR` to the target profile. cpm never
  edits Claude's JSON files directly; all mutations go through the CLI.
- `internal/config` — profile resolution: CLI args > `~/.config/cpm/config.yaml`
  > auto-discovered `~/.claude*` directories.
- `internal/model` — pure aggregation of per-profile CLI data into comparison
  matrices; no I/O.
- `internal/ui` — Bubble Tea app: one `column` of state per profile, loads run
  async per profile, the MCP tab loads lazily on first view.

## Testing conventions

- `claudecli.FakeRunner` (in `fake.go`, not a `_test.go` file, so `config` and
  `ui` tests can inject it) returns canned responses keyed by the space-joined
  args and records every call. `ResponsesByDir` (consulted before `Responses`)
  lets a test vary one command's answer per profile dir — needed when the same
  args run against different dirs, e.g. the default-profile auth fallback.
- Marketplace git lookups are stubbed by swapping the package var
  `gitCommitInfo` (`internal/claudecli/gitinfo.go`, `stubGitCommitInfo`
  helper); tests doing so must not use `t.Parallel()`. Any test whose fake
  marketplace list carries a non-empty `installLocation` must stub it, or the
  load's commit-info pass execs the real `git` against that path.
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
- `claude mcp list` has no `--json` mode and health-checks every server, so it
  is slow — hence the lazy MCP tab and tab-scoped reload. Its output includes
  project/local-scope servers (cwd-dependent) and plugin-provided servers
  (`plugin:<plugin>:<name>`), which `claude mcp remove` cannot remove.
- `claude auth status --json` may exit non-zero for logged-out profiles while
  still printing valid JSON; parseable object output wins over the exit code.
- Every UI-fired CLI call carries a timeout (`cmdTimeout` in
  `internal/ui/app.go`) so a hung `claude` degrades to a per-column error. The
  marketplace refresh gets its own 30s sub-budget (`refreshTimeout` in
  `internal/claudecli/latest.go`) so a hung git remote degrades to a stale
  catalog instead of eating the whole load budget. A timed-out *action* is
  "uncertain" — the write may have partially applied — and forces a column
  reload.
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
  Both sides of that count are plugins (`countPlugins`), not rows: the plugins
  tab emits a row per marketplace header, and a header kept because its group
  holds a match is not itself a match — counting rows would report `(2/5)`
  where one plugin of three matched. The no-match empty state still gates on
  the *row* total, so a profile with marketplaces but no plugins installed
  still has rows for the query to exclude.
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
- The `no plugins match` / `no MCP servers match` empty state is gated on the
  *unfiltered* row set being non-empty **and** on every column having loaded
  (`allLoaded`). Loading and errored columns are skipped by
  `allPluginGroups`/`allMCPRows`, so they produce zero rows too — blaming that
  on the query would replace the table (and with it the per-column spinners and
  `error:` lines) with a false "no matches". The row-set check alone is not
  enough: with one column loaded and another still loading or errored, the
  total is non-zero, so a no-match query would hide the other column's spinner
  and its `error:` line for as long as the filter is applied.
- `rowWindow` (`internal/ui/app.go`) sizes the scroll window as
  `height - chromeLines()`, where `chromeLines` is the count of non-body lines.
  It is not a constant: the filter line adds one, and focusing the filter input
  removes one (the action help line is suppressed in that mode). Getting it
  wrong does not fail loudly — it silently scrolls header chrome off-screen.
- Outdated flags use a custom segment-wise numeric version compare in
  `internal/model` (leading `v` ignored, missing segment = 0, pre-release <
  release, empty never outdated, lexical fallback for non-numeric segments) —
  not a semver library, which would reject real-world refs like `1.2.3.4`.
