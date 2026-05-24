# templates/ — Presentation Layer

## Responsibility
Server-rendered HTML for the web UI. Two standalone page templates parsed via `//go:embed` at build time. No template inheritance, no static asset pipeline, no CSRF tokens.

## Dependencies
- Go `html/template` + `embed` from the standard library.
- `qrcode.min.js` CDN in `feed_location.html`.

## Consumers
- `main.go` parses templates on startup into `*template.Template` fields on `App`.
- Handlers render via `Execute(w, data)` with `map[string]interface{}` view models.

## Module Structure
```
templates/
├── index.html          → GET `/` form, search UI, conditional fields
└── feed_location.html  → POST `/` result page with QR code + copy button
```

## Build-Time Embedding
Templates are embedded with `//go:embed` and parsed once at boot. Syntax errors are boot-time failures (`os.Exit`).

```go
//go:embed templates/*
var templatesFS embed.FS

tmpl, err := template.ParseFS(templatesFS, "templates/index.html")
```

## Self-Contained Pages
Each template is a complete standalone HTML5 document with inline CSS and JS. There is no `base.html`, no partials, and no shared layout. CSS changes must be copied to every template that uses them.

## Form Post-Back with Conditional Re-Render
`handleIndex` serves both GET and POST. On validation errors, `index.html` is re-rendered with `.Error`; on success, `feed_location.html` is rendered with `.URL`.

```go
data := map[string]interface{}{
    "Regions":         a.cfg.Regions,
    "Locales":         a.cfg.Locales,
    "NeedCredentials": !a.cfg.LocalCredentials,
}
```

## Server-to-Client JS Injection
Server values interpolate directly into `<script>` blocks. `html/template` auto-escapes string interpolations to prevent XSS.

```html
<script>
  const needAuth = {{ .NeedCredentials }}; // boolean
  const endpoint = "{{ .SearchEndpoint }}";
</script>
```

<important if="you are adding a new page template">
1. Create `templates/<page>.html` as a complete standalone HTML5 document with inline CSS.
2. Add `//go:embed` + `template.ParseFS` in `main.go` startup; store the parsed template on `App`.

3. Add a handler method on `*App` and wire the route in `setupRoutes()`.
</important>

<important if="you are adding a new form field">
1. Add `<input name="field">` inside `index.html`.
2. In `handleIndex` POST, read with `r.FormValue("field")` and validate.
3. On error, preserve the submitted value in the re-render data map.
</important>
