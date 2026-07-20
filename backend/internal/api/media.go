package api

import (
	"context"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/imagestore"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/media"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ratelimit"
	"github.com/jackc/pgx/v5"
)

const maxInt64 = int64(1<<63 - 1)

// MediaService handles media proxy, image retry, and image-by-hash operations.
type MediaService struct {
	q        *db.Q
	eth      chain.Caller
	rl       *ratelimit.Limiter // v35: per-IP rate limit on image-retry endpoint
	fetch    imageRetryFetcher
	imgStore imagestore.Store // IMG-3: blob store backend (Postgres BYTEA by default)
}

// imageRetryFetcher is the signature of media.FetchBytes.
type imageRetryFetcher func(ctx context.Context, uri, tokenID string) ([]byte, error)

// NewMediaService creates a MediaService.
func NewMediaService(q *db.Q, eth chain.Caller, rl *ratelimit.Limiter) *MediaService {
	return &MediaService{q: q, eth: eth, rl: rl, fetch: media.FetchBytes}
}

// RegisterRoutes registers all media-related routes.
// The app-level /api/v1/img/:sha256 route is registered separately in Mount().
func (s *MediaService) RegisterRoutes(api fiber.Router) {
	api.Get("/media", ValidateQuery(QuerySchema{
		{Name: "url", Required: true, Type: ParamString},
		{Name: "id", Required: false, Type: ParamString},
	}), s.handleProxy)
	api.Post("/img/retry", s.handleRetry)
}

func (s *MediaService) handleProxy(c *fiber.Ctx) error {
	raw := c.Query("url")
	if raw == "" {
		return c.Status(fiber.StatusBadRequest).SendString("invalid url")
	}

	// Resolve ipfs://, bare CIDs (Qm…/baf…), and ar:// to HTTP gateway
	// URLs BEFORE the ProxyAllowedContext check, which only accepts
	// http/https schemes. The {id} placeholder can be filled when the
	// caller passes ?id=<token_id> alongside ?url=.
	tokenID := c.Query("id")
	resolved := media.ResolveURI(raw, tokenID)

	if h := imagestore.ExtractHash(resolved); h != "" {
		// Resolve local blob hash via the store (S3 or Postgres) directly
		// instead of calling imageByHash(c) which expects c.Params("sha256")
		// from the /api/v1/img/:sha256 route.
		if !imagestore.ValidateHash(h) {
			return writeErr(c, fiber.StatusBadRequest, "invalid sha256")
		}
		blob, err := s.store().GetImage(c.Context(), h)
		if err != nil {
			if imagestore.IsNoRows(err) {
				return writeErr(c, fiber.StatusNotFound, "blob not found")
			}
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if imgMime, isImg := media.SniffImage(blob.Body); isImg {
			c.Set("Content-Type", imgMime)
		} else if len(blob.Body) > 0 && blob.Body[0] == '{' {
			c.Set("Content-Type", "application/json")
		} else {
			return writeErr(c, fiber.StatusUnsupportedMediaType, "blob unfit for serve")
		}
		c.Set("Cache-Control", "public, max-age=31536000, immutable")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Imagestore-Sha256", h)
		return c.Send(blob.Body)
	}

	// data: URIs are self-contained; FetchBytes decodes them locally
	// without any network call, so skip the SSRF proxy check entirely.
	if strings.HasPrefix(resolved, "data:") {
		body, err := media.FetchBytes(c.Context(), raw, tokenID)
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

	// Use the DNS-aware ProxyAllowedContext at the entry gate instead of
	// the syntactic-only ProxyAllowed. Without this, an attacker could
	// supply a URL whose hostname is a legitimate public domain that
	// resolves (at DNS time through ProxyAllowedContext for redirects)
	// to a private IP — the entry-gate ProxyAllowed check only validated
	// the hostname syntax, not its resolved addresses, leaving a
	// DNS-rebinding SSRF window between the entry gate and the fetch.
	if !media.ProxyAllowedContext(c.Context(), resolved) {
		return c.Status(fiber.StatusBadRequest).SendString("invalid url")
	}
	body, err := media.FetchBytes(c.Context(), raw, tokenID)
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

// HandleImageByHash returns a handler for /api/v1/img/:sha256 (registered at app level).
func (s *MediaService) HandleImageByHash() fiber.Handler {
	return s.imageByHash
}

// store returns the blob store for image lookups (S3Store or db.Q).
func (s *MediaService) store() imagestore.Store {
	if s.imgStore != nil {
		return s.imgStore
	}
	return s.q
}

func (s *MediaService) imageByHash(c *fiber.Ctx) error {
	sha := c.Params("sha256")
	if !imagestore.ValidateHash(sha) {
		return writeErr(c, fiber.StatusBadRequest, "invalid sha256")
	}

	// IMG-1: ?size=128|256|512 requests a pre-generated thumbnail.
	// When present, look up the thumbnail by (parent_hash, width) instead
	// of serving the full-size blob directly. Falls back to the full-size
	// image when no thumbnail exists at the requested size (best-effort).
	//
	// Format negotiation via Accept header: clients that send
	// Accept: image/webp get WebP thumbnails (~30% smaller); all other
	// clients get the universal JPEG fallback.
	if sizeStr := c.Query("size"); sizeStr != "" {
		size, err := strconv.Atoi(sizeStr)
		if err != nil || (size != 128 && size != 256 && size != 512) {
			return writeErr(c, fiber.StatusBadRequest, "size must be 128, 256, or 512")
		}
		preferWebP := strings.Contains(c.Get("Accept"), "image/webp")
		if preferWebP {
			c.Append("Vary", "Accept") // merges with compress middleware's Accept-Encoding
		}
		blob, err := s.store().GetImageByParent(c.Context(), sha, size, preferWebP)
		if err != nil {
			if imagestore.IsNoRows(err) {
				// No thumbnail at this size — fall through to full-size.
			} else {
				return writeErr(c, fiber.StatusInternalServerError, "internal error")
			}
		} else {
			return serveBlob(c, sha, blob)
		}
		// Fall through: no thumbnail found, serve full-size.
	}

	blob, err := s.store().GetImage(c.Context(), sha)
	if err != nil {
		if imagestore.IsNoRows(err) {
			return writeErr(c, fiber.StatusNotFound, "blob not found")
		}
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return serveBlob(c, sha, blob)
}

// serveBlob writes a blob response with correct Content-Type, cache headers,
// and security headers. Shared by the full-size and thumbnail paths.
func serveBlob(c *fiber.Ctx, sha string, blob imagestore.Blob) error {
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
	return c.Send(blob.Body)
}

func (s *MediaService) handleRetry(c *fiber.Ctx) error {
	// v35: tighter per-IP rate limit (10 req/min) for the image-retry endpoint.
	// This endpoint triggers outbound HTTP fetches to upstream image URIs —
	// a single IP hammering it could exhaust the RPC pool or saturate the
	// upstream gateway. The SSRF guards (ProxyAllowedContext, safeDialContext)
	// remain unchanged; this rate limit is a defence-in-depth addition.
	if s.rl != nil {
		ip := ClientIP(c)
		if !s.rl.Allow("img-retry:"+ip, 10, time.Minute) {
			c.Set("Retry-After", "60")
			c.Set("X-RateLimit-Limit", "10")
			c.Set("X-RateLimit-Remaining", "0")
			c.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
	}

	fetch := s.fetch
	if fetch == nil {
		fetch = media.FetchBytes
	}
	coll := strings.ToLower(strings.TrimSpace(c.Query("coll")))
	tokenID := strings.TrimSpace(c.Query("id"))
	if coll == "" || tokenID == "" {
		return writeErr(c, fiber.StatusBadRequest, "coll and id query params required")
	}

	_, imageURI, err := s.q.GetTokenMeta(c.Context(), coll, tokenID)
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
	if strings.HasPrefix(imageURI, imagestore.PathPrefix+"/") {
		return c.JSON(fiber.Map{"status": "already_local", "image_uri": imageURI})
	}
	if !isRetriableUpstream(imageURI) {
		return writeErr(c, fiber.StatusBadRequest, "image_uri is not an upstream URL")
	}
	// SSRF gate: validate the resolved URI before fetching, not just
	// the original ipfs:// scheme. Without this, an attacker-controlled
	// token metadata could supply an image_uri with an ipfs:// scheme
	// that resolves through FetchBytes to an http gateway URL pointing
	// at a private IP — the isRetriableUpstream check only validates
	// the URI scheme, not the resolved address at the other end.
	resolved := imageURI
	if strings.HasPrefix(imageURI, "ipfs://") {
		resolved = media.ResolveURI(imageURI, tokenID)
	}
	if strings.HasPrefix(resolved, "http") && !media.ProxyAllowedContext(c.Context(), resolved) {
		return writeErr(c, fiber.StatusBadRequest, "image_uri resolves to disallowed address")
	}

	body, ferr := fetch(c.Context(), imageURI, tokenID)
	if ferr != nil {
		log.Warn().Err(ferr).Str("coll", coll).Str("token", tokenID).Str("src", imageURI).
			Msg("image-retry: upstream fetch failed")
		return writeErr(c, fiber.StatusBadGateway, "upstream unavailable")
	}
	st, perr := imagestore.Put(c.Context(), s.store(), media.SniffImage, coll, imageURI, body)
	if perr != nil {
		log.Warn().Err(perr).Str("coll", coll).Str("token", tokenID).
			Msg("image-retry: imagestore put failed")
		return writeErr(c, fiber.StatusBadGateway, "self-host failed")
	}
	if st.Skipped {
		log.Warn().Str("coll", coll).Str("token", tokenID).
			Msg("image-retry: quota exceeded, will retry later")
		return c.JSON(fiber.Map{"status": "quota_exceeded", "image_uri": ""})
	}
	localPath := imagestore.PublicPath(st.Hash)
	if uerr := s.q.UpdateImageURI(c.Context(), coll, tokenID, localPath); uerr != nil {
		log.Warn().Err(uerr).Str("coll", coll).Str("token", tokenID).
			Msg("image-retry: db update failed")
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	log.Info().Str("coll", coll).Str("token", tokenID).Str("hash", st.Hash).Str("prev", imageURI).
		Msg("image-retry: self-hosted via user-triggered endpoint")
	return c.JSON(fiber.Map{"status": "ok", "image_uri": localPath})
}

// isRetriableUpstream gates which URI schemes the retry endpoint will fetch.
func isRetriableUpstream(uri string) bool {
	return strings.HasPrefix(uri, "http://") ||
		strings.HasPrefix(uri, "https://") ||
		strings.HasPrefix(uri, "ipfs://")
}

// isClientGone reports whether err is just a torn-down request rather than a real DB failure.
func isClientGone(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if err == nil {
		return false
	}
	if errors.Is(err, pgx.ErrTxClosed) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer")
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

// listingPreflightWithChain reports fillability and repairs stale nft_ownership rows.
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

		if pf.Listed && !pf.Orphaned && eth != nil {
			owns, std, amt, verified, verr := verifySellerOnChain(c.Context(), eth, coll, tokenID, seller)
			if verr == nil && verified {
				if owns {
					if !pf.SellerOwns {
						if werr := q.EnsureListingSellerOwnership(c.Context(), coll, tokenID, seller, std, amt); werr != nil {
							if isClientGone(werr) {
								log.Debug().Err(werr).Str("coll", coll).Str("token", tokenID).Str("seller", seller).
									Msg("listing-preflight: client gone before repair write")
							} else {
								log.Warn().Err(werr).Str("coll", coll).Str("token", tokenID).Str("seller", seller).
									Msg("listing-preflight: failed to repair seller-owns row; reporting stale db state")
							}
						} else {
							pf.SellerOwns = true
						}
					}
				} else {
					if oerr := q.OrphanListing(c.Context(), coll, tokenID, seller); oerr != nil {
						if isClientGone(oerr) {
							log.Debug().Err(oerr).Str("coll", coll).Str("token", tokenID).Str("seller", seller).
								Msg("listing-preflight: client gone before orphan write")
						} else {
							log.Warn().Err(oerr).Str("coll", coll).Str("token", tokenID).Str("seller", seller).
								Msg("listing-preflight: failed to orphan stale listing; reporting stale db state")
						}
					} else {
						pf.Orphaned = true
						pf.Listed = false
						pf.SellerOwns = false
					}
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
