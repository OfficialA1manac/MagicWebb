// tailwind.config.cjs mirrors the prior runtime `tailwind.config = {…}`
// block that layout.html executed inline. The block lives here now so
// the standalone CLI (cmd/buildtailwindcss) can compile it into a
// static stylesheet; the JIT script that required this config object
// at runtime is gone.
//
// Kept in lockstep with backend/internal/ui/templates/layout.html's
// static-page hand-styling rules — a future addition of utilities like
// `text-brand` or `bg-gold-gradient` should land here AND in the
// patterns of html files (content glob) so the compiler emits them.
// Re-running `go run ./cmd/buildtailwindcss` picks them up.

/** @type {import('tailwindcss').Config} */
module.exports = {
  theme: {
    extend: {
      fontFamily: {
        sans: ['Inter', 'system-ui', 'sans-serif'],
        mono:  ['JetBrains Mono', 'ui-monospace', 'monospace'],
      },
      colors: {
        // Sky-blue family — primary accents, info, watercolor glow.
        sky:    { 100: '#e0f2fe', 200: '#bae6fd', 300: '#7dd3fc', 400: '#38bdf8', 500: '#0ea5e9', 600: '#0284c7' },
        // Gold — prices, success accents, premium highlights.
        gold:   { 100: '#fef3c7', 200: '#fde68a', 300: '#fcd34d', 400: '#fbbf24', 500: '#f59e0b', 600: '#d97706' },
        // Purple — secondary accents, live states, anti-snipe.
        violet: { 100: '#ede9fe', 200: '#ddd6fe', 300: '#c4b5fd', 400: '#a78bfa', 500: '#8b5cf6', 600: '#7c3aed' },
        // Ink — base, surfaces, neutrals.
        ink:    { 950: '#050507', 900: '#0a0a0d', 800: '#13131a', 700: '#1a1a23', 600: '#2a2a35' },
      },
      backgroundImage: {
        'sky-radial':    'radial-gradient(at 0% 0%, rgba(56,189,248,0.20) 0%, transparent 55%)',
        'gold-radial':   'radial-gradient(at 100% 0%, rgba(251,191,36,0.18) 0%, transparent 55%)',
        'violet-radial': 'radial-gradient(at 50% 100%, rgba(167,139,250,0.18) 0%, transparent 55%)',
        'sky-btn':       'linear-gradient(135deg, #38bdf8 0%, #0ea5e9 100%)',
        'gold-btn':      'linear-gradient(135deg, #fcd34d 0%, #f59e0b 100%)',
        'violet-btn':    'linear-gradient(135deg, #a78bfa 0%, #7c3aed 100%)',
      },
      boxShadow: {
        'sky-glow':    '0 0 30px -5px rgba(56,189,248,0.40)',
        'gold-glow':   '0 0 30px -5px rgba(251,191,36,0.45)',
        'violet-glow': '0 0 30px -5px rgba(167,139,250,0.40)',
      },
      animation: {
        'pulse-slow': 'pulse 3s cubic-bezier(0.4,0,0.6,1) infinite',
      },
    },
  },
  // Project's typography plugin is required for any `prose` styling the
  // templates emit. Mirrors the `?plugins=typography` query on the
  // prior JIT script tag.
  plugins: [require('@tailwindcss/typography')],
};
