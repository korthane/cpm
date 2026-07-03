# Vim navigation keys and row highlight

## Overview

Three small UX improvements from TODO.md:

1. `j`/`k` move the selection down/up (aliases for `down`/`up`).
2. `h`/`l` move the selection left/right across profile columns (aliases
   for `left`/`right`).
3. The current row is visibly highlighted: the pinned identity cell
   (marketplace/plugin name or MCP server name, right-hand column) of the
   selected row renders in reverse video, in addition to the already
   reversed selected cell. On wide tables this keeps the row findable at
   a glance.

Applies to both tabs (Plugins and MCP). Design decision (defaulted after
no answer to the style question): pinned-cell reverse, NOT a full-row
background — it needs no renderer changes and reads clearly in any
terminal theme. Revisit if the user prefers the background variant.

## Context (from discovery)

- `internal/ui/app.go` `handleKey` (~line 530): plain
  `switch key.String()` with cases `"left"`, `"right"` (~537–541), `"up"`,
  `"down"` (~543–550). `j/k/h/l` are unused anywhere (existing keys:
  e/d/u/x/i, r, q, tab/shift+tab, enter, space, y/n in
  `handleConfirmKey`). Confirmation flow routes through `handleConfirmKey`
  before `handleKey`'s navigation cases — aliases must behave like arrows
  there too (i.e. not confirm/cancel).
- Selection rendering: `groupColumn` (~1203) and `mcpColumn` (~1253) apply
  `.Reverse(true)` to the selected column's cell at `selRow`. The pinned
  columns — `pinnedGroupColumn` (~1370) and `pinnedMCPColumn` (~1428) —
  take `start, end` but no selection index and never mark the row.
- `viewPlugins` (~1081) / `viewMCP` (~1129) already compute a clamped
  `selRow` and per-column `rowSel = selRow - start`; the same value can be
  passed to the pinned builders.
- `internal/ui/table.go` styles cell text only and pads with plain spaces
  (`table.go:142,160`) — fine for reverse on the cell text, the reason a
  full-row background was not chosen.
- Tests drive `Model.Update` with `tea.KeyMsg` via the `press` helper
  (`internal/ui/actions_test.go`) and assert on `View()` output; reverse
  video appears as the `\x1b[7m` SGR sequence in rendered output.
- Go 1.26; modern idioms; `make test` / `make lint`; UI package has no
  coverage bar, non-UI packages 80%+ (untouched here).

## Development Approach

- **Testing approach**: TDD — write failing tests first, then implement.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes
  in that task — separate checklist items, success and error/edge cases.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run tests after each change; keep backward compatibility (arrow keys,
  action keys, confirmation flow, fold toggle all unchanged).

## Testing Strategy

- **Unit tests**: required for every task (see Development Approach).
- No e2e framework; UI behavior verified by driving `Model.Update` with
  key/load messages and asserting on `View()` output.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs here.
- **Post-Completion** (no checkboxes): quick manual look at the TUI.

## Implementation Steps

### Task 1: j/k/h/l navigation aliases

- [ ] write failing UI tests: `j`/`k` change the selected row exactly like
      `down`/`up` (including clamping at first/last row and fold-aware
      `visibleRows`); `h`/`l` change the selected column exactly like
      `left`/`right` (clamping at first/last profile); aliases work on the
      MCP tab too
- [ ] write failing edge-case tests: during a pending y/n confirmation,
      `j`/`k`/`h`/`l` behave the same as arrow keys do there (do not
      confirm, cancel, or corrupt the pending state)
- [ ] add `"h"`, `"j"`, `"k"`, `"l"` as fallthrough cases beside
      `"left"`/`"down"`/`"up"`/`"right"` in `handleKey`
      (`internal/ui/app.go`)
- [ ] run tests — must pass before task 2

### Task 2: Highlight the selected row's pinned cell

- [ ] write failing UI tests: the pinned cell of the selected row renders
      reversed (`\x1b[7m` present around its text in `View()`) on the
      Plugins tab for both row kinds (marketplace header and plugin row)
      and on the MCP tab; moving the selection moves the highlight; the
      highlight tracks the scroll window (selected row near the bottom of
      a tall list still highlights the correct visible line); no pinned
      cell is reversed for rows other than the selected one
- [ ] pass the window-relative selection index (`selRow - start`, same
      value the profile columns get) into `pinnedGroupColumn` and
      `pinnedMCPColumn`; apply `.Reverse(true)` to the pinned cell's style
      at that index (`internal/ui/app.go`)
- [ ] run tests — must pass before task 3

### Task 3: Verify acceptance criteria

- [ ] verify all three Overview items work on both tabs
- [ ] verify edge cases: empty column list, zero rows, folded groups,
      confirmation pending, scrolled window
- [ ] run full test suite (`make test`) — all pass
- [ ] run `make lint` — all issues fixed
- [ ] verify coverage still ≥80% on non-UI packages (should be untouched)

### Task 4: Update documentation

- [ ] update README.md keybindings: `↑/↓/j/k` rows, `←/→/h/l` columns,
      and mention the row highlight
- [ ] update the in-app footer help line in `View()`
      (`internal/ui/app.go`) only if it fits the current width budget —
      the arrows are already listed; adding `j/k/h/l` is optional, skip if
      it crowds the line

## Technical Details

- `handleKey` switch gains alias cases:
  `case "left", "h":`, `case "right", "l":`, `case "up", "k":`,
  `case "down", "j":`.
- `pinnedGroupColumn(groups, refs, start, end, stale, folded)` and
  `pinnedMCPColumn(rows, start, end)` gain a `sel int` parameter
  (window-relative index, `-1` when nothing selected); the cell at `sel`
  gets `style.Reverse(true)`. Pinned plugin cells currently carry a
  zero-value style and marketplace cells `labelStyle` — reverse composes
  onto both.
- No changes to `internal/ui/table.go`, `internal/model`, or
  `internal/claudecli`.

## Post-Completion

**Manual verification**:
- Run `cpm`, navigate with j/k/h/l on both tabs, confirm the pinned-cell
  highlight follows the selection and stays aligned while scrolling.
