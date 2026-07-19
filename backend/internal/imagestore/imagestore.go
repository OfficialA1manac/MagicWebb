// Package imagestore is the self-hosted, content-addressed BYTEA store for
// NFT images and metadata JSON blobs. Every blob is keyed by the SHA-256 of
// its bytes, so identical bytes from different NFT contracts dedupe to one
// row, and the server can prove content integrity without trusting any
// upstream gateway at serve time. Frontends hit /api/v1/img/<sha256> on the
// same origin — the upstream gateway is not in the render path after ingest,
// all assets are served from the self-hosted BYTEA store.
//
// Storage model: a single Postgres row per SHA-256 (BYTEA body + mime +
// byte_length + source_uri + refcount). Body cap (MaxBlobBytes) is enforced
// before the INSERT so a malicious contract cannot bloat the table. Capacity
// is bounded by the Postgres free-tier limit; if a deployment
// outgrows it, the same API can be swapped to a backend that streams from
// disk (S3-compatible / local volume) by changing only the body column + Store.
package imagestore

import (
	"context"
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

// MaxBlobCountPerCollection caps the number of unique blobs per collection.
// Without a ceiling, a single contract with 10k distinct token images would
// fill the Postgres table before any other collection can store a single
// blob. 1,000 is generous for most ERC-721/1155 collections (many have
// <100 distinct image hashes due to metadata redirection) and fits well
// within Postgres' BYTEA table limits.
//
// Enforced by CountBlobsForCollection in Put(): new blobs from a collection
// that has already stored MaxBlobCountPerCollection distinct hashes are
// silently skipped (Skipped=true). Dedup hits (existing bytes) bypass the
// check since no new storage is consumed. Existing rows with an empty
// collection string do not count toward any quota.
const MaxBlobCountPerCollection = 1_000

// MaxTotalBlobBytes caps the cumulative byte volume of all blobs across
// every collection. Without this, a large generative collection could fill
// the disk / Postgres free-tier allocation. 256 MiB (~32 avg-size images at
// 8 MiB each) provides generous headroom while keeping the table small
// enough for frequent read/write operations. When the cap is exceeded, new
// blobs are silently skipped (not rejected) — the Get/Proxy fallback still
// serves the upstream URL.
const MaxTotalBlobBytes = 256 << 20

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
//
// The hash computation is pluggable via build tags:
//   - Default (no tag): uses Go's crypto/sha256
//   - zigmedia tag:      uses Zig-accelerated SHA-256 via CGO
// Both implementations produce identical results.
func Hash(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	sum := hashBytes(body)
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
	//
	// collection is the NFT contract address that triggered this insert.
	// On INSERT it is stored in the collection column; ON CONFLICT (dedup)
	// the existing collection value is preserved.
	PutImage(ctx context.Context, sha256hex, mime, collection, sourceURI string, body []byte) error

	// PutThumbnail stores a thumbnail variant linked to a parent full-size
	// image via parentHash. IMG-1: called by StoreThumbnails after generating
	// 128/256/512px variants. Same dedup semantics as PutImage — identical
	// thumbnail bytes from different collections share one row via SHA-256.
	// Thumbnail rows do NOT count toward per-collection blob quotas.
	// width is the target pixel width (128, 256, or 512) — stored in the
	// thumb_width column so the ?size= handler can look up thumbnails.
	PutThumbnail(ctx context.Context, sha256hex, mime, parentHash, collection, sourceURI string, body []byte, width int) error

	// GetImageByParent returns the blob for a thumbnail matching (parentHash,
	// width). Returns pgx.ErrNoRows when no thumbnail exists at that size.
	// IMG-1: used by HandleImageByHash when ?size= is present.
	// When preferWebP is true, prefers image/webp thumbnails over JPEG.
	GetImageByParent(ctx context.Context, parentHash string, width int, preferWebP bool) (Blob, error)

	// GetImage returns the blob bytes + mime + source URI for a known hash.
	// Returns (nil, "", "", pgx.ErrNoRows) when the hash is unknown.
	GetImage(ctx context.Context, sha256hex string) (Blob, error)

	// HasImage is a cheap existence check (no body fetch).
	HasImage(ctx context.Context, sha256hex string) (bool, error)

	// TotalBlobBytes returns the cumulative byte_length across all blob
	// rows. Used by Put() to enforce MaxTotalBlobBytes.
	TotalBlobBytes(ctx context.Context) (int64, error)

	// CountBlobsForCollection returns the number of distinct blobs first
	// inserted by this collection. Used by Put() to enforce
	// MaxBlobCountPerCollection.
	CountBlobsForCollection(ctx context.Context, collection string) (int, error)
}

// ErrBodyTooLarge is returned when body exceeds MaxBlobBytes. We surface it
// as a typed error so the indexer can log "blob too large" without burying
// the cause in a generic fmt.Errorf.
var ErrBodyTooLarge = errors.New("imagestore: body exceeds MaxBlobBytes")

// ErrEmptyBody is returned when an attempt is made to store an empty body.
var ErrEmptyBody = errors.New("imagestore: empty body")

// ErrInvalidHash is returned by Stored (and by Get on bad hashes).
var ErrInvalidHash = errors.New("imagestore: invalid sha256 hash")

// Stored is the public ingest response. The returned hex hash is the
// same-origin reference the indexer embeds into nft_metadata.image_uri (or
// metadata_uri) so the frontend hits /api/v1/img/<hash> instead of the
// original gateway URL. Inserted is false when the row pre-existed and
// refcount was bumped rather than the body re-inserted (best-effort hint).
// Skipped is true when the blob was not stored because a quota cap was
// exceeded — the caller should fall back to proxying the upstream URL.
type Stored struct {
	Hash     string // 64-char lowercase hex
	Mime     string // canonical MIME (post-sniff)
	Inserted bool   // false when the row already existed and was ref-bumped
	Skipped  bool   // true when quota cap exceeded — caller should proxy upstream
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
//   - collection is the contract address of the NFT collection, used to
//     enforce per-collection and total blob quotas.
//   - Total blob byte volume is capped at MaxTotalBlobBytes. When exceeded,
//     the blob is skipped (Skipped=true) rather than rejected — the caller
//     falls back to proxying the upstream URL.
//   - Per-collection blob count is capped at MaxBlobCountPerCollection.
//     When exceeded, the blob is skipped rather than rejected.
func Put(ctx context.Context, s Store, src Sniffer, collection, sourceURI string, body []byte) (Stored, error) {
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

	// Check whether the blob already exists. If it does, skip quota checks
	// since we're just bumping a refcount — no new storage consumed.
	// If the existence check itself errors, fail closed: a DB outage must
	// not silently bypass quota enforcement.
	pre, preErr := s.HasImage(ctx, hash)
	if preErr != nil {
		return Stored{}, fmt.Errorf("imagestore: has image: %w", preErr)
	}
	if pre {
		// Blob exists — dedup path: bump refcount only, no quota check.
		if err := s.PutImage(ctx, hash, mime, collection, sourceURI, body); err != nil {
			return Stored{}, fmt.Errorf("imagestore: put: %w", err)
		}
		return Stored{
			Hash:     hash,
			Mime:     mime,
			Inserted: false,
		}, nil
	}

	// New blob: enforce per-collection blob count quota before INSERT.
	// Dedup path above already returned, so this blob is genuinely new.
	// Rows with empty collection (migration 018 default for legacy rows)
	// do not count; CountBlobsForCollection excludes them.
	// Fail closed: a DB error reading the count must not silently bypass
	// the per-collection cap.
	if collection != "" {
		cnt, cerr := s.CountBlobsForCollection(ctx, collection)
		if cerr != nil {
			return Stored{}, fmt.Errorf("imagestore: count collection blobs: %w", cerr)
		}
		if cnt >= MaxBlobCountPerCollection {
			return Stored{
				Hash:    hash,
				Mime:    mime,
				Skipped: true,
			}, nil
		}
	}

	// New blob: enforce total byte quota before INSERT.
	// Fail closed: a DB error reading the total must not silently bypass
	// the global byte cap.
	totalBytes, terr := s.TotalBlobBytes(ctx)
	if terr != nil {
		return Stored{}, fmt.Errorf("imagestore: total blob bytes: %w", terr)
	}
	if totalBytes+int64(len(body)) > MaxTotalBlobBytes {
		return Stored{
			Hash:    hash,
			Mime:    mime,
			Skipped: true,
		}, nil
	}

	if err := s.PutImage(ctx, hash, mime, collection, sourceURI, body); err != nil {
		return Stored{}, fmt.Errorf("imagestore: put: %w", err)
	}
	return Stored{
		Hash:     hash,
		Mime:     mime,
		Inserted: true,
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

// ── IMG-1: Thumbnail generation + storage ───────────────────────────────

// ThumbnailSizes are the standard thumbnail widths generated during ingest.
var ThumbnailSizes = []int{128, 256, 512}

// StoreThumbnails generates thumbnail variants for a full-size image and
// stores them in the blob store linked to parentHash. Called after a
// successful image ingest (Put() returns inserted/deduped).
//
// Two formats are generated per size:
//   - JPEG (universal fallback, works in every browser)
//   - WebP (25-35% smaller, served to clients that advertise image/webp)
//
// Generation is best-effort: if a particular size/format fails (e.g.
// corrupted image data, WebP encoder unavailable), the remaining variants
// still get stored. Returns the count of successfully stored thumbnails.
//
// For WebP source images, JPEG thumbnails are transcoded (WebP decode →
// JPEG encode). WebP-to-WebP thumbnails use the pure-Go deepteams/webp
// encoder — no CGO required.
//
// Thumbnail rows have parentHash set, so they:
//   - Do NOT count toward per-collection blob quotas
//   - Can be looked up via idx_nft_image_blobs_parent_hash for serving
//   - Share the same sourceURI for audit trail
func StoreThumbnails(ctx context.Context, s Store, fullSizeBody []byte, fullSizeMime, parentHash, collection, sourceURI string) int {
	if parentHash == "" {
		return 0
	}

	// Two format generators — JPEG first (universal fallback),
	// WebP second (best-effort, ~30% smaller for modern browsers).
	generators := []func([]byte, string, int) ([]byte, string, error){
		generateThumb,
		generateThumbWebP,
	}

	stored := 0
	for _, size := range ThumbnailSizes {
		for _, gen := range generators {
			thumbBytes, thumbMime, err := gen(fullSizeBody, fullSizeMime, size)
			if err != nil || len(thumbBytes) == 0 {
				continue
			}
			thumbHash := Hash(thumbBytes)
			if thumbHash == "" {
				continue
			}

			// Check if thumbnail already exists (dedup across collections).
			if exists, err := s.HasImage(ctx, thumbHash); err != nil {
				// Transient DB error — skip this variant, don't block ingest.
				// The retry worker can regenerate on next cycle.
				continue
			} else if exists {
				stored++
				continue
			}

			if err := s.PutThumbnail(ctx, thumbHash, thumbMime, parentHash, collection, sourceURI, thumbBytes, size); err != nil {
				continue
			}
			stored++
		}
	}
	return stored
}
