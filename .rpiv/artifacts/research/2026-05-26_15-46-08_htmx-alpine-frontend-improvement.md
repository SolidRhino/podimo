---
date: 2026-05-26T15:46:08+0200
author: SolidRhino
commit: e3733b2
branch: go-rewrite
repository: podimo
topic: "HTMX and Alpine.js frontend improvement"
tags: [research, frontend, htmx, alpinejs, templates, go]
status: complete
last_updated: 2026-05-26T15:46:08+0200
last_updated_by: SolidRhino
---

# Research: HTMX and Alpine.js Frontend Improvement

## Research Question
How can the Podimo RSS Go web application frontend be improved by integrating HTMX and Alpine.js (loaded via CDN) to replace vanilla JS interactivity, surface hidden features like subscriptions, and fix existing static asset serving bugs?

## Summary

The frontend consists of two standalone HTML templates (`templates/index.html`, `templates/feed_location.html`) and a CSS file (`static/style.css`), all embedded via `//go:embed`. The current interactivity is minimal: a vanilla JS `fetch()` search in `index.html`, inline copy-to-clipboard/QR code scripts in `feed_location.html`, and a JSON-only `/subscriptions` endpoint with no UI. Four critical findings emerged:

1. **`staticFS` serving is broken** — `//go:embed static/*` at `main.go:27` attaches to `templatesFS`, not `staticFS`. The `/static/*` route serves an empty FS, so `style.css` currently 404s.
2. **Search must return HTML partials** — `/search` currently returns JSON unconditionally. HTMX requires server-rendered fragment templates (e.g., `templates/partials/search_results.html`).
3. **Form POST returns full HTML documents** — `handleIndex` POST renders `feed_location.html`, a complete `<!doctype html>` page. HTMX swaps need fragment-only responses without `<html>` wrappers.
4. **Alpine.js and HTMX coexist safely** — Alpine's `x-data`, `@click`, `:class` replace imperative JS well, but inside HTMX-swapped fragments, `htmx:afterSwap` may need to call `Alpine.initTree()` for edge cases.

The developer confirmed full HTMX replacement (no vanilla JS backward compat), surfacing subscriptions in the UI, fixing the staticFS bug, and creating a partial templates directory.

## Detailed Findings

### 1. Broken Static Asset Serving (Critical Bug)

- `main.go:26-30` — `//go:embed templates/*` and `//go:embed static/*` both precede `var templatesFS embed.FS`. Go embed directives accumulate onto the **single variable declaration that directly follows them**.
- `var staticFS embed.FS` at `main.go:30` has **no preceding embed directives** — it is an empty `embed.FS`.
- `r.Handle("/static/*", http.FileServer(http.FS(staticFS)))` at `main.go:185` therefore returns `404` for every request under `/static/*`.
- **Impact:** `templates/index.html:11` (`<link rel="stylesheet" href="/static/style.css">`) and `templates/feed_location.html:11` both request a file that 404s. The application appears unstyled to users.
- **Fix:** Reorder directives so `//go:embed static/*` directly precedes `var staticFS embed.FS`. Add cache-control middleware to the file server for performance.

### 2. Search Endpoint — JSON to HTML Fragment

- `main.go:225-286` — `handleSearch` unconditionally calls `json.NewEncoder(w).Encode()` at `main.go:283-286`.
- `templates/index.html:104-152` — Vanilla JS `doSearch` builds a `URL`, embeds credentials into `url.username`/`url.password`, calls `fetch()`, parses JSON, and hand-builds HTML via `resultsDiv.innerHTML = html` string concatenation.
- **HTMX decomposition:**
  - Remove `doSearch()` function entirely.
  - Add `hx-get="/search"`, `hx-target="#searchResults"`, `hx-trigger="click from:#searchBtn, keydown[key=='Enter'] from:#search_query"` to the search input/button.
  - Credentials: HTMX `hx-get` cannot manipulate a `URL` object to embed Basic Auth. The browser auto-sends cached Basic Auth headers on same-origin HTMX requests. For explicit control, Alpine.js can compute the `Authorization` header and bind it via HTMX `hx-headers`.
- **Server change:** `handleSearch` must branch on `HX-Request` header to render a partial template (e.g., `templates/partials/search_results.html`) with `text/html; charset=utf-8` instead of JSON.
- **New partial template needed:** Contains the `<ul>` list of podcasts with `onclick="selectPodcast('{{ .ID }}')"` (or Alpine.js `@click`), cover images, title, and author.

### 3. Main Form POST — Full Page to Fragment Swap

- `templates/index.html:95` — `<form action="./" method="post">` performs a traditional full-page POST.
- `main.go:536-581` — `handleIndex` POST branch builds the feed URL and calls `renderFeedLocation(w, r, feedURL)`.
- `main.go:610-614` — `renderFeedLocation` executes `feedTmpl` (`templates/feed_location.html`), a **complete HTML document** with `<!doctype html>`, `<head>`, and `<body>`.
- **HTMX issue:** HTMX swaps the response into a target element. If the server returns a full HTML document, HTMX must extract the body content. Inline `<script>` tags inside swapped content do **not** re-execute for security reasons.
- **Fix:** Add `hx-post="/"`, `hx-target="#result"` to the form in `index.html`. Modify `renderFeedLocation` (or add a new helper) to check `HX-Request` and render a fragment template (e.g., `templates/partials/feed_result.html`) instead of the full page.
- **Fragment template:** Contains only the result markup (feed URL, copy button, QR code container) without `<html>` wrapper. The QR code library (`qrcode.min.js`) must be present in the persistent page `<head>`, and Alpine.js directives must replace the inline QR-code and copy-button scripts.

### 4. Subscriptions — JSON-Only to HTMX UI

- `main.go:189` — `r.With(a.rateLimitMiddleware).Get("/subscriptions", a.handleSubscriptions)`
- `main.go:290-323` — `handleSubscriptions` returns JSON unconditionally via `json.NewEncoder(w).Encode()`.
- **No frontend consumer** exists in `templates/index.html`.
- **HTMX integration:** Add a "📑 Your Subscriptions" section in `index.html` with a button using `hx-get="/subscriptions"`, `hx-target="#subscription-results"`, `hx-include="#region, #locale"`.
- **Server change:** `handleSubscriptions` must branch on `HX-Request` to render a partial template (e.g., `templates/partials/subscriptions.html`) instead of JSON.
- **Auth for HTMX:** In non-local mode, the browser automatically sends cached Basic Auth headers with HTMX XHRs. In local mode, `region` and `locale` come from the `hx-include` selects.
- **Edge case:** A `401` response does not trigger a browser Basic Auth prompt for XHRs. HTMX would fire `htmx:responseError`. Ensure users authenticate before HTMX requests (e.g., page load requires auth).

### 5. Alpine.js Copy-to-Clipboard

- `templates/feed_location.html:26-50` — `setButtonDiv()` injects button + feedback span via `innerHTML`. `copy()` toggles CSS class `.visible`.
- `static/style.css:316-327` — `.copy-feedback` has `opacity: 0` + `transition`; `.copy-feedback.visible` has `opacity: 1`.
- **Alpine.js replacement:**
  - Replace empty `<div id="button"></div>` with:
    ```html
    <div x-data="{ copied: false, url: '{{ .URL }}' }"
         x-init="$watch('copied', v => v && setTimeout(() => copied = false, 2000))">
      <button @click="navigator.clipboard.writeText(url).then(() => copied = true)">
        Copy to clipboard
      </button>
      <span :class="{ 'visible': copied }" class="copy-feedback">Copied!</span>
    </div>
    ```
  - **Keep CSS transitions** (hybrid approach recommended). Alpine toggles class; CSS handles `opacity` transition. Safer than `x-show` + `x-transition` inside HTMX-swapped fragments.
  - **Remove:** `setButtonDiv()`, `copy()`, and the global `const url` variable.
- **HTMX swap safety:** Alpine's MutationObserver detects new `x-data` nodes automatically. If edge cases arise, use `document.body.addEventListener('htmx:afterSwap', () => Alpine.initTree(evt.detail.target))`.

### 6. CDN vs Vendoring Decision

- The user explicitly requested CDN loading.
- CDN precedent exists: `templates/feed_location.html:26` already loads `qrcode.min.js` from `cdnjs.cloudflare.com`.
- **CDN advantages:** Zero `main.go` changes (avoids fixing staticFS for JS files), no binary bloat, CDN cache headers already optimal, consistent with existing QR code dependency.
- **Vendoring would require:** Fixing the staticFS embed bug + writing cache-control middleware. This is worthwhile for the CSS fix, but JS libraries can remain CDN.
- **Recommended `<head>` additions (both templates):**
  ```html
  <script src="https://unpkg.com/htmx.org@2.0.3" integrity="sha384-..." crossorigin="anonymous"></script>
  <script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"></script>
  ```

### 7. Template Parsing and Tests

- `main.go:114` — `template.ParseFS(templatesFS, "templates/index.html")` loads only one file.
- `main_test.go:39-68` — `setupTestApp` replicates the same parsing logic manually. Any new template field on `App` must be added here.
- **Partial template strategy:** Two approaches:
  1. **Glob parsing:** Change to `template.ParseFS(templatesFS, "templates/*.html", "templates/partials/*.html")`. Allows templates to reference partials via `{{template "search_results"}}`.
  2. **Separate fields:** Add `searchTmpl`, `subscriptionTmpl`, `feedResultTmpl` fields to `App`, each parsed individually.
- **Glob approach recommended** — simpler, less boilerplate, matches the standalone-document pattern (no Jinja2 inheritance). Go `text/template` supports named blocks via `{{define "name"}}`, but the simpler approach is parsing all files and calling individual templates by filename.
- **Test impact:** `setupTestApp` must parse all template files. Any syntax error in a new partial causes `t.Fatalf`. Add new tests that assert on fragment output (e.g., `strings.Contains(body, "<div class=\"search-results\"")`).

### 8. Go Template Conditionals vs Alpine.js

- `config.go:13-34` — `LocalCredentials` is a process-static constant (loaded from env var at startup, never changes at runtime).
- `main.go:585-588` — `NeedCredentials: !a.cfg.LocalCredentials` passed to template.
- `templates/index.html:35` and `templates/index.html:45` — `{{ if .NeedCredentials }}` conditionally renders banner and input fields.
- **Recommendation:** Keep Go `{{ if .NeedCredentials }}` for static config. `LocalCredentials` is not user-toggled; Alpine's reactivity is unnecessary. Removing the DOM server-side is correct — Alpine cannot reference elements that never arrived.
- **Caveat:** If a parent element has `x-data` and a child is wrapped in `{{ if }}`, HTMX-swapping that child in/out after Alpine initialization may leave stale scopes. Mitigation: call `Alpine.initTree()` in `htmx:afterSwap` for swapped elements that contain Alpine directives.
- **For new dynamic UI** (e.g., toggles, dropdowns added later): use Alpine directives and remove Go conditionals around them.

## Code References

- `main.go:26-30` — Broken embed directive binding (`staticFS` empty)
- `main.go:114` and `main.go:119` — Template parsing (single file each)
- `main.go:185` — Static file server route
- `main.go:189` — Subscriptions route registration
- `main.go:225-286` — `handleSearch` (JSON-only)
- `main.go:290-323` — `handleSubscriptions` (JSON-only)
- `main.go:536-581` — `handleIndex` POST branch
- `main.go:610-614` — `renderFeedLocation`
- `main.go:625-628` — `buildFeedURL`
- `main.go:667-669` — `authenticate` helper
- `templates/index.html:11` — Stylesheet link (404s due to bug)
- `templates/index.html:35`, `templates/index.html:45` — `{{ if .NeedCredentials }}`
- `templates/index.html:95` — Form POST action
- `templates/index.html:104-152` — Vanilla JS search `doSearch`
- `templates/feed_location.html:11` — Stylesheet link (404s due to bug)
- `templates/feed_location.html:26-50` — Inline scripts (QR code, copy-to-clipboard)
- `static/style.css:316-327` — Copy feedback CSS transitions
- `main_test.go:39-68` — `setupTestApp` template parsing replication
- `config.go:13-34` — `Config` struct with `LocalCredentials`

## Integration Points

### Inbound References
- `templates/index.html` — Consumes `/search` (JSON), `/` (POST full page). Will consume `/search` (HTML partials), `/subscriptions` (HTML partials), `/` (POST fragment).
- `templates/feed_location.html` — Consumed by `renderFeedLocation`. Will be consumed as both full page and fragment.

### Outbound Dependencies
- `/search` — Currently depends on `json.NewEncoder`. Will also depend on partial template execution.
- `/subscriptions` — Currently depends on `json.NewEncoder`. Will also depend on partial template execution.
- `/` (POST) — Currently depends on `feedTmpl`. Will also depend on fragment template execution.

### Infrastructure Wiring
- `main.go:26-30` — `//go:embed` directives must be reordered for `staticFS`.
- `main.go:185` — File server needs `Cache-Control` header wrapper.
- `main.go:74-75` — `App` struct needs new template fields (or glob parsing).

## Architecture Insights

- **Standalone templates pattern:** The Go rewrite intentionally abandoned Jinja2 inheritance (`templates/base.html` was deleted). New templates must be self-contained HTML5 documents or named template blocks. For HTMX fragments, named template blocks within a parsed set (`{{define "fragment"}}`) are the Go-idiomatic equivalent of partials.
- **No CSP or cache middleware:** The application currently sets no `Content-Security-Policy` or `Cache-Control` headers. Adding CDN scripts does not worsen the security posture (inline scripts already exist).
- **Basic Auth browser caching:** In non-local mode, the browser caches Basic Auth credentials on the first 401 challenge. Subsequent HTMX XHRs automatically carry the `Authorization` header without explicit JavaScript. This makes credential plumbing for HTMX simpler than the current `fetch()` URL-embedding approach.

## Precedents & Lessons

### Precedent: Extract inline CSS to shared external stylesheet
**Commit(s)**: `853af8d` — "feat(templates): extract inline CSS to shared external stylesheet with dark mode support"
**Blast radius**: 4 files (`main.go`, `static/style.css`, both templates)
**Lesson**: Static asset extraction is low-risk once the file server is wired, but the current wiring is broken (this research discovered the `staticFS` bug).

### Precedent: Add /search and /subscriptions endpoints
**Commit(s)**: `85ff27d` — "feat: add /search and /subscriptions endpoints"
**Follow-up fixes**: `ce5d33f`, `6764449` — Podimo GraphQL schema broke twice within 48 hours.
**Lesson**: Frontend interactivity JS is stable; backend API contracts break fast. Any frontend that depends on backend endpoints should include fallback/error handling.

### Precedent: Go rewrite — template migration
**Commit(s)**: `4539b58` — "feat: rewrite entire service from Python to Go"
**Lesson**: Go `text/template` does not support Jinja2 inheritance. Standalone documents or `{{define}}` blocks are the correct pattern. Any template change triggers a cascade of follow-ups (build, tests, dead-code cleanup).

### Composite Lessons
1. Frontend JS additions are stable with zero follow-up fixes (precedent `f1ba057` — QR code, copy button).
2. Backend API contracts break fast; include error states in HTMX partials.
3. Template changes require `//go:embed` vigilance — directives attach only to the immediately following variable.
4. Form actions must remain relative (`./`) to work under reverse proxies.

## Historical Context (from `.rpiv/artifacts/`)
- `.rpiv/artifacts/research/2026-05-24_21-05-47_improvement-opportunities-go-rewrite.md` — Documents frontend state and improvement opportunities.
- `.rpiv/artifacts/designs/2026-05-22_16-00-00_go-rewrite.md` — Documents the Go rewrite decision to use standalone templates via `//go:embed`.

## Developer Context

**Q (research): The search feature currently uses vanilla JS fetch(). With HTMX, should the search endpoint support both JSON (for backward compat) and HTML partials, or fully replace the vanilla JS with HTMX?**
A: Full HTMX replacement.

**Q (research): What else should be included in this frontend improvement? Select all that apply.**
A: Surface subscriptions UI, Fix staticFS embed bug, Create partial templates.

## Related Research
- `.rpiv/artifacts/research/2026-05-24_21-05-47_improvement-opportunities-go-rewrite.md` — General improvement opportunities for the Go rewrite.

## Open Questions
- None resolved at research stage. All ambiguities were resolved through developer checkpoint.
