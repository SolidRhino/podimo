# templates/ — Presentation Layer

## Responsibility
Server-rendered HTML for the web UI. Two full-page templates and three partials parsed via `//go:embed` at build time. No template inheritance, no static asset pipeline, no CSRF tokens. Shared stylesheet at `static/style.css`.

## Dependencies
- Go `html/template` + `embed` from the standard library.
- HTMX 2.0.4, Alpine.js 3.14.8, Newsreader Google Font, qrcode.min.js CDN.

## Consumers
- `main.go` parses templates on startup into `*template.Template` fields on `App`.
- Handlers render via `ExecuteTemplate(w, name, data)` with `map[string]interface{}` view models.

## Module Structure
```
templates/
├── index.html              → GET `/` form, search UI, subscriptions, conditional fields
├── feed_location.html      → POST `/` result page with QR code + copy button
└── partials/
    ├── feed_result.html    → Named template block — feed URL, copy button (Alpine), QR code
    ├── search_results.html → HTMX fragment — podcast search results
    └── subscriptions.html  → HTMX fragment — followed podcasts

static/
└── style.css               → Shared stylesheet with CSS custom properties + dark mode
```

## Build-Time Embedding
Templates and static assets are embedded with `//go:embed` and parsed once at boot. Syntax errors are boot-time failures (`os.Exit`).

```go
//go:embed templates/*
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

tmpl, err := template.ParseFS(templatesFS,
    "templates/index.html",
    "templates/feed_location.html",
    "templates/partials/*.html")
```

## Template Types

**Full pages** — complete HTML5 documents with shared `<head>` assets (HTMX, Alpine.js, Newsreader font, `style.css`). Parsed explicitly by filename.

**Partials** — two flavors:
- **Standalone fragments** (`search_results.html`, `subscriptions.html`): rendered by filename for HTMX swaps. No `<html>` wrapper.
- **Named blocks** (`feed_result.html`): `{{ define "feed_result" }}` invoked via `{{ template "feed_result" . }}` inside `feed_location.html`.

## HTMX + Alpine.js Hybrid
- HTMX owns network requests and DOM replacement (`hx-get`, `hx-target`).
- Alpine.js owns local UI state (`x-data`, `x-init`, `@click`).
- The two frameworks do not directly interact; Alpine initializes via a global mutation observer, so HTMX-swapped fragments work automatically.
- Form credentials are forwarded to HTMX requests via an inline `htmx:configRequest` listener that injects a `Basic` Authorization header.

## Dark Mode
Triggered by `prefers-color-scheme: dark` via the `@media` rule in `static/style.css`. No JavaScript toggle.

<important if="you are adding a new page template">
1. Create `templates/<page>.html` as a complete standalone HTML5 document.
2. Copy the standard `<head>` from `index.html` (meta, viewport, fonts, `style.css`, HTMX, Alpine).
3. Add the file path to `template.ParseFS` in `main.go` startup and in `setupTestApp`.
4. Add a handler method on `*App` and wire the route in `setupRoutes()`.
5. Add a test in `main_test.go` that parses the new template alongside the others.
</important>

<important if="you are adding a new partial template">
1. **Standalone fragment** (HTMX swap): create `templates/partials/<name>.html` and render by filename.
2. **Named block** (embedded in multiple pages): create `templates/partials/<name>.html` with `{{ define "<name>" }}` and invoke via `{{ template "<name>" . }}`.
3. Never include `<html>` or `<body>`; output only the markup fragment.
4. Use the three-branch structure for data partials: `{{ if .Error }} ... {{ else if not .Results }} ... {{ else }} ... {{ end }}`.
5. Set `Content-Type: text/html; charset=utf-8` in the handler before `ExecuteTemplate`.
</important>

<important if="you are adding a new CSS component">
1. Open `static/style.css` and add the component near related styles.
2. Use only CSS custom properties for colors, spacing, and transitions.
3. Include `:hover` and `:focus` states for interactive elements.
4. Verify dark mode by checking that every property references a token with a dark override.
</important>

<important if="you are adding a new form field">
1. Add `<input name="field">` inside `index.html`.
2. In `handleIndex` POST, read with `r.FormValue("field")` and validate.
3. On error, preserve the submitted value in the re-render data map.
</important>
