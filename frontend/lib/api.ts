import { getToken } from './auth'

const BASE = process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:8080'

async function apiFetch(path: string, opts?: RequestInit) {
  const token = getToken()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(opts?.headers as Record<string, string> | undefined),
  }
  if (token) headers['Authorization'] = `Bearer ${token}`
  const res = await fetch(`${BASE}${path}`, { ...opts, headers })
  if (!res.ok) throw new Error(`API ${res.status}: ${path}`)
  return res.json()
}

export interface OfferPayload {
  bidder: string
  collection: string
  token_id: string
  amount_wei: string
  nonce: string
  expires_at: number
  signature: string
}

export const api = {
  getListings: (p?: { collection?: string; seller?: string; limit?: number }) => {
    const params = new URLSearchParams()
    if (p?.collection) params.set('collection', p.collection)
    if (p?.seller) params.set('seller', p.seller)
    if (p?.limit != null) params.set('limit', String(p.limit))
    const qs = params.toString()
    return apiFetch(`/api/v1/listings${qs ? `?${qs}` : ''}`)
  },

  getListing: (collection: string, id: string) =>
    apiFetch(`/api/v1/listings/${collection}/${id}`),

  getCollections: (limit = 50) =>
    apiFetch(`/api/v1/collections?limit=${limit}`),

  getCollection: (address: string) =>
    apiFetch(`/api/v1/collections/${address}`),

  getTrending: (window = '24h', limit = 20) =>
    apiFetch(`/api/v1/trending?window=${window}&limit=${limit}`),

  getAuctions: (p?: { collection?: string; status?: string; limit?: number }) => {
    const params = new URLSearchParams()
    if (p?.collection) params.set('collection', p.collection)
    if (p?.status) params.set('status', p.status)
    if (p?.limit != null) params.set('limit', String(p.limit))
    const qs = params.toString()
    return apiFetch(`/api/v1/auctions${qs ? `?${qs}` : ''}`)
  },

  getAuction: (id: string | bigint) =>
    apiFetch(`/api/v1/auctions/${id}`),

  getServerTime: (): Promise<{ unix_ms: number }> =>
    apiFetch('/api/v1/server-time'),

  getOffers: (p?: { collection?: string; token_id?: string; bidder?: string; owner?: string; status?: string }) => {
    const params = new URLSearchParams()
    if (p?.collection) params.set('collection', p.collection)
    if (p?.token_id) params.set('token_id', p.token_id)
    if (p?.bidder) params.set('bidder', p.bidder)
    if (p?.owner) params.set('owner', p.owner)
    if (p?.status) params.set('status', p.status)
    const qs = params.toString()
    return apiFetch(`/api/v1/offers${qs ? `?${qs}` : ''}`)
  },

  postOffer: (offer: OfferPayload) =>
    apiFetch('/api/v1/offers', { method: 'POST', body: JSON.stringify(offer) }),

  getIndexerStatus: () =>
    apiFetch('/api/v1/indexer/status'),
}
