package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/imagestore"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/media"
)

var (
	tokenURISelector = crypto.Keccak256([]byte("tokenURI(uint256)"))[:4]
	uriSelector      = crypto.Keccak256([]byte("uri(uint256)"))[:4]
)

// rawMeta is the standard ERC-721/1155 metadata JSON shape.
type rawMeta struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Image        json.RawMessage `json:"image"`
	ImageURL     string          `json:"image_url"`
	AnimationURL string          `json:"animation_url"`
	Attributes   []struct {
		TraitType string          `json:"trait_type"`
		Value     json.RawMessage `json:"value"`
	} `json:"attributes"`
}

// runMetadataWorker lazily resolves off-chain metadata for owned tokens that have
// none yet: read tokenURI/uri on-chain, fetch the JSON, persist name/image/traits.
func (r *Runner) runMetadataWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tokens, err := r.q.ListTokensMissingMetadata(ctx, 25)
			if err != nil {
				log.Warn().Err(err).Msg("metadata: list missing")
				continue
			}
			for _, t := range tokens {
				if err := r.fetchOne(ctx, t); err != nil {
					// Warn (not Debug) so a sustained gateway outage surfaces
					// in default prod-log scraping, not just debug mode.
					log.Warn().Err(err).Str("coll", t.Collection).Str("token", t.TokenID).
						Msg("metadata: fetch skipped")
				}
			}
		}
	}
}

func (r *Runner) fetchOne(ctx context.Context, t db.MissingToken) error {
	uri, err := r.tokenURI(ctx, t)
	if err != nil {
		return fmt.Errorf("tokenURI: %w", err)
	}
	if uri == "" {
		// Empty tokenURI is a *valid* contract response (some testnet tokens
		// have no off-chain metadata). Persist a sentinel row so the indexer
		// drops the token from ListTokensMissingMetadata. We use MarkMissing
		// (no nft_tokens mirror, ON CONFLICT DO NOTHING) instead of
		// UpsertMetadata because UpsertMetadata would wipe any existing
		// nft_tokens.name/image mirror written by a prior successful fetch.
		if err := r.q.MarkMissing(ctx, t.Collection, t.TokenID); err != nil {
			return fmt.Errorf("sentinel metadata insert: %w", err)
		}
		log.Debug().Str("coll", t.Collection).Str("token", t.TokenID).
			Msg("metadata: token has no URI, marked sentinel")
		return nil
	}
	resolved := media.ResolveURI(uri, t.TokenID)

	body, err := media.FetchBytes(ctx, resolved, t.TokenID)
	if err != nil {
		return err
	}

	// Self-host the metadata JSON: store its bytes keyed by SHA-256 so the
	// frontend never has to reach the IPFS gateway again for this token.
	// FALL BACK to the upstream resolved URI when self-hosting fails (body
	// rejected by the JSON sniffer is the main case — a contract whose
	// tokenURI returns non-JSON); the indexer MUST NOT loop forever on a
	// single token whose bytes we can't store (UpsertMetadata below still
	// pulls the token out of ListTokensMissingMetadata so we won't retry it
	// on the next tick regardless).
	metaURI := resolved
	if metaSt, merr := imagestore.Put(ctx, r.q, sniffJSON, resolved, body); merr == nil && metaSt.Hash != "" {
		metaURI = imagestore.PublicPath(metaSt.Hash)
	} else if merr != nil {
		log.Warn().Err(merr).Str("coll", t.Collection).Str("token", t.TokenID).Str("src", resolved).
			Msg("metadata: self-host meta body rejected; using upstream URI")
	}

	var m rawMeta
	if err := json.Unmarshal(body, &m); err != nil {
		return fmt.Errorf("parse meta: %w", err)
	}

	// Self-host the image (if any). When the fetch / store fails we keep
	// the ORIGINAL upstream URI as a fallback so mediaProxy still serves it
	// best-effort until a future retry succeeds locally. The token's
	// metadata is never blocked on image storage.
	imageURI := ""
	if img := strings.TrimSpace(imageFromMeta(m)); img != "" {
		imgResolved := media.ResolveURI(img, t.TokenID)
		if imgBody, ferr := media.FetchBytes(ctx, imgResolved, t.TokenID); ferr == nil {
			if imgSt, perr := imagestore.Put(ctx, r.q, media.SniffImage, imgResolved, imgBody); perr == nil && imgSt.Hash != "" {
				imageURI = imagestore.PublicPath(imgSt.Hash)
			}
		}
		if imageURI == "" {
			imageURI = imgResolved
			log.Warn().Str("coll", t.Collection).Str("token", t.TokenID).Str("src", imgResolved).
				Msg("metadata: image not self-hosted at ingest; will retry via slow-path worker")
		}
	}

	animationURI := ""
	if m.AnimationURL != "" {
		if r := media.ResolveURI(m.AnimationURL, t.TokenID); r != "" {
			animationURI = r
		}
	}

	traits := make([]db.Trait, 0, len(m.Attributes))
	for _, a := range m.Attributes {
		if a.TraitType == "" {
			continue
		}
		traits = append(traits, db.Trait{Type: a.TraitType, Value: jsonScalar(a.Value)})
	}
	return r.q.UpsertMetadata(ctx, t.Collection, t.TokenID,
		m.Name, m.Description, imageURI, animationURI, metaURI, traits)
}

// sniffJSON is the dedicated MIME sniffer for ERC-721/1155 metadata JSON.
// The body MUST begin with a `{` after optional leading whitespace; anything
// else is rejected so we never store opaque junk under a JSON Content-Type
// (the frontend caches the response, so mis-labelling would poison cards).
func sniffJSON(body []byte) (string, bool) {
	for len(body) > 0 {
		switch body[0] {
		case ' ', '\t', '\n', '\r':
			body = body[1:]
		default:
			if body[0] == '{' {
				return "application/json", true
			}
			return "", false
		}
	}
	return "", false
}

// imageFromMeta extracts a URL from flat or OpenSea-style nested image fields.
func imageFromMeta(m rawMeta) string {
	if s := strings.TrimSpace(m.ImageURL); s != "" {
		return s
	}
	if len(m.Image) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(m.Image, &s) == nil && strings.TrimSpace(s) != "" {
		return s
	}
	var obj struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(m.Image, &obj) == nil {
		return strings.TrimSpace(obj.URL)
	}
	return ""
}

// ── Slow-path image retry worker ───────────────────────────────────────────────

// runImageRetryWorker periodically re-attempts self-hosting images for tokens
// whose image_uri is still an upstream http(s) URL (self-hosting failed during
// ingest). Runs on a 60-minute cadence — these are not time-critical and a
// failed gateway will be retried indefinitely until it succeeds.
func (r *Runner) runImageRetryWorker(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.retryPendingImages(ctx)
		}
	}
}

func (r *Runner) retryPendingImages(ctx context.Context) {
	tokens, err := r.q.ListTokensWithUpstreamImages(ctx, 50)
	if err != nil {
		log.Warn().Err(err).Msg("image-retry: list candidates")
		return
	}
	if len(tokens) == 0 {
		return
	}
	log.Info().Int("count", len(tokens)).Msg("image-retry: attempting self-host for upstream image URIs")
	for _, t := range tokens {
		if err := r.retryOneImage(ctx, t); err != nil {
			log.Debug().Err(err).Str("coll", t.Collection).Str("token", t.TokenID).Int("attempts", t.RetryCount+1).
				Msg("image-retry: still failed, will retry next cycle")
		}
	}
}

func (r *Runner) retryOneImage(ctx context.Context, t db.ImageRetryToken) (retErr error) {
	// On any failure, bump the retry count for exponential backoff.
	defer func() {
		if retErr != nil {
			if berr := r.q.BumpImageRetry(ctx, t.Collection, t.TokenID, t.RetryCount); berr != nil {
				log.Warn().Err(berr).Str("coll", t.Collection).Str("token", t.TokenID).
					Msg("image-retry: failed to bump retry count")
			}
		}
	}()

	imgBody, err := media.FetchBytes(ctx, t.ImageURI, t.TokenID)
	if err != nil {
		return fmt.Errorf("fetch image: %w", err)
	}
	st, err := imagestore.Put(ctx, r.q, media.SniffImage, t.ImageURI, imgBody)
	if err != nil {
		return fmt.Errorf("imagestore put: %w", err)
	}
	if st.Hash == "" {
		return fmt.Errorf("imagestore returned empty hash")
	}
	localPath := imagestore.PublicPath(st.Hash)

	// Update both nft_metadata and nft_tokens atomically (resets retry tracking).
	if err := r.q.UpdateImageURI(ctx, t.Collection, t.TokenID, localPath); err != nil {
		return fmt.Errorf("update image_uri: %w", err)
	}
	log.Info().Str("coll", t.Collection).Str("token", t.TokenID).Str("hash", st.Hash).
		Msg("image-retry: self-hosted successfully")
	return nil
}

// tokenURI reads tokenURI(id) for ERC-721 or uri(id) for ERC-1155 via eth_call.
func (r *Runner) tokenURI(ctx context.Context, t db.MissingToken) (string, error) {
	sel := tokenURISelector
	if strings.EqualFold(t.Standard, "erc1155") {
		sel = uriSelector
	}
	id, ok := new(big.Int).SetString(t.TokenID, 10)
	if !ok {
		return "", fmt.Errorf("bad token id")
	}
	idBytes := make([]byte, 32)
	id.FillBytes(idBytes)
	data := append(append([]byte(nil), sel...), idBytes...)

	to := common.HexToAddress(t.Collection)
	out, err := r.eth.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return "", err
	}
	return decodeABIString(out), nil
}

// decodeABIString decodes a single ABI-encoded string return value.
func decodeABIString(b []byte) string {
	if len(b) < 64 {
		return ""
	}
	off := new(big.Int).SetBytes(b[0:32]).Int64()
	if off+32 > int64(len(b)) {
		return ""
	}
	n := new(big.Int).SetBytes(b[off : off+32]).Int64()
	start := off + 32
	if start+n > int64(len(b)) || n < 0 {
		return ""
	}
	return string(b[start : start+n])
}

// jsonScalar stringifies a JSON trait value (string or number).
func jsonScalar(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var str string
		if json.Unmarshal(raw, &str) == nil {
			return str
		}
	}
	return strings.Trim(s, `"`)
}
