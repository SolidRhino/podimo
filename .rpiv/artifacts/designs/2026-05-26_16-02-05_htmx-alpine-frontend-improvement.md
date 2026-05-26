---
date: 2026-05-26T16:02:05+0200
author: SolidRhino
commit: e3733b2
branch: go-rewrite
repository: podimo
topic: "htmx-alpine-frontend-improvement"
tags: [design, frontend, htmx, alpinejs]
status: in-progress
last_updated: 2026-05-26T16:02:05+0200
last_updated_by: SolidRhino
parent: .rpiv/artifacts/research/2026-05-26_15-46-08_htmx-alpine-frontend-improvement.md
---

# Design: HTMX and Alpine.js Frontend Improvement

## Summary

Progressively enhance the Podimo RSS web frontend using HTMX for server-driven interactions (search, form submission, subscriptions) and Alpine.js for client-side UI state (copy-to-clipboard feedback). Both libraries load via CDN. The implementation fixes the existing broken `staticFS` embed directive, refactors template parsing to a single template set using `ExecuteTemplate`, and adds three new HTML fragment partials for HTMX swaps.

## Requirements

- Load HTMX and Alpine.js via CDN in both page templates
- Fix the `staticFS` embed bug (`//go:embed static/*` currently attaches to `templatesFS`)
- Refactor template parsing from two separate template fields to a single parsed template set
- Replace vanilla JS `fetch()` search with declarative HTMX `hx-get` on `/search`
- Add `HX-Request` branching in `handleSearch` to render HTML partials instead of JSON
- Surface `/subscriptions` in the UI with an HTMX-powered button
- Add `HX-Request` branching in `handleSubscriptions` to render HTML partials
- Support HTMX form POST on `/` by rendering a feed result fragment instead of a full page
- Replace imperative copy-to-clipboard JS with Alpine.js declarative directives
- Maintain `{{ if }}` Go template conditionals for `NeedCredentials` (static config)
- Keep CSS transitions for visual feedback (hybrid Alpine + CSS approach)
- Update existing tests to assert on HTML partial outputs

## Current State Analysis

Two standalone HTML templates (`templates/index.html`, `templates/feed_location.html`) are embedded via `//go:embed` and parsed as separate `*template.Template` fields (`App.indexTmpl`, `App.feedTmpl`). The static file server is broken because `//go:embed static/*` attaches to `templatesFS` instead of `staticFS`. Search uses vanilla JS `fetch()` with manual DOM injection. Subscriptions is a JSON-only endpoint with no frontend consumer. Form POST performs full-page navigation. Copy-to-clipboard uses imperative `innerHTML` injection and `setTimeout` class toggling.

### Key Discoveries
- `main.go:26-30` — `staticFS` is empty because directives attach only to the immediately following variable
- `main.go:225-286` — `handleSearch` unconditionally returns JSON
- `main.go:290-323` — `handleSubscriptions` unconditionally returns JSON
- `templates/index.html:104-152` — Vanilla JS search `doSearch` with manual HTML string concatenation
- `templates/feed_location.html:26-50` — Imperative `setButtonDiv()` and `copy()` inline scripts
- `main_test.go:39-68` — Tests replicate template parsing manually; must stay in sync with `main()`

## Scope

### Building
- Fix `staticFS` embed directive binding
- Add HTMX + Alpine.js CDN script tags to both template `<head>` elements
- Refactor `main.go` to parse all templates as a single set using `template.ParseFS` with multiple files
- Refactor all `Execute` calls to `ExecuteTemplate(name)`
- New: `templates/partials/search_results.html` — HTMX fragment for search results
- New: `templates/partials/feed_result.html` — HTMX fragment for feed result with Alpine copy button
- New: `templates/partials/subscriptions.html` — HTMX fragment for followed podcasts
- Modify `templates/index.html`: Remove vanilla JS, add HTMX attributes, add `#result` container
- Modify `templates/feed_location.html`: Use `{{template "feed_result" .}}` inside full HTML shell
- Modify `main.go`: `handleSearch`, `handleSubscriptions`, `handleIndex` POST branch on `HX-Request`
- Modify `main_test.go`: Update template parsing, update handler tests for HTML partials

### Not Building
- CSP headers (current inline scripts already exist; no regression)
- Cache-Control middleware for static files (staticFS fix alone is sufficient)
- Service worker or offline capability
- Replacing `qrcode.min.js` CDN dependency
- Template inheritance or base layout (Go `text/template` does not support Jinja2-style blocks)
- JSON backward compat for search/subscriptions (full HTMX replacement confirmed)

## Decisions

### Template Parsing: Single Template Set with Names
**Explored:** Separate `*template.Template` fields per template (current pattern) vs single parsed set with `ExecuteTemplate(name)`
**Decision:** Single template set. `template.ParseFS(templatesFS, "templates/index.html", "templates/feed_location.html", "templates/partials/*.html")` returns one `*template.Template` containing all named templates. Handlers call `ExecuteTemplate(w, "index.html", data)`. This is idiomatic Go and avoids adding a struct field for every partial.
**Rationale:** The project explicitly rejected template inheritance in the Go rewrite (precedent: deleted `templates/base.html`). Named templates within a single parsed set are the Go-idiomatic equivalent of partials and require minimal boilerplate.

### CDN Loading for HTMX and Alpine.js
**Explored:** Vendoring into `static/` vs CDN `<script>` tags in `<head>`
**Decision:** CDN. Insert `<script src="https://unpkg.com/htmx.org@2.0.3">` and `<script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js">` in both template heads.
**Rationale:** User explicitly requested CDN. The app already loads `qrcode.min.js` from cdnjs.cloudflare.com (`templates/feed_location.html:26`). Vendoring would require additionally fixing an existing staticFS bug to serve the files, adding cache-control middleware, and bloating the binary. CDN avoids all three.

### Go Template Conditionals for NeedCredentials
**Explored:** Alpine.js `x-show` vs Go `{{ if }}` for credential field visibility
**Decision:** Keep Go `{{ if .NeedCredentials }}`. `LocalCredentials` is a process-static constant loaded from env var at startup and never changes at runtime. Alpine's reactivity is unnecessary for immutable config.
**Rationale:** `config.go:62-63` loads `LocalCredentials` once. Removing DOM server-side is correct — Alpine cannot reference elements that never arrived. For new dynamic UI (toggles, dropdowns), Alpine would be appropriate.

### Alpine.js + CSS Hybrid for Copy Feedback
**Explored:** Pure Alpine `x-show` + `x-transition` vs Alpine state + CSS class toggle
**Decision:** Hybrid. Alpine owns the `copied` boolean state. `:class="{ 'visible': copied }"` toggles the class. CSS `.copy-feedback.visible` handles the `opacity` transition.
**Rationale:** Inside HTMX-swapped fragments, `x-show` + `x-transition` risks stale transition classes if Alpine misses initialization. CSS transitions are more robust — even without Alpine initialization, the element simply stays hidden rather than stuck in an intermediate state.

### Full HTMX Replacement (No JSON Backward Compat)
**Explored:** Dual output (HX-Request → HTML, other → JSON) vs full replacement
**Decision:** Full HTMX replacement. Remove JSON encoding from handlers entirely.
**Rationale:** User explicitly chose full replacement. This is a self-hosted web UI, not a public API. All consumers of `/search` and `/subscriptions` are browser-based HTMX requests. Tests must be updated to assert HTML partial output.

## Architecture

### main.go — MODIFY
```go
```

### templates/index.html — MODIFY
```html
```

### templates/feed_location.html — MODIFY
```html
```

### templates/partials/search_results.html — NEW
```html
```

### templates/partials/feed_result.html — NEW
```html
```

### templates/partials/subscriptions.html — NEW
```html
```

### static/style.css — No changes (existing CSS reused)

### main_test.go — MODIFY
```go
```

## Slices

### Slice 1: Foundation — Fix staticFS, refactor template parsing, add CDN scripts

**Files**: `main.go`, `templates/index.html`, `templates/feed_location.html`, `main_test.go`

#### Automated Verification:
- [ ] `go test ./...` passes
- [ ] `go vet ./...` passes
- [ ] Static file request `/static/style.css` returns `200 OK` with `text/css` content type
- [ ] Template parsing succeeds (no boot-time `os.Exit`)

#### Manual Verification:
- [ ] Page source contains HTMX and Alpine.js `<script>` tags in `<head>`
- [ ] No JSON response from `/search` (full replacement confirmed)

### Slice 2: Search HTMX — Partial template and declarative search

**Files**: `templates/partials/search_results.html`, `templates/index.html`, `main.go`, `main_test.go`

#### Automated Verification:
- [ ] `TestHandleSearch` asserts HTML body contains search result markup
- [ ] `go test ./...` passes

#### Manual Verification:
- [ ] Typing in search box and clicking Search loads results without page reload
- [ ] Clicking a result populates the Podcast ID field

### Slice 3: Form POST + Alpine.js — Feed result fragment with copy button

**Files**: `templates/partials/feed_result.html`, `templates/feed_location.html`, `templates/index.html`, `main.go`, `main_test.go`

#### Automated Verification:
- [ ] `TestHandleIndexPost` asserts fragment contains feed URL and copy button
- [ ] `go test ./...` passes

#### Manual Verification:
- [ ] Submitting the form shows feed result inline without page reload
- [ ] Clicking "Copy to clipboard" shows "Copied!" feedback for 2 seconds
- [ ] QR code renders correctly

### Slice 4: Subscriptions HTMX — Surface followed podcasts in UI

**Files**: `templates/partials/subscriptions.html`, `templates/index.html`, `main.go`, `main_test.go`

#### Automated Verification:
- [ ] `TestHandleSubscriptions` asserts HTML body contains subscription list markup
- [ ] `go test ./...` passes

#### Manual Verification:
- [ ] "Your Subscriptions" button loads followed podcasts without page reload
- [ ] Clicking a subscription populates the Podcast ID field

## Desired End State

### Handler Dispatch
```go
func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
    // ... auth + search logic ...
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    a.templates.ExecuteTemplate(w, "search_results.html", map[string]interface{}{
        "Results": results,
        "Query":   searchQuery,
    })
}
```

### HTMX Search in Template
```html
<input type="text" placeholder="Search by name"
       hx-get="/search" hx-target="#searchResults"
       hx-trigger="keyup changed delay:300ms, keydown[key=='Enter']"
       name="q" id="search_query">
<div id="searchResults"></div>
```

### Alpine Copy Button in Partial
```html
<div x-data="{ copied: false, url: '{{ .URL }}' }"
    x-init="$watch('copied', v => v && setTimeout(() => copied = false, 2000))">
  <button @click="navigator.clipboard.writeText(url).then(() => copied = true)">
    Copy to clipboard
  </button>
  <span :class="{ 'visible': copied }" class="copy-feedback">Copied!</span>
</div>
```

## File Map
- `main.go` — MODIFY (embed fix, template parsing, handler branching)
- `templates/index.html` — MODIFY (HTMX attrs, remove vanilla JS, subscriptions section)
- `templates/feed_location.html` — MODIFY (CDN scripts, {{template}} wrapper)
- `templates/partials/search_results.html` — NEW (HTMX fragment)
- `templates/partials/feed_result.html` — NEW (HTMX fragment with Alpine)
- `templates/partials/subscriptions.html` — NEW (HTMX fragment)
- `main_test.go` — MODIFY (template parsing, handler assertions)

## Ordering Constraints
- Slice 1 must complete before Slice 2, 3, or 4 (foundation)
- Slice 3 depends on Slice 1 but can run before or after Slice 2
- Slice 4 depends on Slice 1 but can run before or after Slice 2 or 3
- No strict ordering between Slice 2, 3, and 4 (orthogonal concerns)
- Tests must be updated in the same slice as the code they test

## Verification Notes
- Template syntax errors cause `os.Exit(1)` in production and `t.Fatalf` in tests. Validate templates with `go test ./...` after every slice.
- The `staticFS` bug fix must be verified by checking `/static/style.css` returns 200.
- HTMX partials must not contain `<!doctype html>`, `<html>`, `<head>`, or `<body>` — only fragment markup.
- Alpine directives inside HTMX-swapped content initialize via MutationObserver automatically. If edge cases arise, add `htmx:afterSwap` listener with `Alpine.initTree()`.
- Basic Auth browser caching means HTMX XHRs auto-carry `Authorization` headers on same-origin requests. No explicit header wiring needed for non-local mode.
- Go `text/template` auto-escapes string interpolations (XSS-safe). Verify no `template.HTML` usage is introduced.
- Form action must remain `./` (relative) to preserve reverse-proxy compatibility (precedent: `15c9a09`).

## Performance Considerations
- HTMX adds server round-trips but eliminates client-side JS processing. Negligible impact for a single-user/self-hosted tool.
- CDN libraries are cached by browsers across sessions (HTMX ~15KB gzipped, Alpine ~15KB gzipped).
- Single template set parsing happens once at boot — no runtime penalty.
- No N+1 risks introduced; GraphQL calls remain unchanged.

## Migration Notes
- **No data migration** required (templates only).
- **Rollback strategy:** Revert `main.go` template parsing to separate fields and restore original `templates/index.html` vanilla JS block.
- **Backwards compatibility:** RSS feed endpoints (`/feed/*.xml`) are untouched. Only the web UI changes.
- **Template changes:** Old `index.html` vanilla JS is removed. If users bookmarked `/` expecting the old behavior, the new HTMX-enhanced page is fully compatible (progressive enhancement).

## Pattern References
- `main.go:114` — `template.ParseFS(templatesFS, "templates/index.html")` (current single-file parse pattern)
- `main.go:585-592` — `Execute` + error handling pattern
- `main_test.go:39-43` — Test template parsing (must mirror production)
- `main.go:26-30` — `//go:embed` directive placement (critical for staticFS)

## Developer Context
- **Q (research):** The search feature currently uses vanilla JS fetch(). Should the search endpoint support both JSON and HTML partials, or fully replace?
  **A:** Full HTMX replacement.
- **Q (research):** What else should be included? Surface subscriptions UI, fix staticFS embed bug, create partial templates.
  **A:** All three selected.
- **Q (design):** Two valid patterns for handling partial templates in Go. Which should we follow?
  **A:** Single template set with names (ExecuteTemplate).

## Design History
- Slice 1: Foundation — generated (2006-05-26T16:02:05+0200)
  | `main.go` — fixed `//go:embed` directives, refactored `indexTmpl`/`feedTmpl` → `templates`, changed `Execute` → `ExecuteTemplate`
  | `templates/index.html` — added HTMX + Alpine.js CDN scripts
  | `templates/feed_location.html` — added HTMX + Alpine.js CDN scripts
  | `main_test.go` — updated `setupTestApp` to parse as single set
- Slice 2: Search HTMX — generated
  | `templates/partials/search_results.html` — new partial for search results
  | `templates/index.html` — declarative search with `hx-get`/`hx-trigger`, removed vanilla JS `doSearch`
  | `main.go` — `handleSearch` renders HTML partial instead of JSON
  | `main_test.go` — `TestHandleSearch` checks for HTML output
- Slice 3: Form POST + Alpine.js — generated
  | `templates/partials/feed_result.html` — new partial with Alpine.js copy button + QR code
  | `templates/feed_location.html` — refactored to use `feed_result` partial, Alpine.js replaces legacy inline JS
- Slice 4: Subscriptions HTMX — generated
  | `templates/partials/subscriptions.html` — new partial for subscriptions list
  | `templates/index.html` — added subscriptions UI with `hx-get` button
  | `main.go` — `handleSubscriptions` renders HTML partial instead of JSON
  | `main_test.go` — `TestHandleSubscriptions` checks for HTML output

## References
- `.rpiv/artifacts/research/2026-05-26_15-46-08_htmx-alpine-frontend-improvement.md`
- `main.go`
- `templates/index.html`
- `templates/feed_location.html`
- `main_test.go`
- `static/style.css`
- `config.go`
