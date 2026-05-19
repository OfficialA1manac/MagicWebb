const KEY = 'wp:jwt'

function parseExp(token: string): number {
  try {
    const payload = JSON.parse(atob(token.split('.')[1])) as {exp?: number}
    return payload.exp ?? 0
  } catch { return 0 }
}

export function getToken(): string | null {
  if (typeof window === 'undefined') return null
  return localStorage.getItem(KEY)
}

export function setToken(token: string): void {
  if (typeof window === 'undefined') return
  localStorage.setItem(KEY, token)
}

export function clearToken(): void {
  if (typeof window === 'undefined') return
  localStorage.removeItem(KEY)
}

export function isExpired(token: string): boolean {
  return parseExp(token) < Math.floor(Date.now() / 1000)
}
