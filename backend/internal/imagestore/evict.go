package imagestore

import (
	"context"
	"fmt"
)

// EvictableBlob is one full-size blob (parent_hash IS NULL) no token references
// any more, oldest last_seen_at first. Bytes includes its thumbnail variants.
type EvictableBlob struct {
	Sha256 string
	Bytes  int64
}

// Evicter is the optional store capability the image-store health worker
// uses for LRU eviction (v3.6 wave 6). *db.Q implements it; a backend without
// it keeps the old behaviour (new blobs skipped at the cap).
type Evicter interface {
	// ListEvictableBlobs returns up to limit unreferenced full-size blobs,
	// least-recently-seen first.
	ListEvictableBlobs(ctx context.Context, limit int) ([]EvictableBlob, error)
	// DeleteBlob removes a blob and its thumbnail variants; returns bytes freed.
	DeleteBlob(ctx context.Context, sha256hex string) (int64, error)
}

// EvictUnreferenced deletes unreferenced blobs (LRU) until at least
// wantBytes have been freed or nothing evictable is left. Returns bytes and
// blobs freed. Bounded to one page of candidates per call so a huge backlog
// drains across worker ticks instead of one long transaction.
func EvictUnreferenced(ctx context.Context, ev Evicter, wantBytes int64) (freed int64, n int, err error) {
	if wantBytes <= 0 {
		return 0, 0, nil
	}
	cands, err := ev.ListEvictableBlobs(ctx, 100)
	if err != nil {
		return 0, 0, fmt.Errorf("imagestore: list evictable: %w", err)
	}
	for _, c := range cands {
		if freed >= wantBytes {
			break
		}
		if !ValidateHash(c.Sha256) {
			continue
		}
		b, derr := ev.DeleteBlob(ctx, c.Sha256)
		if derr != nil {
			return freed, n, fmt.Errorf("imagestore: delete %s: %w", c.Sha256, derr)
		}
		freed += b
		n++
	}
	return freed, n, nil
}
