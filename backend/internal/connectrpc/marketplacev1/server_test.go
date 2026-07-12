package marketplacev1

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// ── GetListing ──────────────────────────────────────────────────────────────

func TestGetListing_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs("0xabc", "1").
		WillReturnRows(pgxmock.NewRows([]string{
			"collection", "token_id", "seller", "price_wei", "amount",
			"standard", "expires_at", "listed_at", "tx_hash",
			"name", "image_uri",
		}).AddRow(
			"0xabc", "1", "0xseller", "1000000000000000000", int64(1),
			"erc721", now.Add(24*time.Hour), now, "0xtx",
			"MyToken", "https://example.com/img.png",
		))

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetListingRequest{
		Collection: "0xabc",
		TokenId:    "1",
	})
	resp, err := srv.GetListing(context.Background(), req)
	if err != nil {
		t.Fatalf("GetListing failed: %v", err)
	}

	if resp.Msg.Collection != "0xabc" {
		t.Fatalf("collection = %q, want %q", resp.Msg.Collection, "0xabc")
	}
	if resp.Msg.TokenId != "1" {
		t.Fatalf("token_id = %q, want %q", resp.Msg.TokenId, "1")
	}
	if resp.Msg.Seller != "0xseller" {
		t.Fatalf("seller = %q, want %q", resp.Msg.Seller, "0xseller")
	}
	if resp.Msg.PriceWei != "1000000000000000000" {
		t.Fatalf("price_wei = %q, want %q", resp.Msg.PriceWei, "1000000000000000000")
	}
	if resp.Msg.Amount != 1 {
		t.Fatalf("amount = %d, want %d", resp.Msg.Amount, 1)
	}
	if resp.Msg.Standard != "erc721" {
		t.Fatalf("standard = %q, want %q", resp.Msg.Standard, "erc721")
	}
	if resp.Msg.Name != "MyToken" {
		t.Fatalf("name = %q, want %q", resp.Msg.Name, "MyToken")
	}
	if resp.Msg.ImageUri != "https://example.com/img.png" {
		t.Fatalf("image_uri = %q, want %q", resp.Msg.ImageUri, "https://example.com/img.png")
	}
	if resp.Msg.TxHash != "0xtx" {
		t.Fatalf("tx_hash = %q, want %q", resp.Msg.TxHash, "0xtx")
	}
	if resp.Msg.ExpiresAtMs == 0 {
		t.Fatal("expires_at_ms should be non-zero")
	}
	if resp.Msg.ListedAtMs == 0 {
		t.Fatal("listed_at_ms should be non-zero")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetListing_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs("0xabc", "1").
		WillReturnError(pgx.ErrNoRows)

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetListingRequest{
		Collection: "0xabc",
		TokenId:    "1",
	})
	resp, err := srv.GetListing(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for not-found listing")
	}
	if resp != nil {
		t.Fatal("expected nil response for not-found listing")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %v", connect.CodeOf(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetListing_DBError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs("0xabc", "1").
		WillReturnError(errors.New("connection refused")) // arbitrary non-not-found error

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetListingRequest{
		Collection: "0xabc",
		TokenId:    "1",
	})
	_, err = srv.GetListing(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for DB failure")
	}
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected CodeInternal, got %v", connect.CodeOf(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── GetAuction ──────────────────────────────────────────────────────────────

func TestGetAuction_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT a\.auction_id, a\.collection`).
		WithArgs(int64(42)).
		WillReturnRows(pgxmock.NewRows([]string{
			"auction_id", "collection", "token_id", "seller", "standard",
			"reserve_price_wei", "highest_bid_wei", "highest_bidder", "min_increment_bps",
			"starts_at", "ends_at", "status", "create_tx", "name", "image_uri",
		}).AddRow(
			int64(42), "0xcol", "1", "0xseller", "erc721",
			"5000000000000000000", "6000000000000000000", "0xbidder", int16(100),
			now, now.Add(24*time.Hour), "active", "0xtx", "Auction 42", "",
		))

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetAuctionRequest{
		AuctionId: 42,
	})
	resp, err := srv.GetAuction(context.Background(), req)
	if err != nil {
		t.Fatalf("GetAuction failed: %v", err)
	}

	if resp.Msg.AuctionId != 42 {
		t.Fatalf("auction_id = %d, want %d", resp.Msg.AuctionId, 42)
	}
	if resp.Msg.Collection != "0xcol" {
		t.Fatalf("collection = %q, want %q", resp.Msg.Collection, "0xcol")
	}
	if resp.Msg.TokenId != "1" {
		t.Fatalf("token_id = %q, want %q", resp.Msg.TokenId, "1")
	}
	if resp.Msg.Seller != "0xseller" {
		t.Fatalf("seller = %q, want %q", resp.Msg.Seller, "0xseller")
	}
	if resp.Msg.ReservePriceWei != "5000000000000000000" {
		t.Fatalf("reserve_price_wei = %q, want %q", resp.Msg.ReservePriceWei, "5000000000000000000")
	}
	if resp.Msg.HighestBidWei != "6000000000000000000" {
		t.Fatalf("highest_bid_wei = %q, want %q", resp.Msg.HighestBidWei, "6000000000000000000")
	}
	if resp.Msg.HighestBidder != "0xbidder" {
		t.Fatalf("highest_bidder = %q, want %q", resp.Msg.HighestBidder, "0xbidder")
	}
	if resp.Msg.MinIncrementBps != 100 {
		t.Fatalf("min_increment_bps = %d, want %d", resp.Msg.MinIncrementBps, 100)
	}
	if resp.Msg.Status != "active" {
		t.Fatalf("status = %q, want %q", resp.Msg.Status, "active")
	}
	if resp.Msg.CreateTx != "0xtx" {
		t.Fatalf("create_tx = %q, want %q", resp.Msg.CreateTx, "0xtx")
	}
	if resp.Msg.Name != "Auction 42" {
		t.Fatalf("name = %q, want %q", resp.Msg.Name, "Auction 42")
	}
	if resp.Msg.StartsAtMs == 0 {
		t.Fatal("starts_at_ms should be non-zero")
	}
	if resp.Msg.EndsAtMs == 0 {
		t.Fatal("ends_at_ms should be non-zero")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetAuction_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectQuery(`SELECT a\.auction_id, a\.collection`).
		WithArgs(int64(99)).
		WillReturnError(pgx.ErrNoRows)

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetAuctionRequest{
		AuctionId: 99,
	})
	_, err = srv.GetAuction(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for not-found auction")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %v", connect.CodeOf(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetAuction_DBError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectQuery(`SELECT a\.auction_id, a\.collection`).
		WithArgs(int64(42)).
		WillReturnError(errors.New("connection refused"))

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetAuctionRequest{
		AuctionId: 42,
	})
	_, err = srv.GetAuction(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for DB failure")
	}
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected CodeInternal, got %v", connect.CodeOf(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── GetOffer ────────────────────────────────────────────────────────────────

func TestGetOffer_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT offer_id::text, bidder, collection, token_id::text, principal_wei::text`).
		WithArgs("42").
		WillReturnRows(pgxmock.NewRows([]string{
			"offer_id", "bidder", "collection", "token_id",
			"principal_wei", "fee_wei", "units", "standard",
			"expires_at", "status", "make_tx", "created_at",
		}).AddRow(
			"42", "0xbidder", "0xcol", "1",
			"1000000000000000000", "10000000000000000", int64(1), "erc721",
			now.Add(7*24*time.Hour), "pending", "0xmtx", now,
		))

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetOfferRequest{
		OfferId: "42",
	})
	resp, err := srv.GetOffer(context.Background(), req)
	if err != nil {
		t.Fatalf("GetOffer failed: %v", err)
	}

	if resp.Msg.OfferId != "42" {
		t.Fatalf("offer_id = %q, want %q", resp.Msg.OfferId, "42")
	}
	if resp.Msg.Bidder != "0xbidder" {
		t.Fatalf("bidder = %q, want %q", resp.Msg.Bidder, "0xbidder")
	}
	if resp.Msg.Collection != "0xcol" {
		t.Fatalf("collection = %q, want %q", resp.Msg.Collection, "0xcol")
	}
	if resp.Msg.TokenId != "1" {
		t.Fatalf("token_id = %q, want %q", resp.Msg.TokenId, "1")
	}
	if resp.Msg.AmountWei != "1000000000000000000" {
		t.Fatalf("amount_wei = %q, want %q", resp.Msg.AmountWei, "1000000000000000000")
	}
	if resp.Msg.FeeWei != "10000000000000000" {
		t.Fatalf("fee_wei = %q, want %q", resp.Msg.FeeWei, "10000000000000000")
	}
	if resp.Msg.Units != 1 {
		t.Fatalf("units = %d, want %d", resp.Msg.Units, 1)
	}
	if resp.Msg.Standard != "erc721" {
		t.Fatalf("standard = %q, want %q", resp.Msg.Standard, "erc721")
	}
	if resp.Msg.Status != "pending" {
		t.Fatalf("status = %q, want %q", resp.Msg.Status, "pending")
	}
	if resp.Msg.MakeTx != "0xmtx" {
		t.Fatalf("make_tx = %q, want %q", resp.Msg.MakeTx, "0xmtx")
	}
	if resp.Msg.ExpiresAtMs == 0 {
		t.Fatal("expires_at_ms should be non-zero")
	}
	if resp.Msg.CreatedAtMs == 0 {
		t.Fatal("created_at_ms should be non-zero")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetOffer_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectQuery(`SELECT offer_id::text, bidder, collection, token_id::text, principal_wei::text`).
		WithArgs("nonexistent").
		WillReturnError(pgx.ErrNoRows)

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetOfferRequest{
		OfferId: "nonexistent",
	})
	_, err = srv.GetOffer(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for not-found offer")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %v", connect.CodeOf(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetOffer_DBError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectQuery(`SELECT offer_id::text, bidder, collection, token_id::text, principal_wei::text`).
		WithArgs("42").
		WillReturnError(errors.New("connection refused"))

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetOfferRequest{
		OfferId: "42",
	})
	_, err = srv.GetOffer(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for DB failure")
	}
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected CodeInternal, got %v", connect.CodeOf(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── GetToken ────────────────────────────────────────────────────────────────

func TestGetToken_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT COALESCE\(m\.name, t\.name, ''\)`).
		WithArgs("0xabc", "1").
		WillReturnRows(pgxmock.NewRows([]string{
			"name", "description", "image_uri", "animation_uri", "metadata_uri", "fetched_at",
		}).AddRow(
			"My Token", "A cool token", "https://img.com/1.png", "https://anim.com/1.mp4", "https://meta.com/1.json", now,
		))

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetTokenRequest{
		Collection: "0xabc",
		TokenId:    "1",
	})
	resp, err := srv.GetToken(context.Background(), req)
	if err != nil {
		t.Fatalf("GetToken failed: %v", err)
	}

	if resp.Msg.Collection != "0xabc" {
		t.Fatalf("collection = %q, want %q", resp.Msg.Collection, "0xabc")
	}
	if resp.Msg.TokenId != "1" {
		t.Fatalf("token_id = %q, want %q", resp.Msg.TokenId, "1")
	}
	if resp.Msg.Name != "My Token" {
		t.Fatalf("name = %q, want %q", resp.Msg.Name, "My Token")
	}
	if resp.Msg.Description != "A cool token" {
		t.Fatalf("description = %q, want %q", resp.Msg.Description, "A cool token")
	}
	if resp.Msg.ImageUri != "https://img.com/1.png" {
		t.Fatalf("image_uri = %q, want %q", resp.Msg.ImageUri, "https://img.com/1.png")
	}
	if resp.Msg.AnimationUri != "https://anim.com/1.mp4" {
		t.Fatalf("animation_uri = %q, want %q", resp.Msg.AnimationUri, "https://anim.com/1.mp4")
	}
	if resp.Msg.MetadataUri != "https://meta.com/1.json" {
		t.Fatalf("metadata_uri = %q, want %q", resp.Msg.MetadataUri, "https://meta.com/1.json")
	}
	if resp.Msg.FetchedAtMs == 0 {
		t.Fatal("fetched_at_ms should be non-zero")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetToken_NotFound(t *testing.T) {
	// GetTokenFullMetadata swallows pgx.ErrNoRows and returns empty strings
	// with nil error when both the JOIN query and the fallback return no rows.
	// The handler returns a success response with empty/falsy fields.
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectQuery(`SELECT COALESCE\(m\.name, t\.name, ''\)`).
		WithArgs("0xmissing", "999").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery(`SELECT COALESCE\(name,''\).*FROM nft_metadata`).
		WithArgs("0xmissing", "999").
		WillReturnError(pgx.ErrNoRows)

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetTokenRequest{
		Collection: "0xmissing",
		TokenId:    "999",
	})
	resp, err := srv.GetToken(context.Background(), req)
	if err != nil {
		t.Fatalf("expected success (nil error) for unindexed token, got %v", err)
	}
	if resp.Msg.Name != "" || resp.Msg.Description != "" {
		t.Fatal("expected empty name/description for unindexed token")
	}
	if resp.Msg.FetchedAtMs != 0 {
		t.Fatal("expected zero fetched_at_ms for unindexed token")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetToken_DBError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectQuery(`SELECT COALESCE\(m\.name, t\.name, ''\)`).
		WithArgs("0xabc", "1").
		WillReturnError(errors.New("connection reset"))

	srv := NewServer(db.New(mock))
	req := connect.NewRequest(&GetTokenRequest{
		Collection: "0xabc",
		TokenId:    "1",
	})
	_, err = srv.GetToken(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for DB failure")
	}
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected CodeInternal, got %v", connect.CodeOf(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

