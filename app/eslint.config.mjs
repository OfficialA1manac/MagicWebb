import eslint from '@eslint/js';
import tseslint from 'typescript-eslint';
import astroPlugin from 'eslint-plugin-astro';
import sveltePlugin from 'eslint-plugin-svelte';
import globals from 'globals';

export default tseslint.config(
  // Global ignores
  {
    ignores: [
      'dist/',
      '.astro/',
      'node_modules/',
      'vite.bridge.config.mjs',
    ],
  },

  // Base JavaScript/TypeScript rules
  eslint.configs.recommended,
  ...tseslint.configs.recommended,

  // ── All source files: browser + node globals ──
  {
    files: ['src/**/*.ts', 'src/**/*.tsx', 'src/**/*.js', 'src/**/*.astro', 'src/**/*.svelte'],
    languageOptions: {
      globals: {
        ...globals.browser,
        ...globals.es2021,
      },
    },
  },

  // ── TypeScript source files ──
  {
    files: ['src/**/*.ts', 'src/**/*.tsx'],
    rules: {
      '@typescript-eslint/no-explicit-any': 'warn',
      '@typescript-eslint/no-unused-vars': ['warn', {
        argsIgnorePattern: '^_',
        caughtErrorsIgnorePattern: '^_',
      }],
      'no-empty': ['warn', { allowEmptyCatch: true }],
      'no-console': 'off',
    },
  },

  // ── JavaScript source files (appkit-bridge.js) ──
  {
    files: ['src/**/*.js'],
    rules: {
      '@typescript-eslint/no-unused-vars': ['warn', {
        argsIgnorePattern: '^_',
        caughtErrorsIgnorePattern: '^_',
        varsIgnorePattern: '^_',
      }],
      'no-empty': ['warn', { allowEmptyCatch: true }],
      'no-console': 'off',
    },
  },

  // ── Svelte source files ──
  {
    files: ['src/**/*.svelte'],
    rules: {
      '@typescript-eslint/no-unused-vars': ['warn', {
        argsIgnorePattern: '^_',
        caughtErrorsIgnorePattern: '^_',
        varsIgnorePattern: '^_',
      }],
      'no-empty': ['warn', { allowEmptyCatch: true }],
    },
  },

  // ── Astro files — recommended config ──
  ...astroPlugin.configs.recommended,

  // ── Astro inline <script> overrides ──
  // Astro inline scripts commonly use `var`, `async IIFEs`, and browser DOM
  // access patterns that are not caught by the standard TypeScript rules.
  {
    files: ['**/*.astro'],
    rules: {
      // `var` is the conventional pattern for Astro inline scripts (hoisting
      // behaviour needed for IIFE-scoped variables referenced across functions).
      'no-var': 'off',
      // Inline scripts define functions that are only called from HTML event
      // handlers (onclick, onerror, etc.) which ESLint can't statically trace.
      // We use a broad pattern because any lowercase-starting name could be
      // an event-handler helper referenced from inline HTML attributes.
      '@typescript-eslint/no-unused-vars': ['warn', {
        argsIgnorePattern: '^_',
        varsIgnorePattern: '^[a-z_][a-zA-Z0-9_]*$',
      }],
      // Catch: empty catch blocks are intentional in this codebase (fire-and-forget fetches)
      'no-empty': ['warn', { allowEmptyCatch: true }],
    },
  },

  // ── Svelte files ──
  {
    files: ['**/*.svelte'],
    languageOptions: {
      parser: (await import('svelte-eslint-parser')).default,
      parserOptions: {
        parser: tseslint.parser,
      },
    },
    plugins: {
      'svelte': sveltePlugin,
    },
    rules: {
      ...sveltePlugin.configs.recommended.rules,
      // Svelte 5 runes use $state, $derived, $effect — these are valid
      'svelte/valid-compile': 'warn',
    },
  },

  // ── Config files (Node.js) ──
  {
    files: ['astro.config.mjs', 'svelte.config.js'],
    languageOptions: {
      globals: {
        ...globals.node,
      },
    },
    rules: {
      'no-undef': 'off',
    },
  },
);
