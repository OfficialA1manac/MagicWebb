package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/frontend"
)

// staticTokenMeta maps the embedded static metadata filenames to their
// sequential token IDs. The MagicWebbNFT contract mints sequentially starting
// at ID 1; each token's tokenURI() returns a URL pointing to the corresponding
// static file. Seeding the DB from these files at startup eliminates the
// circular dependency where the metadata worker would otherwise:
//   1. Call tokenURI(id) on-chain → gets URL to own server
//   2. HTTP-fetch that URL (hitting the same server)
//   3. Parse and store the result
//
// Order is the mint order: Umbra (token 1), Titan (2), Raven (3), Blade (4).
// When new NFTs are minted, append the new entry here.
var staticTokenMeta = []struct {
	FileName string // filename in frontend/static/nft/metadata/
	TokenID  string // decimal token ID
	ImageSVG string // relative path to the SVG in frontend/static/nft/
}{
	{FileName: "cid-kagenou.json", TokenID: "1", ImageSVG: "/static/nft/umbra.svg"},
	{FileName: "garou.json",       TokenID: "2", ImageSVG: "/static/nft/titan.svg"},
	{FileName: "itachi-uchiha.json", TokenID: "3", ImageSVG: "/static/nft/raven.svg"},
	{FileName: "will-serfort.json", TokenID: "4", ImageSVG: "/static/nft/blade.svg"},
}

// rawMetaJSON mirrors the static JSON file structure for parsing.
type rawMetaJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Image       string `json:"image"`
	ImageURL    string `json:"image_url"`
	Attributes  []struct {
		TraitType string          `json:"trait_type"`
		Value     json.RawMessage `json:"value"`
	} `json:"attributes"`
}

// seedStaticMetadata reads the embedded static NFT metadata JSON files and
// inserts (or updates) them into the database. This runs once at startup so
// the metadata worker never needs to fetch the first-party collection's
// metadata from the network — the static path is the canonical source.
//
// The function:
//  1. Skips if NFT_ADDR is empty (no first-party collection configured).
//  2. Checks if metadata already exists for token 1; if so, skips entirely
//     (idempotent on restart).
//  3. Reads each static file from the embedded FS.
//  4. Builds metadata and image URIs as same-origin relative paths.
//  5. Inserts into nft_metadata, nft_tokens, and nft_attributes via
//     q.UpsertMetadata().
//
// Returns the number of tokens seeded, or 0 if already seeded.
func seedStaticMetadata(ctx context.Context, q *db.Q, nftAddr string) int {
	if nftAddr == "" {
		log.Info().Msg("static-metadata: NFT_ADDR is empty, skipping seed")
		return 0
	}

	nftAddr = strings.ToLower(nftAddr)

	// Idempotency check: if token 1 already has metadata, skip entirely.
	name, _, err := q.GetTokenMeta(ctx, nftAddr, staticTokenMeta[0].TokenID)
	if err == nil && name != "" {
		log.Info().Str("collection", nftAddr).
			Str("token", staticTokenMeta[0].TokenID).
			Str("name", name).
			Msg("static-metadata: already seeded, skipping")
		return 0
	}

	// Verify the embedded metadata directory is accessible.
	subFS, err := fs.Sub(frontend.FS, "static/nft/metadata")
	if err != nil {
		log.Warn().Err(err).Msg("static-metadata: cannot open embedded directory, skipping")
		return 0
	}

	// Build a set of available files for lookup.
	entries, err := fs.ReadDir(subFS, ".")
	if err != nil {
		log.Warn().Err(err).Msg("static-metadata: cannot read embedded directory, skipping")
		return 0
	}
	available := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			available[e.Name()] = true
		}
	}

	seeded := 0
	for _, t := range staticTokenMeta {
		if !available[t.FileName] {
			log.Warn().Str("file", t.FileName).
				Msg("static-metadata: file not found in embedded FS, skipping")
			continue
		}

		body, err := fs.ReadFile(subFS, t.FileName)
		if err != nil {
			log.Warn().Err(err).Str("file", t.FileName).
				Msg("static-metadata: read failed, skipping")
			continue
		}

		var m rawMetaJSON
		if err := json.Unmarshal(body, &m); err != nil {
			log.Warn().Err(err).Str("file", t.FileName).
				Msg("static-metadata: parse failed, skipping")
			continue
		}

		// Image URI: prefer the static SVG path, fall back to JSON field.
		imageURI := t.ImageSVG
		if imageURI == "" {
			imageURI = m.Image
			if m.ImageURL != "" {
				imageURI = m.ImageURL
			}
		}

		// Metadata URI as same-origin relative path.
		metaURI := "/static/nft/metadata/" + t.FileName

		// Extract traits.
		var traits []db.Trait
		for _, a := range m.Attributes {
			if a.TraitType == "" {
				continue
			}
			// Unmarshal the value as a simple string.
			var val string
			if err := json.Unmarshal(a.Value, &val); err != nil {
				// Not a quoted string — use raw bytes trimmed of quotes.
				val = strings.Trim(string(a.Value), `"`)
			}
			if val == "" {
				continue
			}
			traits = append(traits, db.Trait{Type: a.TraitType, Value: val})
		}

		// Upsert metadata + attributes atomically.
		if err := q.UpsertMetadata(ctx, nftAddr, t.TokenID,
			m.Name, m.Description, imageURI, "", metaURI, traits,
		); err != nil {
			log.Warn().Err(err).Str("collection", nftAddr).Str("token", t.TokenID).
				Msg("static-metadata: upsert failed")
			continue
		}

		log.Debug().Str("collection", nftAddr).Str("token", t.TokenID).
			Str("name", m.Name).Msg("static-metadata: seeded")
		seeded++
	}

	if seeded > 0 {
		log.Info().Int("seeded", seeded).Str("collection", nftAddr).
			Msg("static-metadata: done — metadata worker will skip these tokens")
	} else {
		log.Warn().Str("collection", nftAddr).
			Msg("static-metadata: no tokens seeded (may be already seeded or files missing)")
	}

	return seeded
}

// listStaticMetadataFiles returns all .json filenames from the embedded static
// metadata directory, sorted. Used for startup logging only.
func listStaticMetadataFiles() []string {
	subFS, err := fs.Sub(frontend.FS, "static/nft/metadata")
	if err != nil {
		return nil
	}
	entries, err := fs.ReadDir(subFS, ".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// logStaticMetadataStatus logs the embedded static metadata files available
// at startup. Non-fatal; purely informational. Called from main().
func logStaticMetadataStatus() {
	if _, err := fs.Stat(frontend.FS, filepath.ToSlash("static/nft/metadata")); err != nil {
		log.Warn().Err(err).Str("path", "static/nft/metadata").
			Msg("static-metadata: embedded directory not found")
		return
	}
	files := listStaticMetadataFiles()
	if len(files) > 0 {
		log.Info().Int("count", len(files)).Strs("files", files).
			Msg("static-metadata: embedded files available")
	} else {
		log.Warn().Msg("static-metadata: no .json files found in embedded directory")
	}
}
