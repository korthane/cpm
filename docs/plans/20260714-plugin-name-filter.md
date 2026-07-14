# Fuzzy name filter (`/`) for plugin and MCP rows

## Overview

Add a `/` key that opens a text input above the table and narrows the visible
rows to those whose name fuzzy-matches what is typed. Today the only way to find
a plugin in a long list is to scroll it; `TODO.md:8` already tracks this as an
open item.

Behavior:

- `/` opens a focused input rendered above the table header, pre-filled with the
  tab's current filter (so `/` again re-focuses and lets you refine).
- Typing filters rows live, on every keystroke.
- `enter` closes the input but **keeps** the filter applied; normal navigation
  and action keys operate on the filtered subset.
- `esc` closes the input and **clears** the filter (full list restored). `esc`
  while navigating an already-filtered list (input closed) also clears it.
- While the input is focused, every rune goes into it: `q` does not quit, and
  `e/d/u/x/i` do not fire actions.
- Matching is fuzzy (subsequence, case-insensitive) against the plugin name and
  the marketplace name. A matching marketplace keeps its whole group; a
  marketplace with no matching plugins and a non-matching name is dropped.
- Rows keep their existing marketplace grouping and alphabetical order — the
  matcher does **not** re-rank rows, because reordering under a grouped table is
  disorienting.
- Folded groups auto-unfold while a filter is active (otherwise a fold would
  hide matches); fold state is restored when the filter is cleared.
- Each tab (plugins, MCP) keeps its own independent filter string.
- No matches → an explicit "no plugins match …" line instead of an empty table.
- When the input is closed but a filter is active, a persistent indicator shows
  the query and the match count, so the filter is never invisible-but-active.
- The on-screen help line tracks the mode: it advertises `/: filter` when idle,
  and while the input is focused it shows only the keys that actually work
  (`enter: apply`, `esc: cancel`) instead of the navigation, action and quit
  hints, which all type literal runes in that mode.

## Context (from discovery)

Files/components involved:

- `internal/ui/app.go` — `Model` (:84), `handleKey` (:517), `View` (:985),
  `viewPlugins` (:1081), `viewMCP` (:1127), `pluginGroups` (:1064),
  `mcpRows` (:1104), `rowCount` (:1119), `rowWindow` (:1151), `enterTab` (:600),
  `toggleFold` (:576), `startAction` (:651), `selectedMarketplaceRow` (:1026).
- `internal/ui/rows.go` — `rowRef` (:15), `visibleRefs` (:26): the single choke
  point for row visibility.
- `internal/model/` — `PluginGroup`/`MarketplaceRow` (`groups.go:25,38`),
  `PluginRow`/`PluginCell` (`matrix.go:30,40`), `MCPRow` (`mcp.go:19`).
- `README.md`, `CLAUDE.md`, `TODO.md`.

Related patterns found:

- `m.pending != nil` in `handleKey` (app.go:517) is the existing precedent for a
  modal key mode that swallows keys before the main switch — the filter-editing
  branch mirrors it.
- The pinned/selected cell highlight is applied in `pinnedGroupColumn`
  (app.go:1400) and `column.groupColumn` (app.go:1203); tests observe it via
  `forceANSI` + `reversedPinnedCells` (`row_highlight_test.go:20,36`).
- `internal/model` is pure aggregation with no I/O — the matcher belongs there.

Dependencies identified:

- `github.com/charmbracelet/bubbles v1.0.0` is already a direct require; only
  `bubbles/spinner` is imported, `bubbles/textinput` needs no go.mod change.
- New: `github.com/sahilm/fuzzy v0.1.3` (latest, verified against
  proxy.golang.org on 2026-07-14).

## Development Approach

- **Testing approach**: TDD (tests first)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run `make test` and `make lint` after each change
- Maintain backward compatibility: with no filter active, every existing
  behavior (navigation, folding, actions, reload, scroll window) is unchanged

## Testing Strategy

- **Unit tests**: required for every task. `internal/model` gets table-driven
  tests for the matcher; `internal/ui` tests drive `Model.Update` with key/load
  messages and assert on `View()` output (project convention, `CLAUDE.md:38`).
- **E2E tests**: none in this project (no Playwright/Cypress). The UI tests
  above are the behavioral tests.
- Tests asserting on styling must call `forceANSI(t)` and must not use
  `t.Parallel()` (the lipgloss profile is package-global).
- Coverage bar: 80%+ on `internal/model`.

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## Implementation Steps

### Task 1: Add fuzzy matcher and row-filtering functions in `internal/model`

- [x] run `go get github.com/sahilm/fuzzy@v0.1.3` and `go mod tidy`
      (⚠️ `bubbles` already pulls in fuzzy v0.1.1 indirectly, so a bare
      `go mod tidy` downgrades it back — the `go get` must come last)
- [x] run `go doc github.com/sahilm/fuzzy` and confirm the exact API before
      coding: we need an **order-preserving** call (`FindNoSort`/`FindFromNoSort`,
      not `Find`, which sorts by score) and the behavior for an empty pattern
      (empty pattern → nil matches, so the filters guard on `query == ""`)
- [x] write `internal/model/filter_test.go` FIRST — table-driven cases for
      `FilterPluginGroups`: empty query returns groups unchanged (identity);
      subsequence match (`fb` matches `foo-bar`); case-insensitive; a plugin
      match keeps only matching plugins in its group; a marketplace-name match
      keeps the whole group; a group with neither is dropped; no match anywhere
      returns an empty slice; group and plugin order is preserved (never
      re-ranked)
- [x] write tests for `FilterMCPRows`: empty query identity, subsequence match,
      case-insensitive, order preserved, no match → empty
- [x] create `internal/model/filter.go` with `FilterPluginGroups(groups
      []PluginGroup, query string) []PluginGroup` and `FilterMCPRows(rows
      []MCPRow, query string) []MCPRow`; match `PluginRow.ID.Name` and
      `MarketplaceRow.Name` / `MCPRow.Name`; empty query returns the input
      unchanged
- [x] run `make test` and `make lint` — must pass before task 2

### Task 2: Add filter state and key handling to `Model` (plugins tab)

- [x] write `internal/ui/filter_test.go` FIRST — drive `Model.Update`:
      `/` opens the input (assert `View()` shows the prompt); typing runes lands
      in the input; `q` while editing does NOT quit and appears in the input;
      `d` while editing does NOT start an action (assert the `FakeRunner`
      recorded no plugin-disable call); `enter` closes the input; `esc` closes
      the input and clears the query; `esc` with the input closed but a query
      active clears the query; `/` after `enter` re-opens the input pre-filled
      with the previous query
- [x] add state to `Model` (app.go:84): `filters [tabCount]string` (per-tab
      applied query), `filterInput textinput.Model`, `filterEditing bool`;
      initialize the input in `New` (app.go:125)
      (➕ the input's cursor is set to `cursor.CursorStatic`: a blinking caret
      would keep the event loop ticking for no benefit, and its blink `tea.Cmd`
      makes key-driven tests sleep)
- [x] add a `handleFilterKey` branch at the top of `handleKey` (app.go:517),
      after the existing `m.pending != nil` check: `esc` clears + closes,
      `enter` commits + closes, `ctrl+c` still quits, `tab`/`shift+tab` closes
      the input (keeping the query) and falls through to `enterTab`, every other
      key is forwarded to `textinput.Update` and syncs `m.filters[m.tab]`
- [x] bind `/` in the main key switch to open/focus the input pre-filled from
      `m.filters[m.tab]` (cursor at end), and bind bare `esc` to clear an active
      filter
- [x] run `make test` and `make lint` — must pass before task 3

### Task 3: Apply the filter to visible rows, folding, and selection

- [x] write tests FIRST — typing a query narrows the rows shown in `View()`;
      rows of a *folded* group whose plugins match are visible while filtering
      (auto-unfold) and the fold is restored once the filter is cleared; the
      selected row is reset to the top on a query change and never points past
      the end when the list shrinks; `j`/`k` after `enter` navigate only the
      filtered rows (assert with `forceANSI` + `reversedPinnedCells`, no
      `t.Parallel()`); an action key on a filtered row targets the right plugin
- [x] apply `model.FilterPluginGroups` inside `m.pluginGroups()` (app.go:1064)
      so every caller (`viewPlugins`, `rowCount`, `toggleFold`, `startAction`,
      `selectedMarketplaceRow`) sees the filtered set through one choke point
- [x] centralize row visibility in a `Model` method (e.g. `visiblePluginRefs()`)
      that passes a `nil` fold map to `visibleRefs` (rows.go:26) while a filter
      is active, and update the five call sites to use it
      (➕ `pinnedGroupColumn` gets the same fold map via `activeFolds()`, so the
      fold chevrons match the auto-unfolded rows)
- [x] reset `m.selRow` to 0 whenever the query changes, and keep the existing
      clamping so a shrinking list can't strand the cursor
- [x] run `make test` and `make lint` — must pass before task 4

### Task 4: Render the filter line, empty state, and layout

- [x] write tests FIRST — the input renders above the table header; with the
      input closed and a query active, the persistent indicator shows the query,
      the match count and `esc: clear` (e.g. `filter: foo (7/42)  esc: clear`);
      a query matching nothing renders a "no plugins match" line instead of an
      empty table; the visible row count shrinks by one when the filter line is
      present (scroll-window regression: assert against a short `WindowSizeMsg`
      height)
- [x] write tests FIRST for the help line in all three states: idle (advertises
      `/: filter` alongside the existing hints); **editing** (shows only the keys
      that work — `enter: apply  esc: cancel` — and does NOT advertise the
      navigation/action/quit keys, since `q`, `j`/`k` and `e/d/u/x/i` all type
      literal runes while the input is focused); closed-but-filtered (back to the
      normal navigation hints, since every key works again)
- [x] render the filter line in `viewPlugins` (app.go:1081) above the header,
      and the closed-but-active indicator in the same slot
      (➕ the indicator's match count needs the *unfiltered* row total, so
      `pluginGroups` was split into `allPluginGroups` (raw) + `pluginGroups`
      (filtered); the denominator ignores folds so it does not move when a
      group is folded)
- [x] make the hard-coded `const chrome = 11` in `rowWindow` (app.go:1151)
      account for the extra filter line instead of a fixed literal — it is the
      count of non-body lines and silently breaks scrolling otherwise
      (➕ now `chromeLines()`: +1 for the filter line, −1 while editing because
      the action help line is suppressed)
- [x] add `/: filter` to the help line (app.go:1008) and swap it for the
      editing-mode hints while `m.filterEditing` is true
- [x] suppress the second, context-dependent help line (the marketplace-action
      hints, app.go:1009-1016) while the input is focused — no action key is
      reachable in that mode
- [x] run `make test` and `make lint` — must pass before task 5

### Task 5: Extend the filter to the MCP tab (separate per-tab query)

- [x] write tests FIRST — `/` on the MCP tab filters server names; each tab keeps
      its own query across `tab`/`shift+tab` switches (filter the plugins tab,
      switch to MCP, confirm the MCP list is unfiltered, switch back, confirm the
      plugin filter is still applied); switching tabs while the input is focused
      closes the input but preserves both queries; an MCP remove action on a
      filtered row targets the right server
- [x] apply `model.FilterMCPRows` inside `m.mcpRows()` (app.go:1104) and render
      the filter line / empty state in `viewMCP` (app.go:1127)
      (➕ `mcpRows` split into `allMCPRows` (raw) + `mcpRows` (filtered), mirroring
      the plugins tab, since the indicator's denominator needs the raw total)
- [x] run `make test` and `make lint` — must pass before task 6

### Task 6: Verify acceptance criteria

- [x] verify every behavior listed in the Overview is implemented — each maps to
      a passing test (open/refine, live filter, enter keeps, esc clears from
      either mode, rune swallowing, fuzzy plugin+marketplace match, no re-rank,
      auto-unfold, per-tab query, empty state, indicator, mode-aware help)
- [x] verify edge cases: filter active during an async column reload; filter
      active while a `y/n` confirm prompt is up (the confirm must still win the
      key); a query that matches only marketplace headers and no plugins;
      zero loaded columns; a query containing regex/glob metacharacters is
      treated as literal text
      (➕ added `TestFilterSurvivesReload`, `TestFilterOnZeroLoadedColumns`,
      `TestFilterMatchingOnlyMarketplaceHeader`, `TestFilterQueryIsLiteralText`;
      the confirm-prompt case was already covered by
      `TestFilterInputDoesNotBlockConfirmPrompt`)
- [x] run the full test suite (`make test`)
- [x] run `make lint` — all issues must be fixed (0 issues)
- [x] verify coverage is 80%+ on `internal/model` (and not regressed elsewhere)
      — `model` 100%, `config` 98.5%, `claudecli` 98.9%

### Task 7: [Final] Update documentation

- [ ] document the `/` filter in `README.md` (key table and behavior: enter
      keeps, esc clears, per-tab query, fuzzy match on plugin + marketplace name)
- [ ] add a note to `CLAUDE.md` under non-obvious constraints if any emerged —
      in particular the `rowWindow` chrome-line count and the auto-unfold rule
- [ ] tick the `/` filter item in `TODO.md:8`

## Technical Details

**New state on `Model` (`internal/ui/app.go`)**

```go
filters      [tabCount]string  // applied query, per tab; "" == no filter
filterInput  textinput.Model   // bubbles/textinput, focused only while editing
filterEditing bool             // true == all keys go to filterInput
```

**Key routing in `handleKey`** (order matters):

1. `m.pending != nil` → `handleConfirmKey` (existing; the confirm prompt wins)
2. `m.filterEditing` → `handleFilterKey`
3. main switch (`/` opens the input, bare `esc` clears an active filter)

**Filtering flow** — the filter is applied at the two data accessors so every
consumer sees it through one choke point:

- `pluginGroups()` → `model.FilterPluginGroups(groups, m.filters[tabPlugins])`
- `mcpRows()` → `model.FilterMCPRows(rows, m.filters[tabMCP])`
- `visibleRefs(groups, folded)` is called with a `nil` fold map while a filter is
  active, so folded groups do not hide matches.

**Matching** — `sahilm/fuzzy`, using an order-preserving call (`FindNoSort` /
`FindFromNoSort`; plain `Find` sorts by score and would re-rank rows). A group is
kept if its `MarketplaceRow.Name` matches (whole group retained) or if any
`PluginRow.ID.Name` matches (group retained with only matching plugins).

## Post-Completion

*Items requiring manual intervention — no checkboxes, informational only*

**Manual verification**:

- Run `make run` against a real profile set with a long plugin list: check that
  the input renders where expected at several terminal widths/heights, that the
  scroll window is correct with the filter line present, and that a filtered
  action (enable/disable) hits the right plugin.
- Confirm the filter feels right against a marketplace name (whole group shown)
  versus a plugin name (single row shown).
