package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

func main() {
	// All queries from persisted_queries.go, with their names.
	queries := []struct{ name, query string }{
		{"Collection (parameterized)", "query Collection($address: String!) {\n  collection(address: $address) {\n    address name symbol standard deployBlock verified\n    stats { floorPriceWei volume24hWei listedCount }\n    listings(limit: 48, sort: \"recent\") {\n      collection tokenID seller priceWei amount standard\n      expiresAt listedAt name imageURI collectionVerified\n    }\n  }\n}"},
		{"Listings (parameterized)", "query Listings($collection: String, $seller: String, $sort: String, $limit: Int, $minPrice: String, $maxPrice: String, $traits: String) {\n  listings(collection: $collection, seller: $seller, sort: $sort, limit: $limit, minPrice: $minPrice, maxPrice: $maxPrice, traits: $traits) {\n    collection tokenID seller priceWei amount standard\n    expiresAt listedAt name imageURI collectionVerified\n  }\n}"},
		{"Auctions (parameterized)", "query Auctions($collection: String, $seller: String, $status: String, $limit: Int, $minPrice: String, $maxPrice: String) {\n  auctions(collection: $collection, seller: $seller, status: $status, limit: $limit, minPrice: $minPrice, maxPrice: $maxPrice) {\n    auctionId collection tokenID seller reservePriceWei\n    highestBidWei highestBidder status startsAt endsAt\n    name imageURI\n  }\n}"},
		{"Auction (parameterized)", "query Auction($id: Int!) {\n  auction(id: $id) {\n    auctionId collection tokenID seller reservePriceWei\n    highestBidWei highestBidder minIncrementBps status\n    startsAt endsAt createTx name imageURI\n    bids { bidder amountWei placedAt txHash }\n  }\n}"},
		{"Listings (homepage)", "{\n  listings(limit: 48, sort: \"recent\") {\n    collection tokenID seller priceWei amount standard\n    expiresAt listedAt name imageURI collectionVerified\n  }\n}"},
		{"Auctions (homepage)", "{\n  auctions(limit: 50, status: \"active\") {\n    auctionId collection tokenID seller reservePriceWei\n    highestBidWei highestBidder status startsAt endsAt\n    name imageURI\n  }\n}"},
		{"Collections (homepage)", "{\n  collections(limit: 50) {\n    address name symbol standard verified\n    stats { floorPriceWei volume24hWei listedCount }\n  }\n}"},
		{"Trending (homepage)", "{\n  trending(window: \"24h\", limit: 20) {\n    collection window score views bids volumeWei\n  }\n}"},
		{"Activity (parameterized)", "query Activity($limit: Int, $address: String, $collection: String, $tokenID: String) {\n  activity(limit: $limit, address: $address, collection: $collection, tokenID: $tokenID) {\n    type collection tokenID amountWei timestamp txHash\n  }\n}"},
		{"Metrics (homepage)", "{ metrics {\n    totalActiveListings totalSales grossVolumeWei\n    totalAuctions totalBids totalOffers\n  } }"},
	}

	type result struct {
		name      string
		fullBytes int
		hashBytes int
		reduction float64
		hash      string
	}

	var results []result
	for _, q := range queries {
		full := q.query
		fullSize := len(full)

		h := sha256.Sum256([]byte(full))
		hashStr := hex.EncodeToString(h[:])
		hashSize := 64

		reduction := (1.0 - float64(hashSize)/float64(fullSize)) * 100.0

		results = append(results, result{
			name:      q.name,
			fullBytes: fullSize,
			hashBytes: hashSize,
			reduction: reduction,
			hash:      hashStr,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].fullBytes > results[j].fullBytes
	})

	fmt.Printf("%-30s | %5s | %5s | %6s | %s\n", "Query", "POST", "Hash", "Redux", "SHA-256 (truncated)")
	fmt.Println(strings.Repeat("-", 100))
	for _, r := range results {
		fmt.Printf("%-30s | %5d | %5d | %5.0f%% | %s...%s\n",
			r.name, r.fullBytes, r.hashBytes, r.reduction,
			r.hash[:10], r.hash[54:])
	}

	// Summary stats
	var totalFull, totalHash int
	for _, r := range results {
		totalFull += r.fullBytes
		totalHash += r.hashBytes
	}
	avgReduction := (1.0 - float64(totalHash)/float64(totalFull)) * 100.0
	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("%-30s | %5d | %5d | %5.0f%% | (average)\\n", "TOTAL / AVERAGE", totalFull, totalHash, avgReduction)
}
