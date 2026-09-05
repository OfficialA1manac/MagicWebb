// Docs registry (spec B4 "Docs"): one source of truth rendered by both the
// docs index and DocLayout's sidebar, with the 2-minute guide first.
import { describe, it, expect } from 'vitest';
import { DOCS } from '../pages/docs/_registry';

describe('docs registry', () => {
  it('start-here is the first document', () => {
    expect(DOCS[0].slug).toBe('start-here');
    expect(DOCS[0].title).toBe('Start Here');
  });

  it('slugs are unique', () => {
    const slugs = DOCS.map((d) => d.slug);
    expect(new Set(slugs).size).toBe(slugs.length);
  });

  it('every long-standing document is still registered', () => {
    const slugs = DOCS.map((d) => d.slug);
    for (const s of ['whitepaper', 'technical', 'user-guide', 'capabilities', 'faq', 'token-hooks', 'api']) {
      expect(slugs).toContain(s);
    }
  });

  it('every entry carries the fields both renderers need', () => {
    for (const d of DOCS) {
      expect(d.slug).toMatch(/^[a-z0-9-]+$/);
      expect(d.title.length).toBeGreaterThan(0);
      expect(d.blurb.length).toBeGreaterThan(0);
      expect(d.icon.length).toBeGreaterThan(0);
      expect(d.accent).toMatch(/^#[0-9a-f]{6}$/i);
    }
  });
});
