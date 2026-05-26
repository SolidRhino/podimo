# Podimo-to-RSS — Design Specification

> A self-hosted tool that turns paywalled podcasts into standard RSS feeds.

---

## Product Surface

Two server-rendered HTML pages with inline CSS and JS. No external component library, no build pipeline, no static assets. Templates are embedded at compile time via Go's `//go:embed`.

| Page | Route | Purpose |
|------|-------|---------|
| Index | `/` | Form: search, podcast ID input, region/locale selectors |
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
| Page title (H1) | Newsreader / Bodoni Moda | 600 | `clamp(1.75rem, 1.5rem + 1.25vw, 2.5rem)` | 1.15 |
| Section title (H2) | Newsreader / Bodoni Moda | 600 | `clamp(1.25rem, 1.1rem + 0.75vw, 1.75rem)` | 1.2 |
| Body | system-ui, sans-serif | 400 | `clamp(1rem, 0.909rem + 0.45vw, 1.125rem)` | 1.75 |
| Labels / UI | system-ui, sans-serif | 500 | `1rem` | 1.5 |
| Small / captions | system-ui, sans-serif | 400 | `0.875rem` | 1.5 |

> **Display serif** for headings (character, editorial weight). **Sans-serif** for body and UI (legibility, neutrality). If Newsreader / Bodoni Moda are unavailable, fall back to a local serif with similar contrast.

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

## Component Patterns

### Form Inputs

```
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

```
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

### Details / Accordion (SSO help)

```
background: transparent;
border: 1px solid var(--border);
border-radius: 0.375rem;
```

- `summary` has `padding: 1rem`, subtle background (`var(--surface-elevated)`)
- Content area (`ol`) has `padding: 1rem 1.5rem`
- Avoid the grey-on-grey block look — use a clean bordered outline

### Warning Banner

```
background: var(--error-bg);
color: var(--error-text);
border-left: 4px solid var(--error-text);
border-radius: 0.25rem;
padding: 1rem 1.25rem;
margin-bottom: 1.5rem;
```

- Left accent border draws attention without being garish
- Clean, modern alert style

### Search Results List

```
list-style: none;
padding: 0;
margin: 0;
border: 1px solid var(--border);
border-radius: 0.375rem;
overflow: hidden;
```

- Items separated by `border-bottom: 1px solid var(--border)`
- Hover: background `var(--surface-elevated)`
- Cover images: `border-radius: 0.25rem`, `object-fit: cover`
- Clickable with real pointer cursor

### Footer / Info Bar

```
border-top: 1px solid var(--border);
padding: 1.5rem 0;
margin-top: 2.5rem;
color: var(--text-muted);
font-size: 0.875rem;
```

- No background color — integrate into the page flow
- Links use `--accent` with underline on hover

---

## Layout

### Single Column, Centered

```
max-width: 70ch;
margin-inline: auto;
padding: 3rem 1.5rem;
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

Triggered by `prefers-color-scheme: dark`.

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
| `templates/index.html` | Main form page — search, podcast ID, region/locale, all inline CSS/JS |
| `templates/feed_location.html` | Result page — feed URL, copy button, QR code, light inline CSS |

Both templates are **complete standalone HTML5 documents**. No shared layout, no partials, no `base.html`. Any shared design token must be copied between files until the project moves to an asset pipeline.
