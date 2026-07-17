# Podimo-to-RSS — Design Specification

> A self-hosted tool that turns paywalled podcasts into standard RSS feeds.

---

## Product Surface

Two server-rendered HTML pages with a shared external stylesheet. No external component library, no build pipeline. Templates and static assets are embedded at compile time via Go's `//go:embed`.

| Page | Route | Purpose |
|------|-------|---------|
| Index | `/` | Form: search, podcast ID input, region/locale selectors, subscriptions |
| Feed Location | `/` (POST result) | Shows generated RSS URL with copy button and QR code |

---

## Design Principles

1. **Restraint is the aesthetic** — every pixel earns its place; nothing decorative without purpose.
2. **Type does the work** — hierarchy through weight, size, and spacing, not ornament.
3. **Function first** — the interface is a tool, not a product. It should feel modern, confident, and quietly premium without ever being showy.
4. **No gimmicks** — no scroll animations, no reveal choreography, no decorative textures. The interface is remembered for *not* trying to be remembered.

---

## Visual Language

### Tone

**Editorial / magazine spread** — like a well-typeset article or a confident modern publication. Aligned, breathable, intentional.

### Color Palette

| Token | Light Mode | Dark Mode | Usage |
|-------|-----------|-----------|-------|
| `--surface` | `#faf9f7` | `#121212` | Page background |
| `--surface-elevated` | `#ffffff` | `#1e1e1e` | Inputs, buttons, cards |
| `--text-primary` | `#1a1a1a` | `#e8e6e1` | Headings, body text |
| `--text-secondary` | `#555555` | `#a0a0a0` | Labels, small text |
| `--text-muted` | `#888888` | `#666666` | Placeholders, hints |
| `--accent` | `#b45309` | `#d97706` | Submit button, links, focus rings |
| `--accent-hover` | `#92400e` | `#f59e0b` | Button hover state |
| `--border` | `#e5e5e5` | `#333333` | Input borders, dividers |
| `--error-bg` | `#fef2f2` | `#3a1515` | Error/warning banner background |
| `--error-text` | `#991b1b` | `#fca5a5` | Error/warning banner text |

> Warm off-white ground in light mode; warm dark grays in dark mode. Avoid pure `#000` and `#fff` — they are too harsh for a reading experience.

### Typography

| Role | Font | Weight | Size | Line Height |
|------|------|--------|------|-------------|
| Page title (H1) | Newsreader | 600 | `clamp(1.75rem, 1.5rem + 1.25vw, 2.5rem)` | 1.15 |
| Section title (H2) | Newsreader | 600 | `clamp(1.25rem, 1.1rem + 0.75vw, 1.75rem)` | 1.2 |
| Body | system-ui, sans-serif | 400 | `clamp(1rem, 0.909rem + 0.45vw, 1.125rem)` | 1.75 |
| Labels / UI | system-ui, sans-serif | 500 | `1rem` | 1.5 |
| Small / captions | system-ui, sans-serif | 400 | `0.875rem` | 1.5 |

> **Display serif** for headings (character, editorial weight). **Sans-serif** for body and UI (legibility, neutrality). Newsreader is self-hosted (`/static/fonts.css` + `/static/newsreader-400.ttf` + `/static/newsreader-600.ttf`).

> Never use `system-ui` for display / heading text.

### Spacing

| Token | Value | Usage |
|-------|-------|-------|
| `--space-xs` | `0.5rem` | Inline gaps, tight padding |
| `--space-sm` | `0.75rem` | Input padding, label-to-input gap |
| `--space-md` | `1rem` | Section internal padding |
| `--space-lg` | `1.5rem` | Between form sections |
| `--space-xl` | `2.5rem` | Page top margin, major section separations |
| `--space-2xl` | `4rem` | Page max horizontal padding |

> Form max-width: `70ch` (tighter than a typical article). Centered with auto margins. Comfortable `margin-top` so the form doesn't sit flush against the top edge.

---

## Motion

**Subtle micro-interactions only.**

- `transition: color 150ms ease, background-color 150ms ease, border-color 150ms ease, box-shadow 150ms ease`
- Button hover: background darkens to `--accent-hover`
- Input focus: `box-shadow: 0 0 0 2px var(--accent)` ring, border to `--accent`
- No scroll animations, no page-load choreography, no hover effects on non-interactive elements

---

## Architecture

### Shared Stylesheet

All pages link to a single shared stylesheet served from `/static/style.css`:

```html
<link rel="stylesheet" href="/static/style.css">
```

The stylesheet defines the full design token system (CSS custom properties) and all component styles. It is embedded via `//go:embed static/*` and served by the Go static file server at runtime.

### HTMX + Alpine.js

The frontend uses **HTMX** for server-driven interactivity (search, subscriptions) and **Alpine.js** for client-side state (copy-to-clipboard feedback, QR code rendering).

- HTMX `2.0.4` — self-hosted at `/static/htmx.min.js`
- Alpine.js `3.14.8` — self-hosted at `/static/alpine.min.js` (with `defer`)
- `qrcode.min.js` `1.0.0` — self-hosted at `/static/qrcode.min.js` for QR generation

No build step, no bundler. All libraries are self-hosted under `/static/` — no external CDN requests.

### Templates

| File | What it contains |
|------|-------------------|
| `templates/index.html` | Main form page — search, podcast ID, region/locale, subscriptions |
| `templates/feed_location.html` | Result page — feed URL, copy button, QR code |
| `templates/partials/feed_result.html` | Partial — feed URL display, copy button (Alpine), QR code |
| `templates/partials/search_results.html` | Partial — HTMX search results list |
| `templates/partials/subscriptions.html` | Partial — HTMX subscriptions list |

Templates are complete standalone HTML5 documents (for the two pages) or Go template `define` blocks (for partials). Partials are rendered server-side and swapped into the DOM by HTMX.

---

## Component Patterns

### Form Inputs

```css
border: 1px solid var(--border);
border-radius: 0.375rem;   /* 6px — subtle, not pill */
padding: 0.75rem 1rem;
background: var(--surface-elevated);
color: var(--text-primary);
width: 100%;
```

- Border transitions to `--accent` on focus
- Invalid state: border red, warning icon (subtle, not alarming)

### Submit Button

```css
background: var(--accent);
color: #ffffff;
border: none;
border-radius: 0.375rem;
padding: 0.875rem 1.5rem;
font-weight: 500;
cursor: pointer;
```

- Full width within the form column
- Hover: `--accent-hover`
- Disabled (form invalid): opacity 0.4, cursor not-allowed

### Secondary Button

```css
.btn-secondary {
  background: var(--surface-elevated);
  color: var(--text-secondary);
  border: 1px solid var(--border);
}
.btn-secondary:hover {
  background: var(--surface);
  color: var(--text-primary);
}
```

Used for HTMX trigger buttons (search, subscriptions).

### Details / Accordion (SSO help)

```css
background: transparent;
border: 1px solid var(--border);
border-radius: 0.375rem;
```

- `summary` has `padding: 1rem`, subtle background (`var(--surface-elevated)`)
- Content area (`ol`) has `padding: 1rem 1.5rem`
- Avoid the grey-on-grey block look — use a clean bordered outline

### Warning Banner

```css
.banner-warning {
  background: var(--error-bg);
  color: var(--error-text);
  border-left: 4px solid var(--error-text);
  border-radius: 0.25rem;
  padding: 1rem 1.25rem;
  margin-bottom: 1.5rem;
}
```

- Left accent border draws attention without being garish
- Clean, modern alert style

### Search / Subscription Results List

```css
.search-results ul {
  list-style: none;
  padding: 0;
  margin: 0;
  border: 1px solid var(--border);
  border-radius: 0.375rem;
  overflow: hidden;
}
.search-results li {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  padding: var(--space-sm) var(--space-md);
  cursor: pointer;
  border-bottom: 1px solid var(--border);
}
.search-results li:last-child {
  border-bottom: none;
}
.search-results li:hover {
  background: var(--surface-elevated);
}
.search-results li img {
  border-radius: 0.25rem;
  object-fit: cover;
  flex-shrink: 0;
}
```

- Items separated by `border-bottom`
- Hover: background `var(--surface-elevated)`
- Cover images: `border-radius: 0.25rem`, `object-fit: cover`
- Clickable with real pointer cursor; `onclick` invokes `window.selectPodcast(id)`

### Copy-to-Clipboard Button (Alpine.js)

```html
<div x-data="{ copied: false }">
  <button @click="navigator.clipboard.writeText(url).then(() => { copied = true; setTimeout(() => copied = false, 2000) })">
    <span x-text="copied ? 'Copied!' : 'Copy to clipboard'"></span>
  </button>
</div>
```

- Alpine.js `x-data` tracks `copied` state
- Button text swaps for 2 seconds after successful copy

### QR Code

```css
.qrcode {
  margin: var(--space-lg) 0;
}
.qrcode img {
  display: block;
  border: 1px solid var(--border);
  border-radius: 0.375rem;
}
```

- Rendered by `qrcode.min.js` via Alpine.js `x-init`
- Container uses `x-data x-init="new QRCode($el, { text: url, width: 200, height: 200 })"`

### Footer / Info Bar

```css
footer {
  border-top: 1px solid var(--border);
  padding: 1.5rem 0;
  margin-top: 2.5rem;
  color: var(--text-muted);
  font-size: 0.875rem;
}
```

- No background color — integrate into the page flow
- Links use `--accent` with underline on hover

---

## Layout

### Single Column, Centered

```css
body {
  max-width: 70ch;
  margin: 0 auto;
  padding: var(--space-xl) var(--space-lg);
}
```

- Generous vertical whitespace. Sections breathe.
- No sidebar, no grid-breaking, no asymmetry on the main axis — but content alignment is precise, not default-center lazy.

### Responsive Behavior

- `padding: calc(1vmin + 0.5rem)` adapts slightly to viewport
- Font sizes use `clamp()` for smooth scaling
- Inputs and buttons remain full-width within the column
- No dedicated mobile breakpoint — the narrow column + fluid typography handles all viewports

---

## Dark Mode

Triggered by `prefers-color-scheme: dark` via the `@media` rule in `static/style.css`.

- Never pure `#000` — use `#121212` for comfort
- Elevated surfaces at `#1e1e1e`
- Accent turns slightly lighter / warmer for sufficient contrast against dark backgrounds
- Borders soften (`#333333`)

---

## Accessibility

- Focus rings on **all** interactive elements (`box-shadow: 0 0 0 2px var(--accent)`)
- Form inputs have explicit `<label>` elements
- Error messages are clear, inline, and adjacent to relevant fields
- Color contrast meets WCAG AA (4.5:1 for body text, 3:1 for large text / UI)
- No motion that could trigger vestibular disorders (`prefers-reduced-motion` respected implicitly by avoiding animation)

---

## Anti-Patterns (Never)

- `system-ui` as a display / heading font
- Purple-to-blue gradients, generic "SaaS blue" (#3B82F6), pastel palettes
- Centered card stacks, `max-w-7xl mx-auto` on every section
- Identical `rounded-xl shadow-md` cards, generic ghost buttons
- Fade-in on scroll, bounce easings, scattered micro-interactions
- Styled `<div>` or `<span>` pretending to be buttons or links
- Decorative noise, grain, mesh gradients, or geometric patterns — this design uses flat solids only

---

## Files

| File | What it contains |
|------|-------------------|
| `static/style.css` | Shared stylesheet — design tokens, component styles, dark mode |
| `templates/index.html` | Main form page — search, podcast ID, region/locale, subscriptions |
| `templates/feed_location.html` | Result page — feed URL, copy button, QR code |
| `templates/partials/feed_result.html` | Partial — feed URL, copy button (Alpine), QR code |
| `templates/partials/search_results.html` | Partial — HTMX search results list |
| `templates/partials/subscriptions.html` | Partial — HTMX subscriptions list |

Both page templates link to `/static/fonts.css` and `/static/style.css` and load HTMX, Alpine.js, and QRCode.js from `/static/`. Partials are server-rendered snippets swapped into the DOM by HTMX.
