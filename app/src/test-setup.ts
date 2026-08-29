import '@testing-library/jest-dom';
import { beforeEach } from 'vitest';

// Mock localStorage for wallet address persistence
const localStorageMock = (() => {
  let store: Record<string, string> = {};
  return {
    getItem: (key: string) => store[key] ?? null,
    setItem: (key: string, value: string) => { store[key] = value; },
    removeItem: (key: string) => { delete store[key]; },
    clear: () => { store = {}; },
    get length() { return Object.keys(store).length; },
    key: (i: number) => Object.keys(store)[i] ?? null,
  };
})();

Object.defineProperty(window, 'localStorage', { value: localStorageMock, configurable: true });

// The mock's store is module-level: without a reset, a test that writes
// mw_addr leaks it into later tests and makes wallet tests order-dependent.
beforeEach(() => {
  localStorageMock.clear();
});

// Mock clipboard API
Object.defineProperty(navigator, 'clipboard', {
  value: { writeText: () => Promise.resolve() },
  configurable: true,
});
