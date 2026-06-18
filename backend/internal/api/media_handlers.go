package api

import (
	"context"
	"math/big"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/imagestore"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/media"
)

const maxInt64 = int64(1<<63 - 1)

// listingPreflightWithChain reports fillability and repairs stale nft_ownership
// rows by verifying the seller on-chain when the DB projection is missing.
func listingPreflightWithChain(q *db.Q, eth chain.Caller) fiber.Handler {
	return func(c *fiber.Ctx) error {
		coll := strings.ToLower(c.Params("collection"))
		tokenID := c.Params("id")
		seller := strings.ToLower(c.Query("seller"))
		if seller == "" {
			return writeErr(c, fiber.StatusBadRequest, "seller query param required")
		}
		pf, err := q.ListingPreflight(c.Context(), coll, tokenID, seller)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}

		// On-chain verify before buy: repair missing ownership or orphan stale listings.
		if pf.Listed && !pf.Orphaned && eth != nil {
			owns, std, amt, verified, verr := verifySellerOnChain(c.Context(), eth, coll, tokenID, seller)
			if verr == nil && verified {
				if owns {
					if !pf.SellerOwns {
						_ = q.EnsureListingSellerOwnership(c.Context(), coll, tokenID, seller, std, amt)
						pf.SellerOwns = true
					}
				} else {
					_ = q.OrphanListing(c.Context(), coll, tokenID, seller)
					pf.Orphaned = true
					pf.Listed = false
					pf.SellerOwns = false
				}
			}
		}

		ok := pf.Listed && pf.SellerOwns && !pf.Orphaned
		return c.JSON(fiber.Map{
			"ok":          ok,
			"listed":      pf.Listed,
			"orphaned":    pf.Orphaned,
			"seller_owns": pf.SellerOwns,
			"price_wei":   pf.PriceWei,
		})
	}
}

// verifySellerOnChain returns verified=true when RPC returned a definitive answer.
func verifySellerOnChain(ctx context.Context, eth chain.Caller, collection, tokenID, seller string) (owns bool, standard string, amount int64, verified bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	owner, err721 := chain.OwnerOf721(ctx, eth, collection, tokenID)
	if err721 == nil {
		if chain.SameAddr(owner, seller) {
			return true, "erc721", 1, true, nil
		}
		return false, "erc721", 1, true, nil
	}

	bal, err1155 := chain.Balance1155(ctx, eth, collection, tokenID, seller)
	if err1155 == nil {
		if bal.Sign() > 0 {
			return true, "erc1155", boundedPositiveAmount(bal), true, nil
		}
		return false, "erc1155", 0, true, nil
	}
	return false, "", 0, false, err721
}

func boundedPositiveAmount(v *big.Int) int64 {
	if v == nil || v.Sign() <= 0 {
		return 0
	}
	if !v.IsInt64() {
		return maxInt64
	}
	n := v.Int64()
	if n < 1 {
		return 1
	}
	return n
}

// mediaProxy serves external NFT images through same-origin with SSRF guards.
// When the URL points at a self-hosted blob (`/api/v1/img/<sha>`) it short-
// circuits into the same /api/v1/img handler so legacy clients re-encoding
// blob URLs (e.g. via the old /api/v1/media?url= helper) never touch the
// upstream path. http/https/ipfs URLs continue to proxy through
// media.FetchBytes with full SSRF guards.
func mediaProxy(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		raw := c.Query("url")
		if raw == "" {
			return c.Status(fiber.StatusBadRequest).SendString("invalid url")
		}
		if h := imagestore.ExtractHash(raw); h != "" {
			return imageByHash(q)(c)
		}
		if !media.ProxyAllowed(raw) {
			return c.Status(fiber.StatusBadRequest).SendString("invalid url")
		}
		body, err := media.FetchBytes(c.Context(), raw, "")
		if err != nil {
			return c.Status(fiber.StatusBadGateway).SendString("upstream unavailable")
		}
		ct, ok := media.SniffImage(body)
		if !ok {
			return c.Status(fiber.StatusUnsupportedMediaType).SendString("unsupported media type")
		}
		c.Set("Content-Type", ct)
		c.Set("Cache-Control", "public, max-age=86400")
		return c.Send(body)
	}
}

// imageByHash serves one blob by its SHA-256 hash from the self-hosted
// nft_image_blobs table. This is the primary read path for the frontend —
// gateway outages cannot affect tokens whose bytes are already stored.
//
// The handler validates the hash syntax, queries the row, and sends the
// stored bytes back with the same Content-Type the ingest worker recorded.
// It adds a long cache header because identical hashes mean identical bytes:
// the response is byte-for-byte safe to cache forever (refcount bookkeeping
// doesn't change content).
func imageByHash(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		sha := c.Params("sha256")
		if !imagestore.ValidateHash(sha) {
			return writeErr(c, fiber.StatusBadRequest, "invalid sha256")
		}
		blob, err := q.GetImage(c.Context(), sha)
		if err != nil {
			if imagestore.IsNoRows(err) {
				return writeErr(c, fiber.StatusNotFound, "blob not found")
			}
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		// STRICT re-sniff: never trust a stored Content-Type without verifying
		// the bytes still match. A future migration / admin fix could record
		// a wrong mime; the cache header below is 1 year so a stale mime
		// would poison every card. The middleware sniff runs ONCE on the
		// live bytes — image/* returns the sniffed mime verbatim, a JSON
		// metadata blob is served as application/json (verified against the
		// first byte), anything else is 415.
		if imgMime, isImg := media.SniffImage(blob.Body); isImg {
			c.Set("Content-Type", imgMime)
		} else if len(blob.Body) > 0 && blob.Body[0] == '{' {
			c.Set("Content-Type", "application/json")
		} else {
			return writeErr(c, fiber.StatusUnsupportedMediaType, "blob unfit for serve")
		}
		c.Set("Cache-Control", "public, max-age=31536000, immutable")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Imagestore-Sha256", sha)
		// TODO(scale): for > 8 MiB blobs under heavy traffic, switch to
		// c.SendStream(...) with an io.Reader backed by pgx's row reader so
		// concurrent requests don't multiply buffered RAM. Acceptable for
		// current traffic; flagged for the next loadtest-driven round.
		return c.Send(blob.Body)
	}
}

// imageRetryFetcher is the signature of media.FetchBytes. Wrapped through
// the handler factory so tests can stub the upstream fetch without spinning
// up an HTTP server.
type imageRetryFetcher func(ctx context.Context, uri, tokenID string) ([]byte, error)

// imageRetryNow forces immediate self-host of a token whose image_uri is
// still an upstream http(s)/(ipfs) URL. Wired under /api/v1/img/retry.
// The slow-path retry worker (indexer.runImageRetryWorker) does the same
// work on a 60-min cadence for every pending token; this endpoint lets the
// page surface run the same pipeline synchronously when the user is staring
// at a placeholder card.
//
// Intentionally NOT jwt-protected: anyone hitting it can trigger one
// upstream fetch + a content-addressed Put + a 2-row UPDATE; the per-IP
// rate limiter (60 req/min already applied by the api group) caps abuse.
// The user-triggered path does NOT bump image_retry_count — only the slow
// worker does — so a single click cannot push the worker's backoff
// forward and make the worker skip the token for up to 24h.
//
//   200 OK              {status:"ok",            image_uri:"/api/v1/img/<sha>"} — did the work
//   200 OK              {status:"already_local", image_uri:"/api/v1/img/<sha>"} — no work needed
//   400 Bad Request     {error:"<reason>"}        — missing coll/id or unsupported scheme
//   404 Not Found       {error:"<reason>"}        — metadata row missing / image_uri empty
//   502 Bad Gateway     {error:"<reason>"}        — fetch or sniff/store failed (gateway down)
//   500 Internal        {error:"<reason>"}        — db error during UpdateImageURI
func imageRetryNow(q *db.Q, fetch imageRetryFetcher) fiber.Handler {
	if fetch == nil {
		// Defensive: a nil fetcher would NPE on the first request and crash
		// the server. Production never hits this — Mount() passes
		// media.FetchBytes — but a test that forgets to inject one shouldn't
		// be able to bring down the process.
		fetch = media.FetchBytes
	}
	return func(c *fiber.Ctx) error {
		coll := strings.ToLower(strings.TrimSpace(c.Query("coll")))
		tokenID := strings.TrimSpace(c.Query("id"))
		if coll == "" || tokenID == "" {
			return writeErr(c, fiber.StatusBadRequest, "coll and id query params required")
		}

		// Pull whatever image_uri nft_tokens / nft_metadata JOIN currently
		// reports. Both tables are kept in sync by UpdateImageURI, so the
		// value is fresh as of the last successful ingest / retry.
		_, imageURI, err := q.GetTokenMeta(c.Context(), coll, tokenID)
		if err != nil {
			if imagestore.IsNoRows(err) || isNotFound(err) {
				return writeErr(c, fiber.StatusNotFound, "token metadata not found")
			}
			log.Warn().Err(err).Str("coll", coll).Str("token", tokenID).Msg("image-retry: db read failed")
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		imageURI = strings.TrimSpace(imageURI)
		if imageURI == "" {
			return writeErr(c, fiber.StatusNotFound, "no image_uri on file")
		}
		// Already self-hosted — the slow path has already done this work
		// since the page loaded. Don't burn a fetch on a no-op.
		if strings.HasPrefix(imageURI, imagestore.PathPrefix+"/") {
			return c.JSON(fiber.Map{
				"status":    "already_local",
				"image_uri": imageURI,
			})
		}
		if !isRetriableUpstream(imageURI) {
			return writeErr(c, fiber.StatusBadRequest, "image_uri is not an upstream URL")
		}

		body, ferr := fetch(c.Context(), imageURI, tokenID)
		if ferr != nil {
			log.Warn().Err(ferr).Str("coll", coll).Str("token", tokenID).Str("src", imageURI).
				Msg("image-retry: upstream fetch failed")
			return writeErr(c, fiber.StatusBadGateway, "upstream unavailable")
		}
		st, perr := imagestore.Put(c.Context(), q, media.SniffImage, imageURI, body)
		if perr != nil {
			log.Warn().Err(perr).Str("coll", coll).Str("token", tokenID).
				Msg("image-retry: imagestore put failed")
			return writeErr(c, fiber.StatusBadGateway, "self-host failed")
		}
		localPath := imagestore.PublicPath(st.Hash)
		if uerr := q.UpdateImageURI(c.Context(), coll, tokenID, localPath); uerr != nil {
			log.Warn().Err(uerr).Str("coll", coll).Str("token", tokenID).
				Msg("image-retry: db update failed")
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		log.Info().Str("coll", coll).Str("token", tokenID).Str("hash", st.Hash).Str("prev", imageURI).
			Msg("image-retry: self-hosted via user-triggered endpoint")
		return c.JSON(fiber.Map{
			"status":    "ok",
			"image_uri": localPath,
		})
	}
}

// isRetriableUpstream gates which URI schemes the retry endpoint will fetch.
// Symmetric with what the indexer worker's retryOneImage accepts (FetchBytes
// itself handles ipfs→http translation), so a work-driven retry and a
// click-driven retry make the same bring-into-cache decision.
func isRetriableUpstream(uri string) bool {
	return strings.HasPrefix(uri, "http://") ||
		strings.HasPrefix(uri, "https://") ||
		strings.HasPrefix(uri, "ipfs://")
}
