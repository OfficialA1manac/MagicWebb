// Package imagestore is the self-hosted, content-addressed BYTEA store for
// NFT images and metadata JSON blobs. Every blob is keyed by the SHA-256 of
// its bytes, so identical bytes from different NFT contracts dedupe to one
// row, and the server can prove content integrity without trusting any
// upstream gateway at serve time. Frontends hit /api/v1/img/<sha256> on the
// same origin — IPFS, Cloudflare and Pinata are not in the render path
// after ingest.
//
// Storage model: a single Postgres row per SHA-256 (BYTEA body + mime +
// byte_length + source_uri + refcount). Body cap (MaxBlobBytes) is enforced
// before the INSERT so a malicious contract cannot bloat the table. Capacity
// is bounded by the Supabase / Postgres free-tier limit; if a deployment
// outgrows it, the same API can be swapped to a backend that streams from
// disk (S3-compatible / local volume) by changing only the body column + Store.
package imagestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// PathPrefix is the same-origin route segment that serves a blob by hash.
// The frontend never speaks to anything outside this URL once a token has
// been ingested; legacy /api/v1/media?url=… is retained for pre-ingest
// ERC-1155 {id} templates and contract URLs that were minted before migrate.
const PathPrefix = "/api/v1/img"

// MaxBlobBytes caps individual blob sizes at 8 MiB. Anything larger is
// rejected before INSERT so a single malicious contract cannot bloat the
// row past Postgres' 1 GiB bytea row cap. The cap matches media.maxFetchBytes
// in resolve.go so the upstream HTTP path can never produce a body the
// store isn't prepared to accept.
const MaxBlobBytes = 8 << 20

// ValidateHash reports whether s is a syntactically valid SHA-256 hex string
// (64 lowercase hex characters). It is a syntactic check only — the hash
// must also exist in the table for Get to return content.
func ValidateHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < 64; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// PublicPath returns the same-origin path that serves this blob's bytes.
// Always of the form `/api/v1/img/<64-hex>`. Empty hash is rejected.
func PublicPath(sha256hex string) string {
	if !ValidateHash(sha256hex) {
		return ""
	}
	return PathPrefix + "/" + sha256hex
}

// Hash returns the SHA-256 hex digest of body, or an empty string for an
// empty body (which we never store).
func Hash(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Blob is one row of nft_image_blobs. Body length is len(Body) — there is no
// separate ByteLen field; callers that need it should compute it inline.
type Blob struct {
	Body      []byte
	Mime      string
	SourceURI string
}

// Store is the persistent backend for the blob store. Implemented by
// *db.Q (production) and a stub in tests.
type Store interface {
	// PutImage upserts body keyed by sha256, bumping refcount if the row
	// already exists. Returns the persisted mime (input mime on insert,
	// existing mime on a refcount-only collision — the bytes are identical
	// by construction so an upstream mime mismatch is silently corrected
	// to the canonical value).
	PutImage(ctx context.Context, sha256hex, mime, sourceURI string, body []byte) error

	// GetImage returns the blob bytes + mime + source URI for a known hash.
	// Returns (nil, "", "", pgx.ErrNoRows) when the hash is unknown.
	GetImage(ctx context.Context, sha256hex string) (Blob, error)

	// HasImage is a cheap existence check (no body fetch).
	HasImage(ctx context.Context, sha256hex string) (bool, error)
}

// ErrBodyTooLarge is returned when body exceeds MaxBlobBytes. We surface it
// as a typed error so the indexer can log "blob too large" without burying
// the cause in a generic fmt.Errorf.
var ErrBodyTooLarge = errors.New("imagestore: body exceeds MaxBlobBytes")

// ErrEmptyBody is returned when an attempt is made to store an empty body.
var ErrEmptyBody = errors.New("imagestore: empty body")

// ErrInvalidHash is returned by Stored (and by Get on bad hashes).
var ErrInvalidHash = errors.New("imagestore: invalid sha256 hash")

// Store is the public ingest response. The returned hex hash is the
// same-origin reference the indexer embeds into nft_metadata.image_uri (or
// metadata_uri) so the frontend hits /api/v1/img/<hash> instead of the
// original IPFS / gateway URL. Inserted is false when the row pre-existed and
// refcount was bumped rather than the body re-inserted (best-effort hint).
type Stored struct {
	Hash     string // 64-char lowercase hex
	Mime     string // canonical MIME (post-sniff)
	Inserted bool   // false when the row already existed and was ref-bumped
}

// Sniffer is the function signature of media.SniffImage — a runtime seam
// so tests can stub it without importing the media package.
type Sniffer func(body []byte) (mime string, ok bool)

// Put writes body to the store, hashing and deduping automatically.
//
//   - body is capped at MaxBlobBytes (8 MiB).
//   - mime is sniffed from body via src — failed sniff returns an error so
//     we never store opaque junk that the handler would later refuse to
//     serve.
//   - sourceURI is recorded verbatim for audit / dedup debugging; not used
//     at serve time.
func Put(ctx context.Context, s Store, src Sniffer, sourceURI string, body []byte) (Stored, error) {
	if len(body) == 0 {
		return Stored{}, ErrEmptyBody
	}
	if len(body) > MaxBlobBytes {
		return Stored{}, ErrBodyTooLarge
	}
	if src == nil {
		return Stored{}, fmt.Errorf("imagestore: no sniffer provided")
	}
	mime, ok := src(body)
	if !ok {
		return Stored{}, fmt.Errorf("imagestore: unsupported body (mime unfit for store)")
	}
	hash := Hash(body)
	if hash == "" {
		return Stored{}, ErrEmptyBody
	}
	pre, preErr := s.HasImage(ctx, hash)
	if err := s.PutImage(ctx, hash, mime, sourceURI, body); err != nil {
		return Stored{}, fmt.Errorf("imagestore: put: %w", err)
	}
	return Stored{
		Hash:     hash,
		Mime:     mime,
		Inserted: preErr == nil && !pre,
	}, nil
}

// Get reads a blob by hash. Returns ErrInvalidHash if the hash is malformed;
// returns pgx.ErrNoRows when the hash is well-formed but unknown.
func Get(ctx context.Context, s Store, sha256hex string) (Blob, error) {
	if !ValidateHash(sha256hex) {
		return Blob{}, ErrInvalidHash
	}
	return s.GetImage(ctx, sha256hex)
}

// Has is a cheap existence check used by the mediaProxy to decide whether
// the local store can serve the request without contacting the upstream.
func Has(ctx context.Context, s Store, sha256hex string) (bool, error) {
	if !ValidateHash(sha256hex) {
		return false, ErrInvalidHash
	}
	return s.HasImage(ctx, sha256hex)
}

// ExtractHash parses a same-origin /api/v1/img/<hash> URL into the hash.
// Returns "" for any URL that does not match the canonical path.
func ExtractHash(uri string) string {
	uri = strings.TrimSpace(uri)
	prefix := PathPrefix + "/"
	i := strings.Index(uri, prefix)
	if i < 0 {
		return ""
	}
	h := uri[i+len(prefix):]
	// strip any trailing path or query
	if j := strings.IndexAny(h, "?#"); j >= 0 {
		h = h[:j]
	}
	if !ValidateHash(h) {
		return ""
	}
	return h
}

// IsNoRows reports whether err is pgx.ErrNoRows. Convenience for callers
// that don't want to import pgx directly.
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
