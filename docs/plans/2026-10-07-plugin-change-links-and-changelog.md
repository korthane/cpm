# Plugin change links and changelog excerpts

## Overview
- `cpm outdated` tells the user *that* a plugin is behind, not *what*
  changed. This change adds a link to the changes between the installed and
  the latest version, and an optional changelog excerpt.
- **Compare link**: a GitHub compare URL built from the installed commit and
  the commit the latest version comes from:
  `https://github.com/<owner>/<repo>/compare/<installed>...<latest>`. It
  needs no tags or version-string guessing, is offline and per install, and
  is exact when both ends are pinned SHAs (a ref fallback is best-effort).
- **History link**: for a plugin living in a subdirectory (multi-plugin
  repos), `https://github.com/<owner>/<repo>/commits/<latest>/<path>`, since
  a compare page cannot be filtered by path and also lists other plugins'
  commits.
- **`outdated --changelog`**: prints the plugin's `CHANGELOG.md` sections
  between the installed (exclusive) and latest (inclusive) version, read
  offline from the marketplace clone.
- **TUI**: when the selected cell is an outdated install with a link, the link
  is shown in the status line and a new `o` key opens it in the browser; the
  key is advertised in the help line only while it applies.

## Context (from discovery)
- Files/components involved:
  - `internal/claudecli/plugins.go` (installed/available parsing),
    `latest.go` (catalog + manifest reads, `readCloneFile`), `gitinfo.go`
    (clone HEAD lookup)
  - `internal/model/matrix.go` (`MergeLatestVersions`, `IsOutdated`,
    `PluginCell`)
  - `internal/cli/cli.go` (`Options`, `ParseArgs`, help), `outdated.go`
    (text/JSON renderers)
  - `internal/ui/app.go` (`View`, `statusLine`, key handling)
- Data facts verified on real profiles:
  - `claude plugin list --json` reports `installPath` per install but **no**
    commit SHA.
  - `<profile>/plugins/installed_plugins.json` (`"version": 2`, `plugins` maps
    id → list of `{scope, installPath, version, gitCommitSha, …}`) carries
    `gitCommitSha` for most installs; some (e.g. directory sources, older
    installs) have none.
  - Marketplace clones are shallow (one commit) and mostly untagged, so a
    local `git log` between versions or a tag-based compare is not possible.
  - `plugin marketplace list --json` gives `repo` (`owner/name`) for `github`
    sources and `url` for `git` sources (https or ssh form).
  - Remote catalog sources (`url`, `git-subdir`, `github` objects) carry the
    repo, optional `path`, optional `ref` and usually a pinned `sha`.
  - `CHANGELOG.md` layouts seen: at the clone root with plain headings
    (`## v1.2.0 - 2026-01-01`, `## 1.2.0`); at the root of a multi-plugin repo
    with plugin-prefixed headings (`## <plugin> v1.2.0 - …`); inside the
    plugin directory; or absent. Some repos version the changelog by the app,
    not the plugin, so its headings never match plugin versions.
- Patterns to follow: reads of third-party clone content go through
  `os.OpenRoot` + `readCloneFile` (regular files only, 1 MiB cap); `model`
  stays pure; `gitCommitInfo` is a stubbable package var; `internal/cli` tests
  cannot stub git, and `Sources`/`CommitSHA` are unreachable through
  `FakeRunner` (they come from files and git), so link and changelog tests
  there build `[]profileLoad` values directly (the tests are `package cli`).
- Known limitations: marketplaces with no git clone, directory sources and
  non-GitHub remotes get no links; plugins whose version is a commit hash are
  never outdated, so they get no links either. JSON is the stable contract;
  the text layout gains indented lines that line-parsing scripts may notice.

## Development Approach
- **testing approach**: TDD (tests first)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in
  that task
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change (`make test`, `make lint`)
- maintain backward compatibility: existing text lines and JSON fields keep
  their shape; new data is added, never renamed or removed
- the repository is public: tests, fixtures and docs use generic names only
  (`acme/widgets`, `example-market`), no real users, orgs or repositories

## Testing Strategy
- **unit tests**: required for every task (see Development Approach above)
- **e2e tests**: the project has none; the TUI is tested by driving
  `Model.Update` and asserting on `View()`

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview
- **Installed commit**: `InstalledPlugin` gains `InstallPath` (from
  `plugin list --json`) and `CommitSHA`, filled best-effort by reading the
  profile's `plugins/installed_plugins.json` and matching on `installPath`.
  This is a new read of a Claude file (cpm still never writes them); any
  read/parse failure leaves `CommitSHA` empty and the link is omitted.
- **Latest source**: `LatestVersions` gains `Sources map[PluginID]
  PluginSource` describing where the latest version lives:
  - relative (in-clone) source: always the plugin's path inside the clone
    and the clone dir (the changelog read needs no git); plus the marketplace
    repo and the clone's full HEAD SHA when known (empty otherwise, which
    just yields no links);
  - remote source: its repo URL, `sha` (else `ref`) and `path`; no clone dir,
    so no changelog. The `ref` fallback is best-effort: a branch ref can move
    after the catalog was loaded, so links are exact only when both ends are
    pinned SHAs.
- **Merging**: the source comes from a profile that supplied the winning
  latest version (`model.LatestSource`), so link and version agree. Ties
  (equal under the version compare) prefer a non-stale profile, then profile
  order. The matrix/group builders keep their signatures: the UI calls
  `LatestSource` for the selected row only.
- **Links** are built in `model` (pure): repo strings are normalized to a
  GitHub web URL only for `github.com` (https, `git@github.com:`, `ssh://`,
  bare `owner/repo`); everything else yields no link. Owner/repo and commits
  are validated (`[A-Za-z0-9._-]`, hex SHAs or plain ref names, no `..`) and
  path segments escaped, because the parts are third-party data and the URL
  is later passed to the system opener. Exact rules:
  - the host must be exactly `github.com` (compared case-insensitively);
    userinfo (other than the `git@` of the ssh forms), ports, query and
    fragment are refused, so look-alike and `user@host` tricks never pass;
  - owner/repo match `[A-Za-z0-9._-]+` and are not `.` or `..`;
  - a commit is a hex SHA (7–40 chars) or a slash-free ref matching
    `[A-Za-z0-9._-]+` without `..` (a `/` would be escaped to `%2F` and a
    `...` would break the `a...b` compare syntax);
  - path segments are not restricted to the owner/repo alphabet: any
    characters are allowed and each segment is `url.PathEscape`d (so `%2e%2e`
    becomes `%252e%252e` and cannot traverse); only absolute paths and
    original `..` segments are refused, checked before `path.Clean`; only a
    literal leading `./` is trimmed; `.` means the repo root (no history
    link);
  - no compare link when the installed SHA equals the latest commit.
- **Changelog** extraction is a pure `model` function over the file text; the
  read stays in `claudecli` (`ReadChangelog`) through `OpenRoot`.
- **Rejected**: un-shallowing clones for `git log` (network, writes into
  Claude's dirs); GitHub releases API (network, rate limits, few repos publish
  releases); tag-based compare (clones are untagged).

## Technical Details
- `claudecli.PluginSource { RepoURL, Commit, Path, CloneDir string }`;
  `RepoURL` is the raw repo string (`owner/repo`, https or ssh URL).
- `gitCommitInfo` keeps its 3-value signature but emits `--format=%H %cs`;
  `Marketplace.HeadSHA` holds the full SHA and `CommitHash` is derived as its
  first 7 characters for display.
- `model.GitHubWebURL(raw string) (string, bool)`.
- `model.ChangeLinks(src PluginSource, installedSHA string) (compare,
  history string)`: compare needs both commits; history needs a commit and a
  non-root path; either may be empty. `src.Commit` is a SHA when the source
  pins one, else a ref (best-effort: a branch may have moved since load).
- `model.ChangelogExcerpt(text, plugin, installed, latest string)
  (excerpt string, ok bool)`:
  - a *version heading* is an ATX heading (up to 3 spaces indent, then
    1–3 `#` followed by a space) whose first word is a version (`v?` +
    dotted numeric, optional pre-release) — *plain* — or whose first word is
    any name followed by a version — *scoped* to that name; a version
    wrapped as `[1.2.0]` or as a link `[1.2.0](https://…)` (keep-a-changelog,
    release-please) is unwrapped before the check;
  - lines inside ``` or ~~~ fenced code blocks are never headings: a fenced
    `## 2.0.0` cannot match latest and a fenced `# 1.0.0` cannot end an
    excerpt (fenced lines stay content of the section they are in);
  - *every* version heading — plain or scoped to any name — ends the section
    above it; only this plugin's sections are collected: if the file has any
    heading scoped to this plugin (name compared case-insensitively), only
    those, otherwise only plain ones (so an interleaved multi-plugin
    changelog never leaks another plugin's section body into the excerpt);
  - the excerpt starts at the collected heading equal to `latest` (version
    compare, not string equality) and runs until the first collected heading
    `<= installed` or EOF; collected headings above `latest` met later are
    skipped; `## Unreleased`-style headings above it are skipped;
  - no heading for `latest` (or empty `latest`) → `ok=false` (the changelog
    tracks something else); trailing blank lines trimmed; output capped at
    200 lines with a `… (truncated)` marker.
- `claudecli.ReadChangelog(src PluginSource) (text, file string, err error)`:
  tries `<Path>/CHANGELOG.md`, then the clone-root `CHANGELOG.md`, via
  `OpenRoot(CloneDir)` + `readCloneFile`; `file` is the clone-relative path
  for display.
- CLI text output (links always, one indented line per install;
  changelog only with `--changelog`, once per plugin, bounded below by the
  oldest installed version among its installs):
  ```
  widget@example-market  latest 1.4.0
    default  1.2.0
      changes: https://github.com/acme/widgets/compare/1a2b3c4...5d6e7f8
    work     1.3.1
      changes: https://github.com/acme/widgets/compare/9a8b7c6...5d6e7f8
  history: https://github.com/acme/widgets/commits/5d6e7f8/plugins/widget
  changelog (plugins/widget/CHANGELOG.md):
    ## v1.4.0 - 2026-01-01
    - …
  ```
  With `--changelog` and nothing to show, one of: `changelog: no entry for
  1.4.0`, `changelog: no CHANGELOG.md`, `changelog: no local changelog`
  (remote source); JSON gives `null` for all three. Informational only, exit
  code unchanged.
  Changelog lines pass through `quoteControl`.
- JSON: each install gains `"compare_url"` (string, `""` when unknown); each
  outdated plugin gains `"history_url"`; with `--changelog` each plugin gains
  `"changelog": {"file", "since", "text"}` or `null` when none was found. The
  key is absent without the flag.
- TUI: `PluginCell` gains `CommitSHA`; the selected row's source comes from
  `model.LatestSource(perLatest, id, row.LatestVersion)`. When no
  status/prompt is pending and the selected cell is outdated with a link,
  `statusLine` renders `changes: <url>` (faint, width-capped — no change to
  `chromeLines`), showing exactly the URL `o` will open: the compare URL, or
  the history URL when there is no compare URL.
- The opener (`openURL`, injectable) runs as a `tea.Cmd`:
  `exec.CommandContext` with a short timeout, no shell, `open` on darwin and
  `xdg-open` elsewhere; Stdin/Stdout/Stderr left unset so the opener cannot
  write over the Bubble Tea screen. It refuses any URL not starting with
  `https://github.com/` (on macOS `open` would launch files and apps).
  Failure sets an error status. The help line adds `o: open changes` only
  when the selected cell has a link.

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs in this repo
- **Post-Completion** (no checkboxes): manual checks against real profiles

## Implementation Steps

### Task 1: GitHub web URL and change links (model)

**Files:**
- Create: `internal/model/links.go`
- Create: `internal/model/links_test.go`
- Modify: `internal/claudecli/latest.go` (add `PluginSource` type only)

- [x] write table tests for `GitHubWebURL`: `owner/repo`, https with and
      without `.git`, `git@github.com:o/r.git`, `ssh://git@github.com/o/r.git`;
      rejects non-GitHub hosts, extra path segments, empty, bad characters
- [x] write table tests for `ChangeLinks`: both links; no installed SHA (no
      compare); root path (no history); ref instead of SHA; invalid commit or
      `..` in path → no link; path segments escaped; ref with `/` or `...`
      refused; `./plugins/x` cleaned; owner `..` refused; host case
      ignored; installed SHA equal to latest → no compare
- [x] add `claudecli.PluginSource` and implement `GitHubWebURL` and
      `ChangeLinks` in `internal/model/links.go`
- [x] run tests - must pass before next task
- [x] ➕ review follow-up: targeted tests for `.`/`..` commits, ports,
      query/fragment, deceptive userinfo and look-alike hosts, and
      percent-encoded traversal in history paths (escaped, not decoded);
      plan wording for path, URL-form and ref-fallback rules

### Task 2: Changelog excerpt parser (model)

**Files:**
- Create: `internal/model/changelog.go`
- Create: `internal/model/changelog_test.go`

- [x] write tests for plain headings (`## v1.2.0 - date`, `## 1.2.0`,
      `### [1.2.0]`, `## [1.2.0](https://example.com/compare/a...b)
      (2026-01-01)`): range between installed and latest, exclusive/inclusive
      bounds, `## Unreleased` above latest skipped, sub-headings kept as content
- [x] write tests for scoped headings in a multi-plugin changelog (only this
      plugin's sections; another plugin's sections never included)
- [x] write tests for edge cases: latest heading missing (`ok=false`),
      installed heading missing (runs to the first lower version or EOF),
      `v` prefix mismatch, CRLF line endings, 200-line cap marker
- [x] ➕ write tests for section boundaries: interleaved scoped changelog
      (`## widget 2.0.0` / `## helper 9.0.0` / `## widget 1.0.0` yields only
      widget's sections); mixed plain/scoped files in both modes
- [x] ➕ write tests for fenced code blocks (``` and ~~~): a fenced heading
      neither matches latest nor ends an excerpt; strict ATX syntax
      (`##2.0.0`, `#### 1.0.0` and 4-space indent are not headings)
- [x] ➕ review follow-up: a ``` opener whose info string holds a backtick
      is an inline code span, not a fence (tilde info strings unchanged);
      test a newer-than-latest heading appearing after the latest heading
- [x] implement `ChangelogExcerpt` reusing the existing version compare
- [x] run tests - must pass before next task

### Task 3: Installed commit SHA (claudecli)

**Files:**
- Modify: `internal/claudecli/plugins.go`
- Create: `internal/claudecli/installed_commits.go`
- Create: `internal/claudecli/installed_commits_test.go`
- Create: `internal/claudecli/testdata/installed_plugins.json` (generic data)
- Create: `internal/claudecli/main_test.go` (`TestMain` pointing `HOME` at
  an empty temp dir)

- [x] write tests: `installPath` parsed into `InstalledPlugin.InstallPath`;
      `CommitSHA` filled by matching `installPath`; missing file, malformed
      JSON, unknown `version`, missing `gitCommitSha`, oversize file → empty
      SHA and no load error; empty profile dir resolves to `~/.claude`
      (`t.Setenv("HOME")`, so no `t.Parallel()`); an `installPath` reached
      through a symlink still matches (compare cleaned/resolved paths), with
      a fallback match on (id, scope) when exactly one entry has them
- [x] parse `installPath` in `plugins.go`
- [x] implement the best-effort read of `<profile>/plugins/installed_plugins.json`
      (size-capped, regular file only) and fill `CommitSHA` once in
      `LoadPluginsCached` (`LoadPluginsFresh` goes through it)
- [x] run tests - must pass before next task
- [x] ➕ `TestMain` in `claudecli` sets `HOME` to an empty temp dir: loads
      for the default profile (`""`) now read
      `~/.claude/plugins/installed_plugins.json`, which must never be the
      developer's real file in tests; the read reuses `readCloneFile` under
      `os.OpenRoot(<profile>/plugins)` (1 MiB cap); `cli`/`ui` tests that
      start asserting on `CommitSHA` (Tasks 6, 8) need the same guard
- [x] ➕ review follow-up: the install-path match ignored scope, so a
      project install could take a user record's SHA at the same path. It
      now matches path within the install's scope only; several matches
      yield the SHA their non-empty values agree on (a SHA-less record
      hides nothing), conflicting SHAs yield empty. The scope fallback
      still requires exactly one record of that scope

### Task 4: Latest-version source and changelog read (claudecli)

**Files:**
- Modify: `internal/claudecli/gitinfo.go`
- Modify: `internal/claudecli/latest.go`
- Modify: `internal/claudecli/latest_test.go`, `gitinfo_test.go`,
  `latest_unix_test.go` (FIFO test), `plugins.go` (raw available
  `source`s via `loadPlugins`); `export_test.go` needed no change
- Create: `internal/claudecli/changelog.go`, `changelog_test.go`

- [x] write tests: `Sources` filled for a relative source (marketplace repo,
      clone HEAD full SHA, plugin path, clone dir) and a remote source (`url`
      / `github` / `git-subdir` with `sha`, `ref`-only fallback); a relative
      source with git failing or a directory marketplace keeps `Path` and
      `CloneDir` with empty `Commit`/`RepoURL`
- [x] write tests for `ReadChangelog`: plugin-dir file wins over root;
      root fallback; none; symlink escaping the clone and FIFO refused
- [x] switch `gitCommitInfo` to `%H %cs` (same signature), store
      `Marketplace.HeadSHA`, derive the short `CommitHash`; update stub data
- [x] keep `url`/`repo`/`path`/`sha`/`ref` from catalog entries and fill
      `LatestVersions.Sources`
- [x] implement `ReadChangelog` via `OpenRoot` + `readCloneFile`
- [x] run tests - must pass before next task
- [x] ➕ a source is recorded only alongside a non-empty latest version, by
      the same entry (`LatestVersions.setLatest`); a relative source sets
      `RepoURL` and `Commit` only as a pair, so a failed git lookup leaves
      both empty even when the marketplace repo is known

### Task 5: Merge the latest source across profiles (model)

**Files:**
- Modify: `internal/model/matrix.go`
- Modify: `internal/model/matrix_test.go`

- [x] write tests for `LatestSource`: picks the source of a profile whose
      version equals the merged latest; ignores profiles with an older
      version; tie between equal versions prefers non-stale, then profile
      order; none when no profile has one
- [x] write tests: `PluginCell.CommitSHA` set from the installed entry the
      cell represents (builder signatures unchanged)
- [x] implement `LatestSource` and `PluginCell.CommitSHA`
- [x] run tests - must pass before next task

### Task 6: Links in `cpm outdated` output

**Files:**
- Modify: `internal/cli/outdated.go`
- Modify: `internal/cli/outdated_test.go`

- [x] write tests (text): `changes:` line under an install with both commits;
      no line when either is missing; `history:` line for a subdirectory
      plugin; control characters quoted
- [x] write tests (JSON): `compare_url` per install and `history_url` per
      plugin, `""` when unknown; existing fields unchanged
- [x] implement links in both renderers; tests build `[]profileLoad` with
      `latest.Sources` and `InstalledPlugin.CommitSHA` set and call
      `findOutdated` and the renderers directly (FakeRunner cannot supply
      them); one smoke test through `Run` uses a temp profile dir holding
      `plugins/installed_plugins.json`
- [x] run tests - must pass before next task

### Task 7: `outdated --changelog`

**Files:**
- Modify: `internal/cli/cli.go`, `cli_test.go`
- Modify: `internal/cli/outdated.go`, `outdated_test.go`
- Modify: `cmd/cpm/main_test.go` (exit 2 for `refresh --changelog`)

- [x] write tests for parsing: `--changelog` accepted by `outdated`,
      rejected by `refresh` (usage error, exit 2); help text lists it
- [x] write tests for text output: excerpt indented under the plugin with its
      file; lower bound is the oldest installed version; `no entry for X` /
      `no CHANGELOG.md` lines; remote source → `no local changelog`
- [x] write tests for JSON: `changelog` object or `null` with the flag, key
      absent without it
- [x] implement the flag and rendering; tests build `profileLoad` values
      whose `Sources` point `CloneDir` at a temp dir with a `CHANGELOG.md`
      (no git needed, since `CloneDir` is kept without a commit)
- [x] run tests - must pass before next task

### Task 8: TUI change link and `o` key

**Files:**
- Modify: `internal/ui/app.go`
- Create: `internal/ui/open.go` (opener with timeout and URL guard)
- Create: `internal/ui/links_test.go`
- Create: `internal/ui/main_test.go` (HOME sandbox, opener tripwire)

- [x] write tests: selecting an outdated cell with a link shows
      `changes: <url>` in the status slot; a pending prompt or status message
      takes precedence; non-outdated or link-less cells show nothing
- [x] write tests: help line shows `o: open changes` only when the selected
      cell has a link; `o` calls the stubbed opener with the compare URL
      (history URL fallback); opener error sets an error status; `o` on a
      link-less cell, a marketplace header row or the MCP tab is a no-op;
      `o` while the filter input is focused types a literal `o`
- [x] write tests for the opener guard: non-`https://github.com/` URLs are
      refused without exec
- [x] implement the status rendering, `o` key, injectable `openURL` and help
      hint
- [x] run tests - must pass before next task

### Task 9: Verify acceptance criteria
- [x] verify all requirements from Overview are implemented
- [x] verify edge cases are handled (no SHA, non-GitHub remote, remote source,
      missing/mismatched changelog, incomplete profile)
- [x] run full test suite: `make test`
- [x] run linter: `make lint`
- [x] verify coverage stays at 80%+ for `claudecli`, `cli`, `config`, `model`
- [x] ➕ added a test that an incomplete profile's install gets its
      compare link from the catalog of the profile supplying latest

### Task 10: [Final] Update documentation
- [x] update README.md: links in `outdated` output, `--changelog`, JSON
      fields, TUI `o` key
- [x] update CLAUDE.md: `installed_plugins.json` read (read-only,
      best-effort), source/link construction rules, changelog heading rules
- [x] move plan (done by orchestrator at completion)

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes,
informational only*

**Manual verification**:
- run `cpm outdated` and `cpm outdated --changelog` on real profiles (without
  `--refresh` unless needed) and open a few compare links to confirm they
  show the expected commit range
- in the TUI, select an outdated cell, check the status line link and that
  `o` opens the browser
