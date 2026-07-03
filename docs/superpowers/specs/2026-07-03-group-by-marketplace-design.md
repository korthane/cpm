# Group plugins by marketplace — design

Source task: `docs/tasks/group_by_marketplace.txt`.

Four items in one change set:

1. Group the Plugins tab rows by marketplace, with fold/unfold per group.
2. Marketplace rows carry their own actions (add/update/remove per profile)
   and show git commit hash + date as the freshness signal.
3. Bug fix: installing a plugin into a profile that lacks the marketplace
   fails with "marketplace X is not configured there"; instead, add the
   marketplace implicitly before installing.
4. Bug fix: the default `~/.claude` profile shows "not logged in" because on
   macOS Claude Code uses a different Keychain service name when
   `CLAUDE_CONFIG_DIR` is set (even to `~/.claude`) than when it is unset.
   Fall back to an env-stripped auth check for the default profile.

Chosen architecture: grouped model + flattened view list (approach A) —
pure aggregation stays in `internal/model`, fold state and row addressing
stay in `internal/ui`, the index-parallel `comparisonTable` primitive is
untouched.

## 1. Data layer (`internal/claudecli`)

`claude plugin marketplace list --json` returns `name`, `source`
(github/git/directory), `repo`/`url`/`path`, and `installLocation`. There is
no version or last-updated field, so git commit info of the clone at
`installLocation` is the only freshness signal.

- Extend `Marketplace` (currently `{Name, InstallLocation}` in `latest.go`)
  to parse all fields: `Name`, `Source`, `Repo`, `URL`, `Path`,
  `InstallLocation`, plus loader-filled `CommitHash`, `CommitDate`.
- Add `Marketplace.SourceArg() string`: the positional argument for
  `claude plugin marketplace add` — `Repo` for github, `URL` for git,
  `Path` for directory; empty string when none is usable.
- `PluginData` gains `Marketplaces []Marketplace`. `LoadPluginsFresh` and
  `LoadPluginsCached` already call `ListMarketplaces` transiently inside
  `fillFromCatalogFiles`; keep the result instead of discarding it.
- Commit info: run `git -C <installLocation> log -1 --format=%h %cs`
  (abbreviated hash + committer date `YYYY-MM-DD`). This is a read-only
  local exec, not a `claude` invocation, so it does not go through `Runner`;
  it is behind a package-level function var so tests can fake it. It shares
  the load context (and therefore its timeout). Any failure — not a git
  repo, git missing, timeout — degrades to empty `CommitHash`/`CommitDate`,
  never an error.

## 2. Model layer (`internal/model`)

New pure aggregation, no I/O:

```go
type MarketplaceCell struct {
    Configured bool
    CommitHash string // empty when unknown
    CommitDate string // YYYY-MM-DD, empty when unknown
}

type MarketplaceRow struct {
    Name           string
    SourceArg      string // arg for `marketplace add`; empty = cannot add
    SourceConflict bool   // profiles disagree on the source
    Cells          []MarketplaceCell // one per profile, column order
}

type PluginGroup struct {
    Marketplace MarketplaceRow
    Plugins     []PluginRow
}

func BuildPluginGroups(perProfile []claudecli.PluginData, latest map[claudecli.PluginID]string) []PluginGroup
```

- One group per marketplace that is configured in at least one profile
  **or** referenced by an installed/disabled plugin. Orphaned plugins
  (marketplace configured nowhere) still render under their group;
  plugin-less marketplaces still get a row so update/remove work.
- `SourceArg` is resolved across profiles. If two profiles report different
  sources for the same marketplace name, `SourceConflict = true`; add and
  implicit-install are refused for that row.
- Groups sorted by marketplace name; plugins sorted by name within a group.
- Existing `PluginRow`/`PluginCell`/`BuildPluginMatrix` behavior for plugin
  cells is reused; the flat matrix keeps its marketplace-then-name sort, so
  grouping is a partition of contiguous runs.

## 3. UI: grouping, fold, rendering (`internal/ui`)

- `Model` gains `folded map[string]bool` (marketplace name → folded).
  Default: all unfolded. Not persisted across runs.
- A derived `visibleRows []rowRef` with
  `rowRef{kind rowKind, group, plugin int}` (kinds: marketplace, plugin) is
  rebuilt whenever fold state or plugin data changes. `selRow` indexes
  `visibleRows`; rebuilds clamp the selection.
- Rendering keeps the pinned column and every profile column index-aligned,
  one `tableCell` per visible row, so `comparisonTable` is unchanged:

```
 claude-plugins-official      a1b2c3 06-28   a1b2c3 06-15
    superpowers        v6.1.1  v6.1.1         v6.0.0 ↑
    swift-lsp          v1.0.2  v1.0.2         —
 revdiff (2 plugins)          f4e5d6 06-30   —
```

- Marketplace row, pinned column: NerdFont chevron `` (U+F107, unfolded) /
  `` (U+F105, folded) + name; `(n plugins)` suffix when folded. Profile
  cells: `hash date` when configured; `local` for directory sources without
  git info; `—` when not configured.
- Plugin rows: indented two spaces, plugin name **without** `@marketplace`
  (the group header carries it). Latest-version pinned suffix and
  `formatPluginCell` output unchanged.
- `enter`/`space` on a marketplace row toggles its fold; on other rows they
  remain no-ops (outside the existing y/n confirmation flow).
- The second footer help line is selection-dependent: plugin row →
  `e: enable  d: disable  u: update  x: uninstall  i: install`;
  marketplace row → `i: add  u: update  x: remove  enter: fold`.
  Line count is unchanged, so `chrome = 11` in `rowWindow` stays.
- The MCP tab is untouched.

## 4. Actions

Marketplace rows reuse the existing action keys and the existing busy /
confirm / timeout machinery (`startAction`, `pendingAction`,
`actionDoneMsg`, forced column reload on uncertain outcomes):

- `i` → `claude plugin marketplace add <sourceArg> --scope user` on the
  selected profile. Refused when: already configured there,
  `SourceConflict`, `sourceArg` empty, or `sourceArg`/name starts with `-`
  (existing flag-injection guard).
- `u` → `claude plugin marketplace update <name>` on the selected profile.
  Refused when not configured there.
- `x` → y/n confirmation (like plugin uninstall), then
  `claude plugin marketplace remove <name> --scope user`. The `--scope`
  flag is mandatory here: omitting it removes the marketplace from every
  scope (user, project, local), violating cpm's user-scope-only rule.
  (`update` has no scope flag; it refreshes the clone, not config.)
- All marketplace mutations are busy-gated per column like plugin actions;
  a timed-out action is uncertain and forces a column reload.

**Implicit marketplace add on plugin install (bug 3).** The guard in
`startAction` (`app.go:579`, "marketplace %q is not configured there") is
replaced: when the target profile lacks the plugin's marketplace but the
group has a usable `SourceArg`, a single async action runs
`marketplace add <sourceArg> --scope user` and then
`plugin install --scope user <id>` sequentially, updating the status line
between steps (`adding marketplace revdiff…` → `installing…`). If the add
succeeds but the install fails, the marketplace stays added and the column
reload reflects reality. The old refusal message remains only when no
usable source is known (`SourceArg` empty or `SourceConflict`).

## 5. Default-profile auth fallback (bug 4)

On macOS, Claude Code stores OAuth credentials under the Keychain service
`Claude Code-credentials` when `CLAUDE_CONFIG_DIR` is unset, but under
`Claude Code-credentials-<hash>` when it is set — even when set to
`~/.claude`, the same directory the default resolves to. cpm always sets
`CLAUDE_CONFIG_DIR`, so the default profile can look logged-out while the
user is logged in.

- `config.Profile` gains `IsDefault bool`, set during `normalize` when the
  resolved (cleaned, symlink-resolved) path equals the resolved
  `$HOME/.claude`.
- In `loadProfile`/`refreshProfile`: if `LoadAuthStatus` returns
  `LoggedIn == false` with no error and the profile `IsDefault`, re-run
  `LoadAuthStatus` with an empty profile dir. A logged-in second result
  wins; otherwise keep the first result.
- Runner change: when `profileDir == ""`, `Runner.Run` must **strip**
  `CLAUDE_CONFIG_DIR` from the inherited environment. Today it merely does
  not add it, so an ambient value (cpm itself launched with
  `CLAUDE_CONFIG_DIR` exported) would leak through and defeat the fallback.

## 6. Error handling summary

- git info failure → blank commit cells, load continues.
- Marketplace add/update/remove timeout → "uncertain" status + forced
  column reload (existing semantics).
- Implicit add fails → install is not attempted; error in status line.
- Source conflict or unknown source → action refused with an explanatory
  status message; nothing runs.
- Auth fallback runs only for the default profile and only on a clean
  logged-out answer; auth errors keep today's blank-cell behavior.

## 7. Testing

- `claudecli`: parse the extended `Marketplace` fields against the existing
  `testdata/marketplace_list.json`; table test for `SourceArg`; fake the
  git-info function var; `runner_test.go` case asserting
  `CLAUDE_CONFIG_DIR` is stripped when the profile dir is empty.
- `model`: `BuildPluginGroups` tests — grouping, sorting, orphaned plugins,
  plugin-less marketplaces, source conflict, per-profile configured cells.
  Keeps the 80% coverage bar on non-UI packages.
- `ui` (driving `Model.Update` with key/load messages, asserting on
  `View()` and `FakeRunner.Calls`): fold/unfold toggles rendering and row
  count; selection lands on marketplace rows; each marketplace action emits
  the expected CLI args; add-then-install sequence for the install fix
  (replacing `TestInstallBlockedWhenMarketplaceMissingInTarget`); refusals
  for conflict/empty source; context-dependent footer.
- `config`: `IsDefault` detection incl. symlink and trailing-slash cases.
- `ui`/auth: logged-out default profile triggers a second
  `auth status --json` call with empty dir; the logged-in result renders.
