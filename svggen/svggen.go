// Package svggen generates standalone animated SVG artwork for NFT characters.
// It has zero external dependencies — only the Go standard library.
// Copy this entire directory to any Go project and use it immediately.
package svggen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Attribute represents a single NFT trait following OpenSea metadata standard.
type Attribute struct {
	TraitType string `json:"trait_type"`
	Value     string `json:"value"`
}

// NFTMetadata represents the full OpenSea-standard metadata JSON for an NFT.
type NFTMetadata struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Image       string      `json:"image"`
	Attributes  []Attribute `json:"attributes"`
}

// Character represents an NFT character with an animated SVG generator.
type Character struct {
	// Slug is the URL/filesystem-safe identifier (e.g. "raven").
	Slug string
	// Name is the display name (e.g. "Raven — Shadow of the Crimson Moon").
	Name string
	// Description provides lore context for the character.
	Description string
	// Attributes are the NFT trait list for OpenSea-standard metadata.
	Attributes []Attribute
	// Generate returns a full standalone SVG document (including <?xml?> and <svg> root).
	Generate func() string
}

// Registry of all available characters.
var registry []Character

// Register adds a character to the global registry. Called from init() in each
// character file so the tool works with zero configuration.
func Register(c Character) {
	for i, existing := range registry {
		if existing.Slug == c.Slug {
			registry[i] = c
			return
		}
	}
	registry = append(registry, c)
}

// GetCharacters returns a copy of the character registry.
func GetCharacters() []Character {
	out := make([]Character, len(registry))
	copy(out, registry)
	return out
}

// GetBySlug returns the character matching slug, or nil if not found.
func GetBySlug(slug string) *Character {
	for _, c := range registry {
		if strings.EqualFold(c.Slug, slug) {
			return &c
		}
	}
	return nil
}

// GenerateSVG generates the SVG string for the named character.
func GenerateSVG(slug string) (string, error) {
	c := GetBySlug(slug)
	if c == nil {
		return "", fmt.Errorf("svggen: unknown character %q (try: raven, titan, umbra, blade)", slug)
	}
	return c.Generate(), nil
}

// GenerateMetadataJSON generates OpenSea-standard NFT metadata JSON for the character.
// The image field is set to <slug>.svg by default.
func GenerateMetadataJSON(slug string) (string, error) {
	c := GetBySlug(slug)
	if c == nil {
		return "", fmt.Errorf("svggen: unknown character %q", slug)
	}
	meta := NFTMetadata{
		Name:        c.Name,
		Description: c.Description,
		Image:       c.Slug + ".svg",
		Attributes:  c.Attributes,
	}
	if meta.Attributes == nil {
		meta.Attributes = []Attribute{}
	}
	out, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", fmt.Errorf("svggen: marshal metadata: %w", err)
	}
	return string(out), nil
}

// SaveToFile writes the character's SVG to a file at outputDir/<slug>.svg.
// The slug is normalized to the canonical lowercase form from the Character struct.
func SaveToFile(slug, outputDir string) (string, error) {
	c := GetBySlug(slug)
	if c == nil {
		return "", fmt.Errorf("svggen: unknown character %q", slug)
	}
	canonSlug := c.Slug
	svg := c.Generate()
	outPath := filepath.Join(outputDir, canonSlug+".svg")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("svggen: mkdir: %w", err)
	}
	if err := os.WriteFile(outPath, []byte(svg), 0644); err != nil {
		return "", fmt.Errorf("svggen: write %s: %w", outPath, err)
	}
	return outPath, nil
}

// SaveMetadataToFile writes the character's NFT metadata JSON to a file.
// The slug is normalized to the canonical lowercase form from the Character struct.
func SaveMetadataToFile(slug, outputDir string) (string, error) {
	c := GetBySlug(slug)
	if c == nil {
		return "", fmt.Errorf("svggen: unknown character %q", slug)
	}
	canonSlug := c.Slug
	jsonStr, err := GenerateMetadataJSON(canonSlug)
	if err != nil {
		return "", err
	}
	outPath := filepath.Join(outputDir, canonSlug+".json")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("svggen: mkdir: %w", err)
	}
	if err := os.WriteFile(outPath, []byte(jsonStr), 0644); err != nil {
		return "", fmt.Errorf("svggen: write %s: %w", outPath, err)
	}
	return outPath, nil
}

// SaveNFT writes both the SVG and JSON metadata for a character.
// Returns (svgPath, jsonPath, error). The slug is normalized to the canonical
// lowercase form from the Character struct.
func SaveNFT(slug, outputDir string) (string, string, error) {
	c := GetBySlug(slug)
	if c == nil {
		return "", "", fmt.Errorf("svggen: unknown character %q", slug)
	}
	svgPath, err := SaveToFile(c.Slug, outputDir)
	if err != nil {
		return "", "", err
	}
	jsonPath, err := SaveMetadataToFile(c.Slug, outputDir)
	if err != nil {
		return svgPath, "", err
	}
	return svgPath, jsonPath, nil
}

// FormatType represents the output format for SVG generation.
type FormatType int

const (
	FormatSVG  FormatType = iota // Only SVG files (default)
	FormatJSON                   // Only JSON metadata files
	FormatNFT                    // Both SVG + JSON metadata files
)

// ParseFormat parses a format string into a FormatType.
func ParseFormat(s string) (FormatType, error) {
	switch strings.ToLower(s) {
	case "svg":
		return FormatSVG, nil
	case "json":
		return FormatJSON, nil
	case "nft":
		return FormatNFT, nil
	default:
		return FormatSVG, fmt.Errorf("svggen: unknown format %q (use: svg, json, nft)", s)
	}
}

// GenerateAll generates SVGs and/or metadata JSON for all registered characters.
// The format parameter controls what is written (SVG only, JSON only, or both).
// Returns a map of slug → list of output file paths.
func GenerateAll(outputDir string, format FormatType) (map[string][]string, error) {
	results := make(map[string][]string, len(registry))
	var errs []string
	for _, c := range registry {
		files, err := generateOne(c.Slug, outputDir, format)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		results[c.Slug] = files
	}
	if len(errs) > 0 {
		return results, fmt.Errorf("svggen: %d errors:\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return results, nil
}

func generateOne(slug, outputDir string, format FormatType) ([]string, error) {
	var files []string
	switch format {
	case FormatSVG:
		path, err := SaveToFile(slug, outputDir)
		if err != nil {
			return nil, err
		}
		files = append(files, path)
	case FormatJSON:
		path, err := SaveMetadataToFile(slug, outputDir)
		if err != nil {
			return nil, err
		}
		files = append(files, path)
	case FormatNFT:
		svgPath, jsonPath, err := SaveNFT(slug, outputDir)
		if err != nil {
			return nil, err
		}
		files = append(files, svgPath, jsonPath)
	}
	return files, nil
}

// ─── SVG helpers ──────────────────────────────────────────────────────────────

// SVG wraps content in a full SVG document with the standard viewBox and namespace.
// The animateCSS parameter is the contents of a <style> block (CSS @keyframes, classes).
func SVG(animateCSS, body string) string {
	doc := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1000 1000" width="100%%" height="100%%">
<defs>
%s
</defs>
%s
</svg>`, animateCSS, body)
	return doc
}

// GlowFilter returns a <filter> element for a glow effect.
// name: filter ID, color: glow color, stdDev: blur radius.
func GlowFilter(name, color string, stdDev float64) string {
	return fmt.Sprintf(`<filter id="%s" x="-50%%" y="-50%%" width="200%%" height="200%%">
  <feGaussianBlur in="SourceGraphic" stdDeviation="%.1f" result="blur"/>
  <feFlood flood-color="%s" flood-opacity="0.6" result="glowColor"/>
  <feComposite in="glowColor" in2="blur" operator="in" result="coloredBlur"/>
  <feMerge>
    <feMergeNode in="coloredBlur"/>
    <feMergeNode in="SourceGraphic"/>
  </feMerge>
</filter>`, name, stdDev, color)
}

// RadialGrad returns a <radialGradient> element.
func RadialGrad(id string, cx, cy, r float64, stops string) string {
	return fmt.Sprintf(`<radialGradient id="%s" cx="%.2f" cy="%.2f" r="%.2f">%s</radialGradient>`, id, cx, cy, r, stops)
}

// LinearGrad returns a <linearGradient> element.
func LinearGrad(id string, x1, y1, x2, y2 float64, stops string) string {
	return fmt.Sprintf(`<linearGradient id="%s" x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f">%s</linearGradient>`, id, x1, y1, x2, y2, stops)
}

// Stop returns a gradient <stop> element.
func Stop(offset, color string, opacity float64) string {
	return fmt.Sprintf(`<stop offset="%s" stop-color="%s" stop-opacity="%.2f"/>`, offset, color, opacity)
}
