// tailwind.config.cjs — MagicWebb — 5-color palette:
//   baby/sky blue · gold · black · purple · white
//
// Mirrors the runtime `tailwind.config = {…}` block that layout.html used
// to execute inline (now removed for SRI-friendliness). Token changes
// here MUST be paired with a `go run ./cmd/buildtailwindcss` rebuild
// — the compiled bundle at internal/ui/static/tailwind.css is what
// layout.html loads, not this file directly.
//
// Comments annotate palette roles so template authors can predict
// token meaning without scanning every utility. Adding a new class
// token to a page is free (Tree-shaken to used-only rules by Tailwind).

/** @type {import('tailwindcss').Config} */
module.exports = {
  theme: {
    extend: {
      fontFamily: {
        sans: ['Inter', 'system-ui', 'sans-serif'],
        mono:  ['JetBrains Mono', 'ui-monospace', 'monospace'],
      },
      colors: {
        // ── Sky / Baby Blue ──
        // The "primary" color. Reads at every luminance, on every
        // background. Lighter shades (50-200) are the "baby blue" of
        // the brand pale watercolor glow; 300-500 are CTAs and links.
        sky: {
          50:  '#f0f9ff', // baby blue (palette ground)
          100: '#e0f2fe',
          200: '#bae6fd',
          300: '#7dd3fc', // primary text accent
          400: '#38bdf8',
          500: '#0ea5e9', // CTA button (gradient stop 1)
          600: '#0284c7',
          700: '#0369a1',
          900: '#0c4a6e',
        },
        // ── Gold ──
        // "Money / success" — prices, sale confirmations, premium accents.
        gold: {
          50:  '#fffbeb',
          100: '#fef3c7',
          200: '#fde68a',
          300: '#fcd34d', // card price badge
          400: '#fbbf24', // CTA button (gradient stop 1)
          500: '#f59e0b', // CTA button (gradient stop 2)
          600: '#d97706',
        },
        // ── Purple (violet) ──
        // The "accent" color. Active states, auctions (live pulse), purples.
        violet: {
          50:  '#f5f3ff',
          100: '#ede9fe',
          200: '#ddd6fe',
          300: '#c4b5fd',
          400: '#a78bfa',
          500: '#8b5cf6',
          600: '#7c3aed',
          700: '#6d28d9',
          900: '#4c1d95',
        },
        // ── Black / Ink ──
        // Backgrounds, surfaces, neutral text. We expand ink past the
        // prior 600-950 range to enable black/100 from transparent to deep.
        ink: {
          50:  '#fafafa',
          100: '#f4f4f5',
          200: '#e4e4e7',
          300: '#d4d4d8',
          400: '#a1a1aa',
          500: '#71717a',
          600: '#52525b', // muted body
          700: '#3f3f46',
          800: '#27272a',
          850: '#1f1f23',
          900: '#18181b', // card surface
          950: '#09090b', // page background
          1000:'#000000', // pure black (modal backdrop)
        },
        // ── White ──
        // Explicit white-on-dark for headings + contrast surfaces.
        white: '#ffffff',
      },
      backgroundImage: {
        'sky-radial':     'radial-gradient(at 0% 0%,  rgba(186,230,253,0.18) 0%, transparent 55%)',
        'gold-radial':    'radial-gradient(at 100% 0%, rgba(253,230,138,0.18) 0%, transparent 55%)',
        'violet-radial':  'radial-gradient(at 50% 100%,rgba(196,181,253,0.18) 0%, transparent 55%)',
        'sky-btn':        'linear-gradient(135deg, #7dd3fc 0%, #0ea5e9 100%)',
        'gold-btn':       'linear-gradient(135deg, #fcd34d 0%, #f59e0b 100%)',
        'violet-btn':     'linear-gradient(135deg, #a78bfa 0%, #7c3aed 100%)',
        'mesh-hero':      'radial-gradient(at 20% 0%,  rgba(125,211,252,0.30) 0px, transparent 50%), radial-gradient(at 80% 0%, rgba(252,211,77,0.18) 0px, transparent 50%), radial-gradient(at 100% 100%, rgba(196,181,253,0.18) 0px, transparent 50%)',
      },
      boxShadow: {
        'sky-glow':     '0 0 30px -5px rgba(56,189,248,0.45)',
        'gold-glow':    '0 0 30px -5px rgba(251,191,36,0.50)',
        'violet-glow':  '0 0 30px -5px rgba(167,139,250,0.45)',
        'white-glow':   '0 0 22px -4px rgba(255,255,255,0.85)',
        'inner-soft':   'inset 0 0 0 1px rgba(255,255,255,0.05)',
        'topbar':       '0 1px 0 rgba(255,255,255,0.04), 0 12px 24px -8px rgba(0,0,0,0.55)',
      },
      animation: {
        'pulse-slow':   'pulse 3s cubic-bezier(0.4,0,0.6,1) infinite',
        'spin-slow':    'spin 2.5s linear infinite',
        'gradient':     'gradient 5s ease infinite',
        'shimmer':      'shimmer 2s infinite',
      },
      keyframes: {
        gradient: {
          '0%, 100%': { 'background-position': '0% 50%' },
          '50%':      { 'background-position': '100% 50%' },
        },
      },
      borderRadius: {
        '4xl': '2rem',
      },
    },
  },
  plugins: [require('@tailwindcss/typography')],
};
