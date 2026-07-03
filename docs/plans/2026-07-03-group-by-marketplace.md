# Group plugins by marketplace

## Overview

Implements the approved spec
`docs/superpowers/specs/2026-07-03-group-by-marketplace-design.md`. Four items:

1. Group the Plugins tab rows by marketplace with per-group fold/unfold
   (NerdFont chevrons `` U+F107 unfolded / `` U+F105 folded).
2. Marketplace rows carry actions (`i` add, `u` update, `x` remove with
   confirmation) and show git commit hash + date per profile as the
   freshness signal (marketplaces have no version field).
3. Bug fix: installing a plugin into a profile that lacks its marketplace
   now adds the marketplace implicitly (when a usable source is known)
   instead of refusing.
4. Bug fix: the default `~/.claude` profile can show "not logged in"
   because macOS Keychain uses a different service name when
   `CLAUDE_CONFIG_DIR` is set vs unset; fall back to an env-stripped
   auth check for the default profile.

Architecture (decided in spec): pure aggregation (`PluginGroup`,
`MarketplaceRow`) in `internal/model`; fold state and row addressing
(`visibleRows []rowRef`) in `internal/ui`; the index-parallel
`comparisonTable` primitive in `internal/ui/table.go` stays untouched.

## Context (from discovery)

- `internal/claudecli/latest.go` — `Marketplace{Name, InstallLocation}`
  parses `plugin marketplace list --json` but discards `source`, `repo`,
  `url`, `path`; `ListMarketplaces` result is used transiently in
  `fillFromCatalogFiles` and thrown away.
- `internal/claudecli/runner.go:73-75` — sets `CLAUDE_CONFIG_DIR` only when
  profileDir non-empty; does NOT strip an ambient value when empty.
- `internal/config/profile.go` — `Profile{Path, Label}`, `normalize`
  cleans/symlink-resolves paths; no notion of a default profile.
- `internal/model/matrix.go` — `PluginRow`/`PluginCell`,
  `BuildPluginMatrix` sorts by marketplace then name (groups are already
  contiguous runs).
- `internal/ui/app.go` — `viewPlugins`/`pinnedPluginColumn`/`pluginColumn`
  render index-aligned columns; `selRow`/`selCol` are plain ints;
  `startAction` holds the "marketplace %q is not configured there" guard
  at ~line 579 (`hasAvailable`); `actionVerbs` maps keys; footer help is
  hardcoded in `View()`; `rowWindow` has `chrome = 11`.
- Tests: `claudecli.FakeRunner` keyed by space-joined args, records calls
  (incl. profile dir); fixtures in `internal/claudecli/testdata/`
  (`marketplace_list.json` already has all source variants); UI tested by
  driving `Model.Update` (`modelWithCells`, `press` helpers in
  `actions_test.go`); `TestInstallBlockedWhenMarketplaceMissingInTarget`
  pins the behavior task 9 replaces.
- Go 1.26: use modern idioms (`slices`, `maps`, `cmp.Or`, `t.Context()`,
  `for i := range n`, `errors.AsType`).

## Development Approach

- **Testing approach**: TDD — write failing tests first, then implement.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes
  in that task — tests are a required deliverable, success and error cases,
  listed as separate checklist items.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run tests after each change (`make test`), lint with `make lint`.
- Maintain backward compatibility: MCP tab behavior, existing plugin action
  semantics, busy-gating, timeout-uncertain-reload semantics all unchanged.
- Project constraints (CLAUDE.md): all mutations go through the `claude`
  CLI with `--scope user`; third-party names must not start with `-`;
  every UI-fired CLI call carries a timeout; 80%+ coverage on non-UI
  packages (`claudecli`, `config`, `model`).

## Testing Strategy

- **Unit tests**: required for every task (see Development Approach).
- No e2e framework in this project; UI behavior is verified by driving
  `Model.Update` with key/load messages and asserting on `View()` output
  and `FakeRunner.Calls` — treat these with the same rigor as unit tests.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.
- Keep plan in sync with actual work done.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs in this repo.
- **Post-Completion** (no checkboxes): manual verification on a real
  multi-profile setup.

## Implementation Steps

### Task 1: Parse full marketplace JSON and add SourceArg

- [x] write failing tests: `ListMarketplaces` against
      `testdata/marketplace_list.json` asserting `Source`, `Repo`, `URL`,
      `Path`, `InstallLocation` for the github/git/directory entries
- [x] write failing table test for `Marketplace.SourceArg()`: github→Repo,
      git→URL, directory→Path, unknown source or missing field→""
- [x] extend `Marketplace` struct in `internal/claudecli/latest.go` with
      `Source`, `Repo`, `URL`, `Path` JSON fields plus non-JSON
      `CommitHash`, `CommitDate` fields
- [x] implement `SourceArg()`
- [x] run tests — must pass before task 2

### Task 2: Marketplace git commit info and Marketplaces in PluginData

- [x] write failing tests for git-info fill: injectable function var
      (e.g. `gitCommitInfo func(ctx, dir) (hash, date string, err error)`)
      faked in tests; failure → empty fields, no error propagation
- [x] write failing tests: `LoadPluginsFresh` and `LoadPluginsCached`
      return `PluginData.Marketplaces` populated from
      `marketplace list --json` (extend existing FakeRunner-based tests)
- [x] add `Marketplaces []claudecli.Marketplace` to `PluginData`
      (`internal/claudecli/plugins.go`); keep the `ListMarketplaces`
      result currently discarded inside `fillFromCatalogFiles`
      (`internal/claudecli/latest.go`)
- [x] implement real `gitCommitInfo` via
      `git -C <installLocation> log -1 --format=%h %cs` (direct exec, not
      Runner; honors the load context/timeout; any failure → blank fields)
- [x] fill `CommitHash`/`CommitDate` on each marketplace during load
- [x] run tests — must pass before task 3

### Task 3: Runner strips ambient CLAUDE_CONFIG_DIR on empty profile dir

- [x] write failing test in `internal/claudecli/runner_test.go`: with
      `CLAUDE_CONFIG_DIR` in the parent env and `profileDir == ""`, the
      child env must NOT contain `CLAUDE_CONFIG_DIR` (today it leaks)
- [x] implement: in `Runner.Run` (`internal/claudecli/runner.go`), when
      profileDir is empty, copy `os.Environ()` filtering out
      `CLAUDE_CONFIG_DIR=` entries; non-empty behavior unchanged
- [x] verify existing runner tests still pass (ambient-dir test at
      `runner_test.go:99` asserts the OLD leak behavior — update it to the
      new contract)
- [x] run tests — must pass before task 4

### Task 4: Profile.IsDefault detection in config

- [x] write failing tests in `internal/config/profile_test.go`: profile
      resolving to `$HOME/.claude` gets `IsDefault=true` (direct path,
      trailing slash, symlink to it); other paths false
- [x] add `IsDefault bool` to `config.Profile`; set in `normalize`
      (`internal/config/profile.go`) by comparing the resolved path with
      resolved `$HOME/.claude`
- [x] run tests — must pass before task 5

### Task 5: Default-profile auth fallback in UI load path

- [x] write failing UI test: default profile whose
      `auth status --json` (with dir) says logged-out triggers a second
      `auth status --json` call with empty profile dir; logged-in second
      result renders the email/subscription; non-default profiles never
      re-check; auth errors keep blank-cell behavior
- [x] thread `IsDefault` from `config.Profile` into the UI column/profile
      struct (`internal/ui/app.go`) — `loadProfile`/`refreshProfile` now
      take the full `config.Profile` (the column already held it)
- [x] implement fallback in `loadProfile` and `refreshProfile`: on clean
      logged-out result for the default profile, call
      `claudecli.LoadAuthStatus` again with `""`; logged-in result wins
      (`loadAuth` in `internal/ui/app.go`; FakeRunner gained
      `ResponsesByDir` so tests can vary a response per profile dir)
- [x] run tests — must pass before task 6

### Task 6: BuildPluginGroups in model

- [x] write failing tests for `BuildPluginGroups` in
      `internal/model`: grouping and sorting (groups by name, plugins by
      name); orphaned plugins (marketplace configured nowhere) still
      grouped; plugin-less marketplaces get a group with empty Plugins;
      per-profile `MarketplaceCell{Configured, CommitHash, CommitDate}`;
      `SourceArg` resolution across profiles; `SourceConflict=true` when
      profiles disagree
- [x] add `MarketplaceCell`, `MarketplaceRow`, `PluginGroup` types and
      `BuildPluginGroups(perProfile, latest)` to `internal/model`
      (new file `groups.go`), reusing existing `PluginRow` building
- [x] keep 80%+ coverage on `internal/model` (100% after this task)
- [x] run tests — must pass before task 7

### Task 7: Grouped rendering with fold/unfold

- [x] write failing UI tests: `View()` shows chevron+marketplace header
      rows with commit `hash date` cells (`—` unconfigured, `local` for
      directory sources without git info) and indented plugin names
      WITHOUT `@marketplace`; `enter`/`space` on a marketplace row folds
      (row count shrinks, header shows `(n plugins)`) and unfolds;
      selection moves across marketplace and plugin rows; selection
      clamps after folding; footer second line switches between plugin
      actions and `i: add  u: update  x: remove  enter: fold` by selected
      row kind (`internal/ui/group_view_test.go`)
- [x] add `folded map[string]bool` and `visibleRows []rowRef`
      (`rowRef{kind, group, plugin}`) to `Model`; rebuild on data load and
      fold toggle; `selRow` indexes `visibleRows`
      (➕ deviation: `visibleRows` is derived per Update/View by
      `visibleRefs` in `internal/ui/rows.go` instead of cached on `Model` —
      caching would need a rebuild at every mutation point and one missed
      call site means stale refs indexing rebuilt groups; only `folded` is
      state)
- [x] switch `viewPlugins`/`pinnedPluginColumn`/`pluginColumn` to walk
      `visibleRows`, emitting marketplace header cells and plugin cells
      index-aligned across pinned + profile columns (`comparisonTable`
      unchanged, `chrome = 11` unchanged) — now
      `pinnedGroupColumn`/`groupColumn`/`marketplaceCell`
      (➕ `model.MarketplaceCell` gained `Local bool` (directory source) so
      the UI can render `local` without re-deriving the source kind)
- [x] handle `enter`/`space` in `handleKey` (no-op outside marketplace
      rows and existing confirmation flow); make footer help
      selection-dependent in `View()`
- [x] run tests — must pass before task 8

### Task 8: Marketplace actions (add / update / remove)

- [x] write failing UI tests: on a marketplace row, `i` runs
      `plugin marketplace add <sourceArg> --scope user` on the selected
      profile (refused when already configured, `SourceConflict`, empty
      `sourceArg`, or arg starting with `-`); `u` runs
      `plugin marketplace update <name>` (refused when not configured);
      `x` asks y/n then runs
      `plugin marketplace remove <name> --scope user` (`--scope user` is
      mandatory — omitting removes from ALL scopes); `e`/`d` are no-ops on
      marketplace rows; busy-gating and timeout→uncertain→forced reload
      match plugin action semantics
      (`internal/ui/marketplace_actions_test.go`)
- [x] extend `startAction`/`actionVerbs`/`pendingAction` dispatch in
      `internal/ui/app.go` to branch on selected row kind; add
      `runMarketplaceAction` mirroring `runPluginAction`
      (➕ deviation: marketplace keys map via a separate `marketplaceVerbs`
      instead of extending `actionVerbs`, and marketplace results reuse
      `actionDoneMsg` — its `plugin claudecli.PluginID` field generalized
      to `target string` — so busy-clearing, refresh, and MCP-reload
      semantics are shared instead of duplicated)
- [x] run tests — must pass before task 9

### Task 9: Implicit marketplace add before plugin install

- [x] write failing UI tests: installing a plugin into a profile lacking
      its marketplace but with a usable `SourceArg` fires ONE async action
      running `plugin marketplace add <sourceArg> --scope user` then
      `plugin install --scope user <id>` sequentially (assert
      `FakeRunner.Calls` order and dirs); add-failure → install not
      attempted, error in status; no usable source (`SourceArg` empty or
      `SourceConflict`) → old refusal message; timeout → uncertain +
      forced reload
      (➕ also: flag-like `SourceArg` refused; add succeeded + install
      failed cleanly → column still reloads, since the add already wrote)
- [x] replace the `hasAvailable` refusal branch in `startAction`
      (`internal/ui/app.go:579`) with the add-then-install command;
      status line updates between steps
      (`adding marketplace X…` → `installing…`)
      (➕ deviation: implemented as two chained tea.Cmds, not one — the add
      reports via a new `marketplaceAddedMsg` whose handler sets the
      `install …` status and fires the install command; a single command
      could not update the status between steps. Busy is held across both
      steps, so the busy-gating is unchanged. The chained install's
      `actionDoneMsg` carries a new `mutated` flag forcing the column
      reload even on a clean install failure, because the add already
      mutated the profile's config)
- [x] replace `TestInstallBlockedWhenMarketplaceMissingInTarget`
      (`internal/ui/actions_test.go:585`) with the new-contract tests
- [x] run tests — must pass before task 10

### Task 10: Verify acceptance criteria

- [ ] verify all four spec items are implemented (walk the spec's
      sections 1–5 against the code)
- [ ] verify edge cases: source conflict, empty source, directory-source
      marketplace, orphaned plugin, plugin-less marketplace, folded-group
      selection clamping, ambient `CLAUDE_CONFIG_DIR` stripping
- [ ] run full test suite (`make test`) — all pass
- [ ] run `make lint` — all issues fixed
- [ ] verify coverage ≥80% on `internal/claudecli`, `internal/config`,
      `internal/model`

### Task 11: Update documentation

- [ ] update README.md: grouped plugin view, fold keybinding, marketplace
      row actions and cell format, implicit marketplace add on install,
      default-profile auth fallback
- [ ] update CLAUDE.md non-obvious constraints: marketplace remove needs
      `--scope user` (omitting removes from all scopes); marketplaces have
      no version — commit hash/date of the clone is the freshness signal;
      default-profile Keychain namespace quirk and the env-stripped
      fallback

## Technical Details

New/changed data structures:

```go
// internal/claudecli
type Marketplace struct {
    Name            string `json:"name"`
    Source          string `json:"source"` // github | git | directory
    Repo            string `json:"repo"`
    URL             string `json:"url"`
    Path            string `json:"path"`
    InstallLocation string `json:"installLocation"`
    CommitHash      string `json:"-"` // filled by loader via git
    CommitDate      string `json:"-"` // YYYY-MM-DD
}
func (m Marketplace) SourceArg() string // repo|url|path by Source, else ""

// internal/model
type MarketplaceCell struct{ Configured bool; CommitHash, CommitDate string }
type MarketplaceRow struct {
    Name, SourceArg string
    SourceConflict  bool
    Cells           []MarketplaceCell
}
type PluginGroup struct {
    Marketplace MarketplaceRow
    Plugins     []PluginRow
}

// internal/ui
type rowKind int // rowMarketplace | rowPlugin
type rowRef struct{ kind rowKind; group, plugin int }
```

CLI commands (all through Runner with per-profile `CLAUDE_CONFIG_DIR` and
`cmdTimeout`):

- `claude plugin marketplace add <sourceArg> --scope user`
- `claude plugin marketplace update <name>` (no scope flag exists)
- `claude plugin marketplace remove <name> --scope user` (scope mandatory)
- git info (NOT through Runner): `git -C <installLocation> log -1 --format=%h %cs`

Rendering sketch:

```
 claude-plugins-official      a1b2c3 06-28   a1b2c3 06-15
    superpowers        v6.1.1  v6.1.1         v6.0.0 ↑
    swift-lsp          v1.0.2  v1.0.2         —
 revdiff (2 plugins)          f4e5d6 06-30   —
```

Edge handling: git failure → blank commit cells; implicit-add failure →
install skipped, error status; add succeeded + install failed →
marketplace stays added, column reload reflects reality; auth fallback
only for `IsDefault` profiles on a clean logged-out answer.

## Post-Completion

**Manual verification**:
- Run `cpm` against real profiles (`~/.claude`, `~/.claude-evo`): confirm
  the default profile shows logged-in, fold/unfold renders correctly with
  the configured NerdFont, marketplace add/update/remove round-trips, and
  installing a plugin into a marketplace-less profile adds the
  marketplace then installs.
- Terminals without a NerdFont show tofu for the chevrons — accepted
  trade-off per design decision.
