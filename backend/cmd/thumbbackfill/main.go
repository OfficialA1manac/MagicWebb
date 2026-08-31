// Command thumbbackfill generates thumbnail variants for image blobs that
// predate the ingest-time thumbnail hook.
//
// Why this exists: thumbnails are generated ONLY at ingest
// (internal/indexer/metadata.go), so every blob stored before that landed has
// zero variants. The serve path (internal/api/media.go) silently falls through
// to the FULL-SIZE bytea when a ?size= lookup misses, so those images keep
// shipping at full weight — measured 1,693,489 B for a grid tile that renders a
// few hundred pixels wide, versus 7,904 B at size=256.
//
// Without this backfill, the frontend's ?size= request is a no-op for every
// older image.
//
// Usage:
//
//	thumbbackfill [-limit N] [-dry-run]
//
// Reads POSTGRES_URL from the environment. Safe to re-run: it only selects
// blobs that still have no thumbnail rows, so completed work is never redone.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/imagestore"
)

func main() {
	limit := flag.Int("limit", 500, "maximum blobs to process in this run")
	batch := flag.Int("batch", 50, "blobs to claim per query")
	dryRun := flag.Bool("dry-run", false, "report what would be generated, write nothing")
	flag.Parse()

	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "FATAL: POSTGRES_URL is not set")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: connect: %v\n", err)
		os.Exit(2)
	}
	defer pool.Close()

	q := db.New(pool)
	var store imagestore.Store = q

	var processed, generated, skipped int
	for processed < *limit {
		n := *batch
		if remaining := *limit - processed; remaining < n {
			n = remaining
		}

		blobs, err := q.ListBlobsMissingThumbnails(ctx, n)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: list: %v\n", err)
			os.Exit(1)
		}
		if len(blobs) == 0 {
			break // nothing left to do
		}

		for _, b := range blobs {
			processed++

			if *dryRun {
				fmt.Printf("would backfill %s (%s) %s\n", b.SHA256[:12], b.Mime, b.SourceURI)
				continue
			}

			// Fetch the full-size body. This is the expensive part (a whole
			// bytea per blob), which is why the tool is batched and bounded.
			full, err := store.GetImage(ctx, b.SHA256)
			if err != nil {
				fmt.Fprintf(os.Stderr, "skip %s: fetch: %v\n", b.SHA256[:12], err)
				skipped++
				continue
			}

			// Best-effort by design, exactly as at ingest: a blob whose format
			// the resizer cannot decode (SVG, a truncated file) must not stop
			// the run. StoreThumbnails returns how many variants it wrote.
			wrote := imagestore.StoreThumbnails(ctx, store, full.Body, full.Mime, b.SHA256, "", b.SourceURI)
			if wrote == 0 {
				fmt.Fprintf(os.Stderr, "skip %s (%s): no variants generated\n", b.SHA256[:12], full.Mime)
				skipped++
				continue
			}
			generated += wrote
			fmt.Printf("backfilled %s (%s): %d variants\n", b.SHA256[:12], full.Mime, wrote)
		}

		// A blob that produced no variants still has no thumbnail rows, so the
		// next query would return it again forever. Stop once a full batch made
		// no progress rather than spinning.
		if !*dryRun && generated == 0 {
			fmt.Fprintln(os.Stderr, "no variants generated for an entire batch — stopping")
			break
		}
	}

	fmt.Printf("\ndone: %d blob(s) examined, %d variant(s) written, %d skipped\n",
		processed, generated, skipped)
	if skipped > 0 {
		fmt.Println("skipped blobs keep serving full-size; re-run after fixing their source format")
	}
}
