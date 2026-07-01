# CPM — Claude Plugin Manager (TUI)

## Overview

CPM is a terminal UI (Go + Bubble Tea) that makes it easy to compare and manage
Claude Code configuration across multiple **profiles** (distinct
`CLAUDE_CONFIG_DIR` directories such as `~/.claude`, `~/.claude-work`,
`~/.claude-personal`).

The core view is a **comparison table**: one column per profile plus a pinned
rightmost identity column, one row per resource. The first release ships two
tabs:

1. **Plugins tab** — for every `plugin@marketplace` seen in any profile, show its
   state in each profile (installed version, `disabled (vX.Y.Z)`, or absent) and
   the latest available version in the pinned column. Actions: update,
   enable/disable, uninstall, and install into a profile where it is missing.
2. **MCP tab** — the same comparison layout for MCP servers. MCP has no "update"
   concept; v1 supports viewing and removing servers (add/copy deferred).

Each profile column header shows the directory path plus the **account email**
and **subscription plan** for that profile (from `claude auth status --json`).

**Problem it solves:** today there is no easy way to see how plugin/MCP setup
differs between Claude Code profiles or to reconcile them. CPM gives a single
visual, actionable overview.

**Integration:** CPM is a thin, read-mostly front end over the **public `claude`
CLI**. All reads use `claude ... --json`; all mutations use `claude plugin ...`
subcommands, each invoked with `CLAUDE_CONFIG_DIR` pointed at the target
profile. CPM never edits Claude's internal JSON files directly.

## Context (from discovery)

Greenfield project — only `IDEA.md` exists; not yet a git repo. macOS, Go
toolchain available, `claude` CLI on PATH, `ralphex` installed.

### Verified CLI surface (the data/action contract)

Reads (all support per-profile via `CLAUDE_CONFIG_DIR=<dir> claude ...`):

- `claude plugin list --json` → array of `{id, version, scope, enabled,
  installPath, installedAt, lastUpdated}` where `id` is `name@marketplace`.
- `claude plugin list --available --json` → `{installed:[...], available:[...]}`.
  `available[]` entries carry `{pluginId, name, marketplaceName, source:{ref,
  sha,...}}` where `source.ref` (e.g. `v1.5.5`) is the catalog's version.
- `claude plugin marketplace list --json` → `[{name, source, repo|url|path,
  installLocation}]`.
- `claude auth status --json` → `{loggedIn, authMethod, email, orgName,
  subscriptionType, ...}`.
- `claude mcp list` → **no `--json`**; plain text lines
  `name: <cmd-or-url> - <status>`; runs a health check so it is **slow**.

Fresh latest-version strategy (user requirement — do **not** trust stale cache):

- `claude plugin marketplace update [name]` re-fetches a marketplace from its
  source. After updating, the fresh version is readable from the refreshed
  `<installLocation>/.claude-plugin/marketplace.json` (`plugins[].version`) or
  from `plugin list --available --json` (`source.ref`).

Actions:

- `claude plugin enable|disable|uninstall|update|install <plugin>`
  (`install` accepts `plugin@marketplace`).
- `claude mcp remove <name>` (and `add`/`add-json` for future add support).

Measured: a `claude plugin list --json` spawn is ~0.3s (parallel-friendly);
`claude mcp list` is the slow path (health checks) → must load off the UI thread.

## Development Approach

- **Testing approach: TDD (tests first).** Write a failing test, then the code
  to pass it, for every task with logic. This is why the CLI is isolated behind
  a `Runner` interface (below) — parsing and aggregation become pure functions,
  and command execution is faked in tests. No test shells out to the real
  `claude` binary.
- Follow **modern Go** guidelines for the project's Go version (run the
  `/use-modern-go` skill before writing code).
- Complete each task fully before the next. Small, focused changes.
- **CRITICAL: every task with code changes MUST include new/updated tests** —
  success and error/edge cases — listed as separate checklist items.
- **CRITICAL: all tests + linter must pass before starting the next task.**
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run tests after each change. Maintain backward compatibility.

## Testing Strategy

- **Unit tests**: required every task. Pure parsers (JSON/text → structs) and the
  aggregation/merge logic are fully unit-tested with table-driven cases and
  golden fixtures captured from real CLI output (checked into `testdata/`).
- **Runner fakes**: the Bubble Tea commands depend on a `Runner` interface; tests
  inject a fake that returns fixture bytes/errors, so async load and action flows
  are testable without a real `claude` binary.
- **Bubble Tea model tests**: use `teatest` (from `github.com/charmbracelet/x/exp/teatest`)
  or direct `Update(msg)` assertions to verify state transitions (loading →
  loaded, per-column spinner, tab switch, action → refresh) without a real TTY.
- **No project e2e/UI harness** (no Playwright/Cypress) — not applicable.

## Progress Tracking

- Mark completed items `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix; blockers with ⚠️ prefix.
- Keep the plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]`): everything achievable in this repo.
- **Post-Completion** (no checkboxes): manual/interactive verification requiring
  a real multi-profile machine and live marketplaces.

## Architecture (locked decisions)

- **Language / framework:** Go, **Bubble Tea** (the de-facto standard Go TUI
  framework) with **Lip Gloss** (styling) and **Bubbles** (spinner, key help,
  viewport). Module path `github.com/korthane/cpm`.
- **CLI-only backend:** a `claudecli` package wrapping the `claude` binary behind
  a `Runner` interface:
  `Run(ctx, profileDir string, args ...string) ([]byte, error)` — sets
  `CLAUDE_CONFIG_DIR=profileDir`, captures stdout/stderr. A `realRunner` uses
  `os/exec`; a `fakeRunner` powers tests. All reads/writes go through this.
- **Profile discovery precedence** (highest wins):
  1. **CLI args** — `cpm ~/.claude ~/.claude-work` → show **only** these.
  2. **Config file** — `~/.config/cpm/config.yaml` with `profiles: [{path,
     label}]` controlling set, order, and labels.
  3. **Auto-discover** — glob `~/.claude*` directories (default, zero-config).
- **Async, non-blocking loading:** on start, render the table shell immediately
  and fire one Bubble Tea command **per profile** (parallel). Each column shows a
  **per-column spinner** until its `profileLoadedMsg` arrives; columns fill in
  independently. The slow MCP tab loads the same way (lazily on first view).
- **Layout / pinned column:** the **rightmost** column is the resource identity
  (`name@marketplace` + **latest version**) and is **pinned** (always visible).
  The N−1 profile columns to its left **scroll horizontally** (◀/▶) when they do
  not all fit on screen.
- **Cell content (plugins):** installed → `vX.Y.Z`; disabled → `disabled
  (vX.Y.Z)`; absent → `—`. A version that is behind the pinned latest is marked
  (e.g. colored / `↑`).

## Implementation Steps

### Task 1: Scaffold the Go project
- [x] `git init`; add `.gitignore` (Go); `go mod init github.com/korthane/cpm`
- [x] add deps: `bubbletea`, `lipgloss`, `bubbles` (+ `teatest` for tests)
- [x] create `cmd/cpm/main.go` with a minimal Bubble Tea program that renders a
      placeholder and quits on `q`/`ctrl+c`
- [x] add `Makefile` (`build`, `test`, `lint`, `run`) and `.golangci.yml`
- [x] write a smoke test asserting the root model's `Init`/`Update`/`View` wire
      up and `q` triggers quit
- [x] run tests + `golangci-lint` — must pass before Task 2

### Task 2: Command runner abstraction (`claudecli.Runner`)
- [x] define `Runner` interface `Run(ctx, profileDir string, args ...string)
      ([]byte, error)` and a `realRunner` using `os/exec` that sets
      `CLAUDE_CONFIG_DIR`
- [x] implement a `fakeRunner` (test helper) keyed by args → canned
      stdout/error, recording invocations
- [x] write tests: real runner builds correct env/args (use `echo`-style stub or
      a script); fake runner returns canned output and records calls
- [x] write tests: non-zero exit / stderr is surfaced as an error
- [x] run tests + lint — must pass before Task 3

### Task 3: Profile discovery with precedence
- [x] implement config-file load (`~/.config/cpm/config.yaml`, optional) →
      `[]Profile{Path, Label}`
- [x] implement auto-discover of `~/.claude*` directories (dirs only, skip
      non-config dirs like `plugins/` cache paths — restrict to home-level)
- [x] implement `ResolveProfiles(cliArgs, config, discover)` applying precedence
      CLI args > config > auto-discover; expand `~`; de-dup; preserve order
- [x] write tests: each precedence tier wins; `~` expansion; empty/missing config
- [x] write tests: CLI args restrict to exactly the given set; label defaults to
      basename when unlabeled
- [x] run tests + lint — must pass before Task 4

### Task 4: Auth-status loader (per profile)
- [ ] add `LoadAuthStatus(ctx, Runner, profileDir) (AuthStatus, error)` parsing
      `auth status --json` → `{Email, SubscriptionType, LoggedIn}`
- [ ] handle logged-out / malformed / missing-fields gracefully (no crash;
      degrade to blank email/plan)
- [ ] write tests with golden fixture (success) and error/edge fixtures
- [ ] run tests + lint — must pass before Task 5

### Task 5: Plugin data loader (per profile)
- [ ] capture real `plugin list --available --json` output into
      `testdata/` fixtures
- [ ] add `LoadPlugins(ctx, Runner, profileDir) (PluginData, error)` parsing into
      `installed []InstalledPlugin{ID(name,marketplace), Version, Enabled}` and
      `available []AvailablePlugin{ID, LatestVersion(from source.ref)}`
- [ ] split `id` into `name` + `marketplace`; handle `version:"unknown"`
- [ ] write tests: parse fixture → expected structs (installed + available)
- [ ] write tests: malformed JSON and empty arrays handled as errors/empties
- [ ] run tests + lint — must pass before Task 6

### Task 6: Fresh latest-version resolver
- [ ] add `RefreshMarketplaces(ctx, Runner)` → `claude plugin marketplace update`
      (all), and a resolver that reads fresh latest versions from
      `--available --json` after refresh (source of truth = post-update catalog)
- [ ] add optional fallback: read `<installLocation>/.claude-plugin/marketplace.json`
      `plugins[].version` when catalog lacks a ref
- [ ] make refresh best-effort: on failure, fall back to cached version and flag
      the value as stale (surface in UI later)
- [ ] write tests: fake runner simulates update + available; resolver returns
      fresh versions; stale-fallback path on update error
- [ ] run tests + lint — must pass before Task 7

### Task 7: Aggregate plugins into a comparison matrix
- [ ] implement `BuildPluginMatrix(profiles, perProfilePluginData, latestVersions)`
      → rows keyed by `name@marketplace`, each with a cell per profile
      (`{State: Installed|Disabled|Absent, Version}`) and a `LatestVersion`
- [ ] sort rows (by marketplace, then name); compute per-cell `Outdated` flag vs
      latest
- [ ] write tests: union of plugins across profiles; disabled vs installed vs
      absent cells; outdated detection; deterministic ordering
- [ ] write tests: single-profile and all-absent edge cases
- [ ] run tests + lint — must pass before Task 8

### Task 8: Bubble Tea app skeleton — tabs + async parallel loading
- [ ] root model with tab state (`Plugins` | `MCP`), profile list, and per-profile
      load status (`loading|loaded|error`)
- [ ] `Init` fans out one load `tea.Cmd` **per profile** (parallel); define
      `profileLoadedMsg`, `profileErrMsg`, and a `spinner.TickMsg` per column
- [ ] on all messages, update only the affected profile's slice of state
- [ ] key handling: tab switch, `q`/`ctrl+c` quit, `r` reload
- [ ] write tests (teatest / direct `Update`): initial state fires N load cmds;
      each `profileLoadedMsg` flips that column loaded; error → error state
- [ ] write tests: tab switch changes active view; `r` re-triggers loads
- [ ] run tests + lint — must pass before Task 9

### Task 9: Plugin table view — pinned column, headers, horizontal scroll
- [ ] render header row: per profile show `label / path`, `email`, `plan`; pinned
      right header = `plugin@marketplace  latest`
- [ ] render body: one row per matrix row; cells show version / `disabled
      (vX.Y.Z)` / `—`; outdated cells styled; pinned right column always drawn
- [ ] implement horizontal scroll over the N−1 profile columns (◀/▶ keys) while
      keeping the rightmost identity column pinned; show scroll indicators
- [ ] per-column spinner rendered for columns still loading
- [ ] write tests: rendered output contains pinned column at all scroll offsets;
      disabled/absent/outdated cells formatted correctly (golden strings)
- [ ] write tests: scrolling changes visible left columns but never the pinned one
- [ ] run tests + lint — must pass before Task 10

### Task 10: Plugin actions (enable/disable/uninstall/update/install)
- [ ] row/cell selection + an action menu (keys: `e` enable, `d` disable, `u`
      update, `x` uninstall, `i` install-into-profile-where-absent)
- [ ] each action runs the matching `claude plugin ...` via Runner against the
      selected profile, with a confirmation prompt for destructive ops
- [ ] on success, refresh that profile's data (re-run Task 5 loader) and update
      the matrix; show transient status/error line
- [ ] write tests: each action invokes the correct CLI args + profile dir (fake
      runner records calls); confirmation gate blocks until confirmed
- [ ] write tests: action failure surfaces an error and leaves state consistent;
      post-action refresh updates the cell
- [ ] run tests + lint — must pass before Task 11

### Task 11: MCP loader + MCP table view
- [ ] capture real `claude mcp list` text output into `testdata/`; implement a
      **line parser** → `[]MCPServer{Name, Target, Status}` (tolerant of the
      `Checking health…` preamble and varied `- status` suffixes)
- [ ] add `LoadMCP(ctx, Runner, profileDir)`; wire as an async per-profile load
      that populates the MCP tab lazily on first view (slow → spinner)
- [ ] `BuildMCPMatrix` = presence/target per profile, pinned identity column
      (name); reuse the Task 9 table renderer
- [ ] write tests: parse fixture (multiple servers, health preamble, error line)
      → structs; matrix union across profiles; present vs absent cells
- [ ] write tests: malformed/empty output handled; renderer shows spinner while
      MCP tab loads
- [ ] run tests + lint — must pass before Task 12

### Task 12: MCP actions (remove; add deferred)
- [ ] `x` remove → `claude mcp remove <name>` against selected profile, with
      confirmation, then refresh that profile's MCP data
- [ ] show a clear "add not yet supported" hint where install would appear
      (per IDEA: no update for MCP; add needs cmd/url/args — future scope)
- [ ] write tests: remove invokes correct args + profile; confirmation gate;
      failure surfaces error; refresh updates the cell
- [ ] run tests + lint — must pass before Task 13

### Task 13: Verify acceptance criteria
- [ ] verify all Overview requirements are implemented (two tabs, per-profile
      headers with email+plan, pinned latest-version column, horizontal scroll,
      parallel non-blocking load with per-column spinners, plugin actions, MCP
      view+remove)
- [ ] verify edge cases: single profile, logged-out profile, marketplace refresh
      failure (stale flag), plugin `version:"unknown"`
- [ ] run full unit suite; run `golangci-lint` — all issues fixed
- [ ] verify coverage meets project standard (80%+ on non-UI packages)

### Task 14: [Final] Documentation
- [ ] write `README.md`: what CPM is, install/build, usage (CLI args, config
      file, keybindings), screenshots/asciicast placeholder
- [ ] document the config-file schema and profile-discovery precedence
- [ ] note requirement that `claude` CLI must be on PATH

## Technical Details

- **Packages:** `cmd/cpm` (entry), `internal/claudecli` (Runner + loaders +
  parsers), `internal/model` (matrix/aggregation, pure), `internal/ui` (Bubble
  Tea models + views), `internal/config` (profiles + config file).
- **Key messages:** `profileLoadedMsg{profileIdx, PluginData/MCPData}`,
  `profileErrMsg{profileIdx, err}`, `actionDoneMsg{profileIdx, result}`,
  `spinner.TickMsg`.
- **Concurrency:** one `tea.Cmd` per profile per tab; Bubble Tea serializes the
  resulting messages back onto the update loop, so no shared-state locking is
  needed in the model.
- **Fixtures:** real CLI outputs captured under `internal/claudecli/testdata/`
  (`plugin_list_available.json`, `auth_status.json`, `mcp_list.txt`,
  `marketplace_list.json`) so tests never touch a live `claude`.

## Post-Completion

*Manual/interactive verification requiring a real multi-profile machine — no
checkboxes.*

**Manual verification:**
- Run against ≥2 real profiles with differing plugin sets; confirm the matrix,
  per-column parallel loading, and per-column spinners behave on a live machine.
- Exercise each plugin action end-to-end (enable/disable/update/uninstall/install)
  and confirm Claude Code reflects the change (restart may be required for
  `update`/`install` to take effect).
- Confirm `marketplace update` latency is acceptable and the stale-version
  fallback triggers correctly when a marketplace source is unreachable.
- Validate horizontal scrolling and the pinned column on a narrow terminal with
  many profiles.

**Future scope (from IDEA.md, not in this plan):**
- MCP add/copy-across-profiles support (needs command/url/args capture).
- Additional tabs: hooks, installed skills, rules, subagents.
