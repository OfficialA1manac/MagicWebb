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
					log.Debug().Err(err).Str("coll", t.Collection).Str("token", t.TokenID).
						Msg("metadata: fetch skipped")
				}
			}
		}
	}
}

func (r *Runner) fetchOne(ctx context.Context, t db.MissingToken) error {
	uri, err := r.tokenURI(ctx, t)
	if err != nil || uri == "" {
		return fmt.Errorf("tokenURI: %w", err)
	}
	resolved := media.ResolveURI(uri, t.TokenID)

	body, err := media.FetchBytes(ctx, resolved, t.TokenID)
	if err != nil {
		return err
	}
	var m rawMeta
	if err := json.Unmarshal(body, &m); err != nil {
		return fmt.Errorf("parse meta: %w", err)
	}
	image := imageFromMeta(m)

	traits := make([]db.Trait, 0, len(m.Attributes))
	for _, a := range m.Attributes {
		if a.TraitType == "" {
			continue
		}
		traits = append(traits, db.Trait{Type: a.TraitType, Value: jsonScalar(a.Value)})
	}
	return r.q.UpsertMetadata(ctx, t.Collection, t.TokenID,
		m.Name, m.Description, media.ResolveURI(image, t.TokenID), media.ResolveURI(m.AnimationURL, t.TokenID), uri, traits)
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
