# templates/ — Presentation Layer

## Responsibility
Server-rendered Jinja2 HTML for the Quart web UI. Two user-facing pages (`index.html`, `feed_location.html`) plus a single base layout. No static asset pipeline; all CSS and JS are inline.

## Dependencies
- Quart's built-in Jinja2 environment (`render_template`)
- CDN script `qrcode.min.js` loaded by `feed_location.html`

## Consumers
- `main.py` route handlers render templates via `render_template`
- No other modules reference templates

## Module Structure
```
templates/
└── *.html   # Page templates (flat, no subdirectories)
```

| File | Role |
|------|------|
| `base.html` | Root layout with `{% block body %}` |
| `index.html` | Form + search UI (extends `base.html`) |
| `feed_location.html` | Result page with QR code (extends `base.html`) |

## Base Template Inheritance
All pages extend `base.html` and override only the `body` block.

```html
<!-- templates/base.html -->
<!doctype html>
<html>
<head>
  <title>Podimo-to-RSS converter</title>
  <meta name="robots" content="noindex, nofollow">
  <style>/* inline global CSS */</style>
</head>
<body>
  {% block body %}{% endblock %}
</body>
</html>
```

## Context-Driven Conditional Rendering
Flags passed from route handlers control UI sections.

```html
{% if error %}
<h2>Error: {{ error }}</h2>
{% endif %}
```

## Inline JavaScript Bootstrapping
Server-side values are interpolated directly into `<script>` blocks.

```html
<script>
  const url = "{{ url }}";
  // Jinja2 conditionals may emit entire JS blocks:
  {% if need_credentials %}
  const email = document.getElementById('email').value;
  {% endif %}
</script>
```

## Architectural Boundaries
- **No static directory** — there is no `static/` folder; assets are inline or CDN.
- **No CSRF protection** — form POSTs lack CSRF tokens.
- **Single inheritance depth** — only `base.html` → page template.

<important if="you are adding a new page template">
1. Create `templates/page_name.html`
2. Start with `{% extends "base.html" %}` and define `{% block body %}`
3. Add the Quart route in `main.py` returning `await render_template("page_name.html", ...)`
4. Pass serializable values only; no complex objects
</important>

<important if="you are adding conditional UI to an existing template">
1. Pass the boolean flag from the route handler
2. Wrap markup in `{% if flag %}...{% endif %}`
3. Ensure the variable is defined in every `render_template` call for that template
</important>
