// Command svggen generates animated SVG artwork for NFT characters.
//
// Usage:
//   svggen --all                                           # generate all SVGs
//   svggen --all --format nft                              # SVGs + NFT metadata JSON
//   svggen --format json --all                             # only JSON metadata
//   svggen --name raven --format nft                       # SVG + JSON for one character
//   svggen --name titan --stdout                           # print SVG to stdout
//   svggen --name umbra --stdout --format json             # print JSON to stdout
//   svggen --list                                          # list available characters
//
// The generated SVGs are standalone, self-contained, and have zero
// external dependencies (fonts, images, etc.). Each SVG includes CSS
// keyframe animations for rich, living artwork.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/magicwebb/svggen"
)

func main() {
	var (
		flagName   = flag.String("name", "", "Character slug to generate (raven, titan, umbra, blade)")
		flagAll    = flag.Bool("all", false, "Generate all characters")
		flagList   = flag.Bool("list", false, "List available characters and exit")
		flagOutput = flag.String("output", "./out", "Output directory for generated files")
		flagFormat = flag.String("format", "svg", "Output format: svg (SVG only), json (metadata only), nft (SVG + JSON)")
		flagStdout = flag.Bool("stdout", false, "Print output to stdout instead of file (requires --name)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `svggen — Standalone Animated NFT SVG Art Generator

Generate animated SVG artwork and NFT-standard metadata JSON for
NFT marketplace characters. Fully portable, zero dependencies.

USAGE:
  svggen --all --format nft              SVG + JSON metadata for all
  svggen --all                           SVGs only for all characters
  svggen --name <slug>                   Generate one character's SVG
  svggen --name <slug> --format nft      SVG + JSON for one character
  svggen --name <slug> --stdout          Print SVG to stdout
  svggen --name <slug> --stdout --format json  Print JSON to stdout
  svggen --format json --all             Metadata JSON only
  svggen --list                          List available characters

FLAGS:
  --name <slug>    Character slug (raven, titan, umbra, blade)
  --all            Generate all characters
  --list           List available characters
  --output <dir>   Output directory (default: ./out)
  --format <fmt>   Output format: svg (default), json, nft
  --stdout         Print to stdout instead of file (requires --name)

FORMATS:
  svg              SVG artwork files only (*.svg)
  json             NFT metadata JSON only (*.json)
  nft              Both SVG artwork and metadata JSON (*.svg + *.json)

CHARACTERS:
`)
		for _, c := range svggen.GetCharacters() {
			fmt.Fprintf(os.Stderr, "  %-8s  %s\n", c.Slug, c.Name)
		}
	}

	flag.Parse()

	// Parse format flag
	fmtType, err := svggen.ParseFormat(*flagFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// ── List mode ────────────────────────────────────────────────────────
	if *flagList {
		fmt.Println("Available characters:")
		for _, c := range svggen.GetCharacters() {
			fmt.Printf("  %-8s  %s\n", c.Slug, c.Name)
			fmt.Printf("           %s\n\n", c.Description)
		}
		return
	}

	// ── Single character mode ────────────────────────────────────────────
	if *flagName != "" {
		if *flagStdout {
			switch fmtType {
			case svggen.FormatSVG:
				svg, err := svggen.GenerateSVG(*flagName)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				fmt.Print(svg)
			case svggen.FormatJSON, svggen.FormatNFT:
				jsonStr, err := svggen.GenerateMetadataJSON(*flagName)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				fmt.Println(jsonStr)
			}
			return
		}

		switch fmtType {
		case svggen.FormatSVG:
			path, err := svggen.SaveToFile(*flagName, *flagOutput)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✅ Generated: %s\n", path)
		case svggen.FormatJSON:
			path, err := svggen.SaveMetadataToFile(*flagName, *flagOutput)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✅ Generated: %s\n", path)
		case svggen.FormatNFT:
			svgPath, jsonPath, err := svggen.SaveNFT(*flagName, *flagOutput)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✅ Generated:\n   • %s\n   • %s\n", svgPath, jsonPath)
		}
		return
	}

	// ── Bulk mode (--all) ────────────────────────────────────────────────
	if *flagAll {
		results, err := svggen.GenerateAll(*flagOutput, fmtType)
		hadErrors := err != nil
		if err != nil {
			fmt.Fprintf(os.Stderr, "warnings:\n%v\n", err)
		}

		totalFiles := 0
		for _, files := range results {
			totalFiles += len(files)
		}
		fmt.Printf("✅ Generated %d files for %d characters in %s/\n", totalFiles, len(results), *flagOutput)
		for slug, files := range results {
			fmt.Printf("   • %-8s\n", slug)
			for _, f := range files {
				info, _ := os.Stat(f)
				size := ""
				if info != nil {
					size = fmt.Sprintf(" (%d bytes)", info.Size())
				}
				fmt.Printf("       └── %s%s\n", f, size)
			}
		}
		if hadErrors {
			os.Exit(1)
		}
		return
	}

	// ── No flags ─────────────────────────────────────────────────────────
	flag.Usage()
	os.Exit(1)
}
