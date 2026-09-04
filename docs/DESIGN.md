# MagicWebb design system (v3.5, spec B0)

Source of truth: `app/src/styles/tokens.css` (tokens), `app/src/styles/base.css`
(reset, type, `.btn`, forms, focus, tab bar, banners), `app/src/lib/icons.ts`
(icon set). Components reference variables only — no raw hex in new code.

Principles: **don't make me think** (self-evident to someone who has never used
a wallet) · **empty states are features** · **subtraction** (one header, one
sort, one CTA) · **trust at the pixel** (fees, refunds, next step visible
before signing) · **calm app UI** · **44px everywhere**.

## Tokens

| Group | Token | Dark | Notes |
|---|---|---|---|
| Surface | `--bg` | `#09090b` | page |
| | `--surface` | `#0f0f13` | cards, inputs |
| | `--surface-2` | `#16161c` | menus, toasts, skeleton base |
| | `--line` / `--line-strong` | `rgba(255,255,255,.08)` / `.16` | borders |
| Text | `--text` | `#f4f4f5` | |
| | `--text-2` | `rgba(255,255,255,.72)` | body secondary (≥4.5:1) |
| | `--text-3` | `rgba(255,255,255,.56)` | captions ≥12px only; **never below .56 for text** |
| Accent | `--sky` `#7dd3fc` | primary / listings | `--gold` `#fcd34d` price / offers |
| | `--violet` `#a78bfa` auctions | `--green` `#4ade80` success / live | `--red` `#fca5a5` error · `--amber` `#fbbf24` creator |
| Tints | `--sky-12`, `--sky-35`, `--gold-12`, … | 12 % fills, 35 % borders for pills | |
| Spacing | `--sp-1…16` | 4 8 12 16 24 32 48 64 | 4-pt grid |
| Radius | `--r-control` 8 · `--r-card` 12 · `--r-pill` 999 | | |
| Shadow | `--shadow` | `0 8px 24px rgba(0,0,0,.35)` | menus/modals only; cards use border |
| Motion | `--dur-fast` 120ms · `--dur` 200ms · `--ease` `cubic-bezier(.2,.8,.2,1)` | | |
| Layout | `--header-h` 60 (56 mobile) · `--tabbar-h` 60 · `--hit` 44 | | |
| z-index | header 40 · banner 30 · drawer 50 · modal 60 · toast 70 | `--z-*` | |

Legacy aliases (`--ink-950`, `--sky-300`, `--white-60`, …) map onto the new
tokens so unmigrated pages keep rendering; delete them when the last page moves.

**Light theme** is tokens only (`prefers-color-scheme: light` in tokens.css);
no toggle in v1.

## Type

Inter (self-hosted via `@fontsource/inter`, latin, weights 400–800,
`font-display: swap`) and JetBrains Mono for addresses/prices via the single
`.mono` utility.

| Role | Size/line | Class |
|---|---|---|
| Display | 44/48 (mobile 32/36) | `.t-display` |
| H1 | 28/34 | `h1`, `.t-h1` |
| H2 | 22/28 | `h2`, `.t-h2` |
| H3 | 17/24 | `h3`, `.t-h3` |
| Body | 15/22 | default, `.t-body` |
| Small | 13/18 | `.t-small` |
| Caption | 12/16, `--text-3` | `.t-caption` (+ `.upper` = uppercase, .04em) |

Never 9–11px text.

## Buttons — one system

`.btn` + variant + optional size. md = 40px, `.btn-lg` = 48px, `.btn-sm` = 36px;
**≤640px every `.btn` is ≥44px**.

- `.btn-primary` sky fill, ink text
- `.btn-secondary` 1px `--line-strong` + text
- `.btn-ghost` text only
- `.btn-danger` red outline

Hover lightens 8 %, active scales .98, disabled = opacity .5 + `aria-disabled`
+ reason tooltip (`title` or a `Hint`). No underlines on buttons.
`.icon-btn` is the 44×44 icon-only control. `.btn-sky/.btn-gold/.btn-violet`
are aliases for the hero until index.astro migrates; the per-component
`.btn.p/.v/.g/.gold` copies are scheduled for deletion.

## Inputs

44px tall (40 allowed on desktop), 15px text, label **above** (13px
`--text-2`), helper/error line below (13px), border `--line-strong`, focus
ring, no placeholder-as-label. Markup: `<div class="field"><label>…</label>
<input><p class="help">…</p></div>`.

## Focus

`:focus-visible { outline: 2px solid var(--sky); outline-offset: 2px }` on
links, buttons, inputs, selects, cards, tabs, menu items. Skip link
(`.skip-link`, "Skip to content") is the first focusable element.

## Icons

Inline SVG, Lucide subset, 24-unit viewBox drawn at 20px, `currentColor`:
collection, image, user, wallet, gavel, tag, search, chart, inbox, check, x,
chevron-down/right, external, copy, refresh, info, bell, menu, home, alert,
book. Svelte: `<Icon name="tag" />`; string HTML: `iconSvg('tag')`.
No emoji in UI chrome.

## Components

- `Skeleton.svelte` — one gradient sweep (1.2s); `card` prop = image + 3 lines.
  Grids show 8 (desktop) / 4 (mobile) placeholders.
- `EmptyState.svelte` — icon 32, title 17/600, body 15 `--text-2`, primary
  `.btn`, optional secondary link. Every empty state has a next action.
- `ErrorState.svelte` — red tint card, `role=alert`, Retry.
- `Toasts.svelte` + `lib/toast.svelte.ts` — bottom centre (above tab bar on
  mobile), max 3, 4s (errors 8s + close), `role=status aria-live=polite`.
  From inline scripts: `window.dispatchEvent(new CustomEvent('mw-toast',
  {detail:{message, variant}}))`.
- `Hint.svelte` — `i` button (44px hit) with `aria-describedby` popover;
  click/focus opens, Escape/outside closes. Never `title=` only.
- Cards: hover lift 2px + border glow; `<a>`-wrapped with an inner sibling
  `<button>` overlay for the action.

## Number formats (`lib/format.ts`)

- price `fmtPrice(wei)` → `12.5` (2 dp max, zeros trimmed); `fmtAmount(wei,
  currency)` → `12.5 C2FLR`
- address `shortAddr(a)` → `0x1408…9f91` (6+4) + copy button
- relative time `timeAgo` → `2m ago`
- countdown `fmtCountdownShort` → `1d 03h` / `5h 12m` / `02:14:09` under 1h;
  `countdownUrgent` = red under 3 min

## Motion

Allowed: card hover lift, button active scale, `.reveal` fade + 8px rise,
toast slide 200ms, countdown colour change, skeleton shimmer 1.2s. Removed:
hero parallax off the home page, document-level tilt, floating blobs. All
honour `prefers-reduced-motion`.

## Shell (B1)

Header 60/56px: wordmark 18/700 · nav 14px with 44px hit areas, one active
colour (text + 2px sky underline) · network pill (green dot = trading live,
grey = browse-only; `role=menu`, arrow keys, Escape, current item checked and
disabled; switching keeps `pathname + search` and toasts "Switching to X…")
· bell 44px (badge hidden at 0) · wallet ("Connect" ≤480px). Mobile drawer
covers the tab bar, ✕ 44px, focus trap, Escape. Bottom tab bar: Home ·
Listings · Auctions · Offers · Profile; `main` pads for it. Banners: testnet
(faucet link from `MW_FAUCET_URL`) and browse-only ([Try Coston2] [Why?]),
dismissible per session. Footer: Docs · GitHub · Status. 404 page:
`app/src/pages/404.astro`.
