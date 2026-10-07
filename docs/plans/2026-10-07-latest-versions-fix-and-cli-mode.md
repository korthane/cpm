# Latest-Version Fix for Installed Plugins and Non-Interactive CLI Mode

## Overview
- **Bug:** cpm never resolves the latest version of an *installed* plugin. For
  example, an installed plugin at 0.34.0 shows no update even though its catalog lists
  0.35.1. Root cause: `claude plugin list --available --json` (verified on CLI
  2.1.291 and in `testdata/plugin_list_available.json`) leaves installed plugins
  out of `available`. `LoadPluginsCached` (`internal/claudecli/latest.go`) puts
  only `data.Available` IDs into `lv.Versions`, and `fillFromCatalogFiles` only
  fills keys that already exist with an empty value. So installed plugins never
  get a key, and the on-disk `marketplace.json` is never read for them. Today
  the "latest"/outdated signal only works when the CLI happens to list a plugin
  as available.
- **Feature:** a non-interactive mode. `cpm <command> [flags] [<profile-dir> ...]`
  prints a result and exits, without starting the TUI. It has two output
  formats: `--text` (default; for humans and agents) and `--json` (for
  automation).
  - `cpm outdated` lists each plugin that has an outdated version installed,
    and in which profiles. It reads the cached catalogs by default.
    `--refresh` runs `plugin marketplace update` first.
  - `cpm refresh` runs `plugin marketplace update` in every profile and reports
    the result for each one.
- Naming: `refresh` is used rather than brew's `update`, because in the Claude
  CLI `plugin update` means "upgrade a plugin". This keeps `update` free for a
  future upgrade command.

## Context (from discovery)
- files/components involved:
  - `internal/claudecli/latest.go`: `LoadPluginsFresh`, `LoadPluginsCached`,
    `fillFromCatalogFiles`
  - `internal/claudecli/plugins.go`: `PluginData`, `LoadPlugins`
  - `internal/model/matrix.go`: `BuildPluginMatrix` (computes
    `PluginCell.Outdated`) and `MergeLatestVersions` (picks the newest version
    across profiles)
  - `cmd/cpm/main.go`: argument handling and `resolveProfiles`. It currently
    rejects every `-`-prefixed argument.
  - `internal/config/profile.go`: `Profile{Path, Label, IsDefault}`
- related patterns found:
  - `claudecli.FakeRunner` with canned responses keyed by args, plus
    `ResponsesByDir`
  - `stubGitCommitInfo` is needed whenever a fake marketplace list has a
    non-empty `installLocation`. Tests that use it must not call `t.Parallel()`.
  - the UI uses a per-command timeout (`cmdTimeout`, 2 min) and
    `refreshTimeout` (30s)
- dependencies: no new modules. Use `encoding/json`, `text/tabwriter` and
  `flag`/hand parsing from the standard library.

## Development Approach
- **testing approach**: TDD. Write a failing test first, starting with the
  installed-plugin regression.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change (`make test`, `make lint`)
- maintain backward compatibility: `cpm [<profile-dir> ...]` still launches the TUI

## Testing Strategy
- **unit tests**: required for every task. The CLI package is tested by
  driving it with `FakeRunner` and asserting on the captured stdout/stderr and
  the exit code. No TTY and no real `claude` binary are involved.
- **e2e tests**: none in this project.
- coverage: 80%+ on the new `internal/cli` package (same bar as the other
  non-UI packages).

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview
- **Fix:** in `LoadPluginsCached`, add every installed plugin ID to
  `lv.Versions` with `""` before the `unresolved` check. Never overwrite a
  version already resolved from `available`. The existing
  `fillFromCatalogFiles` then reads `<installLocation>/.claude-plugin/marketplace.json`
  for those plugins, which is already the correct source (the clone is kept
  fresh by `marketplace update`). The TUI gets the fix for free.
  `parseMarketplaceCatalog` currently reads only `version`. Make it fall back
  to a `source.ref` that passes `isVersionRef`, the same rule `latestVersion`
  applies to `available` entries, so ref-tagged catalog entries resolve too.
- **CLI dispatch:** `main` checks `args[0]` *before* the global `-h`/`--help`
  scan, so `cpm outdated --help` reaches the command help. If `args[0]` is a
  known command (`cli.IsCommand`), `main` calls `cli.ParseArgs(args)`. A usage
  error exits `2` before profiles are resolved. Otherwise `main` calls
  `resolveProfiles(opts.Dirs)` (it stays in `main`) and then
  `cli.Run(ctx, runner, profiles, opts, stdout, stderr)`. Any other `args[0]`
  takes the current TUI path unchanged. A profile dir literally named
  `outdated` in cwd is reached as `./outdated` (document this in the usage
  text).
- **New package `internal/cli`:** no Bubble Tea imports. It exports
  `IsCommand`, `ParseArgs(args) (Options, error)` and `Run`. `Run` returns an
  exit code, so `main` stays thin and everything is testable with
  `FakeRunner`.
- **Outdated computation** reuses `model.MergeLatestVersions` for latest
  versions and the matrix's ordering (`model.ComparePluginIDs`) and version
  compare (`model.IsOutdated`, the exported form of `versionLess`), so the CLI
  and the TUI agree on what "outdated" means. It evaluates *every*
  `Installed` entry of every profile rather than `BuildPluginMatrix` cells:
  the matrix collapses a plugin installed at several scopes in one profile
  into one cell (user scope wins), which would hide an outdated project-scope
  install beside a current user-scope one. A profile with an old clone still
  benefits from a newer catalog in another profile.
- **Concurrency:** load profiles in parallel, one goroutine per profile.
  Within the process there is one writer per config dir, because each profile
  gets its own `marketplace update` and `config.normalize` dedups profiles by
  resolved path. Writes are *not* coordinated with another process, such as a
  running cpm TUI or `claude` on the same profile. This is documented, not
  locked. Each profile load runs under `loadTimeout` (2 min, a constant in
  `internal/cli` that matches the UI's `cmdTimeout`).
- **Shared refresh helper:** add `claudecli.RefreshMarketplaces(ctx, r, dir)
  error`, which runs `plugin marketplace update` under `refreshTimeout`.
  `LoadPluginsFresh` and `cpm refresh` both use it, so neither the args nor
  the 30s budget is duplicated.
- **FakeRunner concurrency:** `FakeRunner.Run` appends to `Calls` without a
  lock. Parallel CLI loads would race on it, so it gets a `sync.Mutex`.
- **Streams and exit codes:**
  - data goes to stdout
  - in text mode, errors and warnings go to stderr
  - in JSON mode, errors are carried inside the JSON and stderr stays empty
  - exit codes:
    - `0`: success, including when outdated plugins are found (brew
      semantics) and when a requested refresh failed (stale data, with a
      warning)
    - `1`: any profile failed to load, or profile resolution failed (no
      profiles, a dir that isn't a directory)
    - `2`: usage error (unknown flag, `--text` together with `--json`, unknown
      command, a profile dir starting with `-`)
  - one failing profile does not stop the others
- **Unknown versions:** installed versions the CLI reports as `unknown` (an
  empty version) are never outdated (`versionLess` with an empty side), so
  `outdated` omits them. Document this in the README.

## Technical Details
- Command line:
  ```
  cpm [<profile-dir> ...]                                  # TUI (unchanged)
  cpm outdated [--refresh] [--text|--json] [<profile-dir> ...]
  cpm refresh  [--text|--json] [<profile-dir> ...]
  cpm -h | --help | <command> --help
  ```
  Flags and profile dirs may be interleaved. `--text` and `--json` together is
  a usage error. Profile dirs starting with `-` are still rejected (they are
  treated as unknown flags).
- `outdated --text` (empty result → `all plugins up to date`):
  ```
  foo@acme  latest 0.35.1
    home      0.34.0
    work      0.34.0
  bar@acme  latest 6.4.1
    work      6.3.0  (disabled)
  ```
  stderr: `error: other: <message>`, and with `--refresh`, a profile whose
  refresh failed adds
  `warning: <label>: marketplace refresh failed; using cached catalog`.
  The profile is shown by `Label`, falling back to `Path`. A non-`user` scope
  is shown as `(scope: project)`.
- `outdated --json` (stable, documented shape):
  ```json
  {
    "profiles": [
      {"label": "home", "path": "/Users/x/.claude-home",
       "refresh": "ok", "error": ""}
    ],
    "outdated": [
      {"plugin": "foo@acme", "latest": "0.35.1",
       "installs": [{"label": "home", "path": "/Users/x/.claude-home",
                     "version": "0.34.0", "scope": "user", "enabled": true}]}
    ]
  }
  ```
  `outdated` is always an array (`[]` when empty), never `null`. `refresh` is
  `"skipped"` (no `--refresh`, or the profile errored), `"ok"` or `"failed"`
  (stale catalog used).
- `refresh --text`: one line per profile, `home  ok` or
  `home  error: <msg>`. `--json`:
  `{"profiles":[{"label","path","error"}]}`.
- Plugins are listed in the same order as `BuildPluginMatrix` (marketplace,
  then name). Installs follow the profile order.

## What Goes Where
- **Implementation Steps**: the bug fix, the `internal/cli` package, the
  `main` dispatch, tests, and README/CLAUDE.md updates
- **Post-Completion**: a manual run against the real profiles

## Implementation Steps

### Task 1: Resolve latest versions for installed plugins

**Files:**
- Modify: `internal/claudecli/latest.go`
- Modify: `internal/claudecli/latest_test.go`
- Modify: `internal/claudecli/plugins.go`
- ➕ Create: `internal/claudecli/latest_matrix_test.go`, `internal/claudecli/export_test.go` (external test package: importing `model` from package `claudecli` is an import cycle)

- [x] write a failing test (with `stubGitCommitInfo`, no `t.Parallel`): an
  installed `foo@acme` 0.34.0 that is absent from `available`, and a
  marketplace whose `installLocation` is a temp dir containing
  `.claude-plugin/marketplace.json` with `0.35.1` → expect
  `lv.Versions[foo@acme] == "0.35.1"`. In the same test, feed the result
  through `model.MergeLatestVersions` + `model.BuildPluginMatrix` and assert
  the cell is `Outdated`.
- [x] write a failing test: the version of an installed plugin that also
  appears in `available` with a version is not overwritten by the catalog file
- [x] write a failing test: a catalog entry with no `version` but a
  version-like `source.ref` (`v1.5.5`) resolves; a branch ref (`main`) does not
- [x] write a test: an installed plugin with no catalog entry stays `""`, and
  the load does not fail
- [x] in `LoadPluginsCached`, add `data.Installed` IDs to `lv.Versions` (only
  where they are missing) before the `unresolved` scan, and update the doc
  comment to explain why: `available` leaves installed plugins out
- [x] make `parseMarketplaceCatalog` fall back to `source.ref` through
  `isVersionRef` (`source` may be a string or an object)
- [x] fix the stale `AvailablePlugin` comment in `plugins.go`: the catalog
  fallback lives in `LoadPluginsCached`, not `LoadPluginsFresh`
- [x] run `make test` - must pass before next task

### Task 2: Shared refresh helper and concurrency-safe FakeRunner

**Files:**
- Modify: `internal/claudecli/latest.go`
- Modify: `internal/claudecli/latest_test.go`
- Modify: `internal/claudecli/fake.go`
- Modify: `internal/claudecli/runner_test.go` (or a new `fake_test.go`)

- [x] write a failing test for `RefreshMarketplaces`: it issues
  `plugin marketplace update` for the dir, returns the error on failure, and
  is bounded by `refreshTimeout` (reuse the
  `TestLoadPluginsFreshBoundsRefreshWithOwnDeadline` pattern)
- [x] implement `RefreshMarketplaces(ctx, r, dir) error` and make
  `LoadPluginsFresh` call it; the existing `LoadPluginsFresh` tests must stay
  green
- [x] write a test calling `FakeRunner.Run` from many goroutines and checking
  that `len(Calls)` is exact; run it with `-race`
- [x] add a `sync.Mutex` to `FakeRunner` around the `Calls` append and update
  its "not safe for concurrent use" doc comment
- [x] run `make test` and `go test -race ./internal/claudecli/...` - must pass
  before next task

### Task 3: CLI argument parsing and Run skeleton

**Files:**
- Create: `internal/cli/cli.go`
- Create: `internal/cli/cli_test.go`

- [x] write failing tests for `ParseArgs`:
  - command name
  - interleaved flags and dirs
  - default `text` format, and `--json`
  - `--text` with `--json` → usage error
  - unknown flag → usage error
  - `--refresh` accepted only by `outdated`
  - `-h`/`--help` → `Help` set
- [x] write failing tests for `IsCommand` (known, unknown, empty string) and
  for `Run`:
  - `Help` prints that command's usage and returns `0`
  - an unknown command returns `2`
- [x] implement `IsCommand`, `ParseArgs` returning
  `Options{Command, Format, Refresh, Help, Dirs}` (with a usage error type),
  the per-command usage text, and `Run(ctx, r, profiles, opts, stdout, stderr) int`
  dispatching to the command handlers
- [x] run `make test` - must pass before next task

### Task 4: `outdated` command

**Files:**
- Create: `internal/cli/outdated.go`
- Create: `internal/cli/outdated_test.go`
- ➕ Modify: `internal/cli/cli.go` (drop the stub), `internal/model/matrix.go`,
  `internal/model/matrix_test.go`

Test setup note: `gitCommitInfo` cannot be stubbed from `internal/cli`.
Fake `plugin marketplace list` responses must keep `installLocation` empty,
and latest versions come from canned `available` entries (use
`ResponsesByDir` to vary them per profile). The catalog-file path is covered
in Task 1.

- [x] write failing tests with `FakeRunner` across two profiles:
  - one outdated plugin installed in both profiles
  - an up-to-date plugin (must be omitted)
  - a disabled outdated install (included, marked)
  - a non-`user` scope install (marked)
  - an `unknown` installed version (omitted)
- [x] write failing tests for error and empty cases:
  - the latest version comes from the *other* profile's newer catalog
  - a profile load error → on stderr in text mode, in `error` in JSON mode
    (stderr empty); other profiles still listed; exit `1`
  - an empty result → `all plugins up to date` / `"outdated": []`, exit `0`
- [x] write failing tests for `--refresh`:
  - without it, no `plugin marketplace update` call is recorded and
    `"refresh": "skipped"`
  - with it, one call is recorded per profile → `"ok"`
  - a failed refresh → stderr warning in text mode, `"failed"` in JSON, exit
    `0`
- [x] implement the parallel per-profile load (`LoadPluginsCached`, or
  `LoadPluginsFresh` with `--refresh`) under `loadTimeout`. Then build the
  result with `MergeLatestVersions` + `BuildPluginMatrix` and keep only rows
  with an `Outdated` cell. (Amended: evaluate every `Installed` entry
  instead of matrix cells; see the ➕ item below.)
- [x] implement the text renderer (tabwriter, label falling back to path,
  scope and disabled markers, errors and warnings on stderr) and the JSON
  renderer (the shape in Technical Details, with non-nil slices)
- [x] ➕ report every outdated install, not one cell per profile: export
  `model.IsOutdated` and `model.ComparePluginIDs` (shared with
  `BuildPluginMatrix`), and test one profile holding a plugin at user scope
  (current) and project scope (outdated) → the project install is reported
  as `(scope: project)` / `"scope": "project"`
- [x] run `make test` and `go test -race ./internal/cli/...` - must pass
  before next task

### Task 5: `refresh` command

**Files:**
- Create: `internal/cli/refresh.go`
- Create: `internal/cli/refresh_test.go`

- [ ] write failing tests:
  - all profiles refreshed (one `plugin marketplace update` call per profile
    dir)
  - one profile failing → reported (stderr in text mode, JSON `error`), exit
    `1`
  - both formats
- [ ] implement a parallel per-profile `claudecli.RefreshMarketplaces`, then
  render text/JSON
- [ ] run `make test` and `go test -race ./internal/cli/...` - must pass
  before next task

### Task 6: Wire command dispatch into `main`

**Files:**
- Modify: `cmd/cpm/main.go`
- Modify: `cmd/cpm/main_test.go`

- [ ] write failing tests:
  - `cpm outdated --json /dir` routes to the CLI with the profile dir
    resolved
  - `cpm outdated --help` prints the command usage and exits `0`, not the TUI
    usage
  - `cpm /dir` still goes to the TUI path
  - `cpm --bogus` still errors
  - `cpm outdated --bogus` → exit `2`
  - `cpm outdated /nonexistent` → exit `1`
- [ ] extract a testable `run(args, stdout, stderr) int` that holds the
  dispatch and the deferred `stop()` of `signal.NotifyContext`. `main` becomes
  `os.Exit(run(...))`, so defers still run.
- [ ] check `cli.IsCommand(args[0])` before the global help scan, then:
  `ParseArgs` (usage error → `2`), `resolveProfiles(opts.Dirs)` (error → `1`),
  and `cli.Run` with `claudecli.NewRunner()` and the signal-aware context
- [ ] extend the top-level `usage` text with the commands, flags, exit codes
  and the `./outdated` note
- [ ] run `make test` and `make lint` - must pass before next task

### Task 7: Verify acceptance criteria
- [ ] verify all requirements from Overview are implemented
- [ ] verify edge cases: no profiles found, all profiles erroring, a plugin
  installed at two scopes in one profile, an empty catalog
- [ ] run full test suite: `make test`
- [ ] run race check: `go test -race ./internal/cli/... ./internal/claudecli/...`
- [ ] run linter: `make lint`
- [ ] verify coverage ≥ 80% on `internal/cli` and `internal/claudecli`
  (`go test -cover ./internal/...`)

### Task 8: [Final] Update documentation
- [ ] README.md: add a "Command-line mode" section covering:
  - the `outdated` and `refresh` commands, their flags, and text and JSON
    examples
  - exit codes and stream usage
  - that `unknown` installed versions are never reported
  - that a CLI refresh is not coordinated with a TUI running on the same
    profile
- [ ] CLAUDE.md:
  - note that `plugin list --available` leaves installed plugins out (why
    installed IDs are added to the version map) and the `source.ref` catalog
    fallback
  - describe `internal/cli` in Architecture and add it to the 80% coverage
    bar
  - note that `FakeRunner` is now concurrency-safe in Testing conventions
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Manual verification**:
- `make run`: an installed plugin with a newer catalog version shows it as
  latest, and its cells are flagged outdated
- `cpm outdated` lists every outdated installed plugin in each profile
- `cpm outdated --refresh --json | jq .` returns valid JSON. `cpm refresh`
  prints `ok` for each profile.
