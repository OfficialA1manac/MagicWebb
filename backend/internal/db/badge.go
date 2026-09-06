package db

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"
)

// ── NFT-level badge (v3.6 wave 3, owner decision D3) ─────────────────────────
//
// Two signals, computed here so every row type answers them identically:
//
//   ✓ Verified   — "this NFT fully works on the marketplace": its collection
//                  passed the verifier (ERC-165 standard + metadata resolved),
//                  the token has a name, its image is served from our own store
//                  (or is an inline data: URI), and a holder is known. Reasons
//                  for a missing checkmark are listed so the UI can say why.
//   ★ Creator    — the token's current holder IS the collection's creator
//                  (ERC-173 owner / deployer) AND the token was minted by that
//                  creator (nft_tokens.minter, migration 044). Never shown to a
//                  mere owner.
//
// The checkmark is derived from fields the list queries already return, so no
// SELECT changed; the minter check is a single batched lookup per page and is
// best-effort — an enrichment failure never fails the request.

// TokenBadge is embedded in every NFT-bearing row type.
type TokenBadge struct {
	Verified       bool     `json:"verified"`
	VerifiedReason []string `json:"verified_reason"`
	// CreatorIsOwner: holder == collection creator AND minter == creator.
	CreatorIsOwner bool `json:"creator_is_owner"`
}

// Reasons (stable identifiers the UI maps to copy).
const (
	ReasonUnverifiedCollection = "unverified_collection"
	ReasonNoMetadata           = "no_metadata"
	ReasonImageMissing         = "image_missing"
	ReasonOwnerUnknown         = "owner_unknown"
)

// ComputeTokenBadge returns the checkmark and the unmet checks.
func ComputeTokenBadge(collectionVerified bool, name, imageURI string, ownerKnown bool) (bool, []string) {
	reasons := []string{}
	if !collectionVerified {
		reasons = append(reasons, ReasonUnverifiedCollection)
	}
	if strings.TrimSpace(name) == "" {
		reasons = append(reasons, ReasonNoMetadata)
	}
	if !imageStored(imageURI) {
		reasons = append(reasons, ReasonImageMissing)
	}
	if !ownerKnown {
		reasons = append(reasons, ReasonOwnerUnknown)
	}
	return len(reasons) == 0, reasons
}

// imageStored is true when the image bytes are ours to serve: content-addressed
// in the blob store (/api/v1/img/<sha> or /img/<sha>) or inline in the metadata.
func imageStored(uri string) bool {
	u := strings.TrimSpace(uri)
	return strings.HasPrefix(u, "/api/v1/img/") || strings.HasPrefix(u, "/img/") || strings.HasPrefix(u, "data:")
}

// badgeRow is what a row must expose for enrichment. holder is the address
// whose relationship to the creator decides ★ (seller for listings/auctions,
// owner for tokens); "" when the row has no holder concept (offers, activity).
type badgeRow interface {
	badgeInputs() (collection, tokenID string, collectionVerified bool, name, imageURI string, ownerKnown bool, holder, creator string)
	setBadge(TokenBadge)
}

// fillTokenBadges computes ✓ for every row, then resolves ★ with ONE minter
// lookup for the rows whose holder equals the creator. Best-effort: on a
// lookup failure ★ stays false and the request still succeeds.
func (q *Q) fillTokenBadges(ctx context.Context, rows []badgeRow) {
	type cand struct {
		idx  int
		coll string
		id   string
		cre  string
	}
	var cands []cand
	for i, r := range rows {
		coll, id, cv, name, img, ownerKnown, holder, creator := r.badgeInputs()
		ok, reasons := ComputeTokenBadge(cv, name, img, ownerKnown)
		r.setBadge(TokenBadge{Verified: ok, VerifiedReason: reasons})
		if holder != "" && creator != "" && strings.EqualFold(holder, creator) {
			cands = append(cands, cand{i, coll, id, strings.ToLower(creator)})
		}
	}
	if len(cands) == 0 {
		return
	}
	colls := make([]string, len(cands))
	ids := make([]string, len(cands))
	for i, c := range cands {
		colls[i], ids[i] = strings.ToLower(c.coll), c.id
	}
	minters, err := q.MintersFor(ctx, colls, ids)
	if err != nil {
		log.Debug().Err(err).Msg("badge: minter lookup failed; creator badge withheld")
		return
	}
	for _, c := range cands {
		if m, ok := minters[strings.ToLower(c.coll)+"/"+c.id]; ok && m == c.cre {
			r := rows[c.idx]
			_, _, cv, name, img, ownerKnown, _, _ := r.badgeInputs()
			ok2, reasons := ComputeTokenBadge(cv, name, img, ownerKnown)
			r.setBadge(TokenBadge{Verified: ok2, VerifiedReason: reasons, CreatorIsOwner: true})
		}
	}
}

// MintersFor returns "collection/token_id" → minter (lowercase) for the given
// pairs, only where a minter is recorded.
func (q *Q) MintersFor(ctx context.Context, collections, tokenIDs []string) (map[string]string, error) {
	rows, err := q.reader().Query(ctx,
		`SELECT t.collection, t.token_id::text, lower(t.minter)
		 FROM nft_tokens t
		 JOIN unnest($1::text[], $2::text[]) AS k(c, i) ON t.collection = k.c AND t.token_id = k.i::numeric
		 WHERE t.minter IS NOT NULL`, collections, tokenIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var c, i, m string
		if err := rows.Scan(&c, &i, &m); err != nil {
			return nil, err
		}
		out[strings.ToLower(c)+"/"+i] = m
	}
	return out, rows.Err()
}

// SetTokenMinter records the first minter (Transfer from 0x0). Idempotent:
// COALESCE keeps the first value so a re-index never rewrites provenance.
func (q *Q) SetTokenMinter(ctx context.Context, collection, tokenID, minter string) error {
	_, err := q.writer().Exec(ctx,
		`INSERT INTO nft_tokens(collection, token_id, minter) VALUES($1,$2,$3)
		 ON CONFLICT(collection, token_id) DO UPDATE SET minter = COALESCE(nft_tokens.minter, EXCLUDED.minter)`,
		strings.ToLower(collection), tokenID, strings.ToLower(minter))
	return err
}

// ── per-row adapters ─────────────────────────────────────────────────────────

func (r *ListingRow) badgeInputs() (string, string, bool, string, string, bool, string, string) {
	return r.Collection, r.TokenID, r.CollectionVerified, r.Name, r.ImageURI, r.Seller != "", r.Seller, r.CollectionCreator
}
func (r *ListingRow) setBadge(b TokenBadge) { r.TokenBadge = b }

func (r *AuctionRow) badgeInputs() (string, string, bool, string, string, bool, string, string) {
	return r.Collection, r.TokenID, r.CollectionVerified, r.Name, r.ImageURI, r.Seller != "", r.Seller, r.CollectionCreator
}
func (r *AuctionRow) setBadge(b TokenBadge) { r.TokenBadge = b }

func (r *OfferRow) badgeInputs() (string, string, bool, string, string, bool, string, string) {
	// An offer targets a token whose holder the row does not carry: ✓ still
	// answers (holder is implied — someone owns it), ★ never applies here.
	return r.Collection, r.TokenID, r.CollectionVerified, r.Name, r.ImageURI, true, "", ""
}
func (r *OfferRow) setBadge(b TokenBadge) { r.TokenBadge = b }

func (r *SearchResult) badgeInputs() (string, string, bool, string, string, bool, string, string) {
	return r.Collection, r.TokenID, r.Verified, r.Name, r.ImageURI, true, "", ""
}
func (r *SearchResult) setBadge(b TokenBadge) { r.TokenBadge = b }

func (r *ActivityRow) badgeInputs() (string, string, bool, string, string, bool, string, string) {
	// Activity rows carry no collection_verified column (only tracked); the
	// checkmark is withheld rather than guessed.
	return r.Collection, r.TokenID, false, r.Name, r.ImageURI, true, "", ""
}
func (r *ActivityRow) setBadge(b TokenBadge) { r.TokenBadge = b }

func (r *TokenDetail) badgeInputs() (string, string, bool, string, string, bool, string, string) {
	return r.Collection, r.TokenID, r.CollectionVerified, r.Name, r.ImageURI, r.Owner != "", r.Owner, r.CollectionCreator
}
func (r *TokenDetail) setBadge(b TokenBadge) { r.TokenBadge = b }

// collectionTokenBadge adapts CollectionTokenRow, which has no collection
// columns of its own (the handler knows the collection).
type collectionTokenBadge struct {
	row        *CollectionTokenRow
	collection string
	verified   bool
	creator    string
}

func (a *collectionTokenBadge) badgeInputs() (string, string, bool, string, string, bool, string, string) {
	return a.collection, a.row.TokenID, a.verified, a.row.Name, a.row.Image, a.row.Owner != "", a.row.Owner, a.creator
}
func (a *collectionTokenBadge) setBadge(b TokenBadge) { a.row.TokenBadge = b }

// FillCollectionTokenBadges enriches a page of collection tokens with the
// collection's own verified flag and creator.
func (q *Q) FillCollectionTokenBadges(ctx context.Context, collection string, verified bool, creator string, rows []CollectionTokenRow) {
	adapters := make([]badgeRow, len(rows))
	for i := range rows {
		adapters[i] = &collectionTokenBadge{row: &rows[i], collection: collection, verified: verified, creator: creator}
	}
	q.fillTokenBadges(ctx, adapters)
}

func asBadgeRows[T any, PT interface {
	*T
	badgeRow
}](items []T) []badgeRow {
	out := make([]badgeRow, len(items))
	for i := range items {
		out[i] = PT(&items[i])
	}
	return out
}
