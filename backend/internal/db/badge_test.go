package db

import (
	"context"
	"reflect"
	"testing"

	"github.com/pashagolub/pgxmock/v4"
)

// The ✓ checkmark means "this NFT fully works here": verified collection,
// metadata present, image served from our store, holder known. Every unmet
// check is named so the UI can say why.
func TestComputeTokenBadge(t *testing.T) {
	cases := []struct {
		name       string
		cv         bool
		tokenName  string
		image      string
		ownerKnown bool
		ok         bool
		reasons    []string
	}{
		{"all good (stored image)", true, "Genesis #1", "/api/v1/img/abc", true, true, []string{}},
		{"all good (/img alias)", true, "Genesis #1", "/img/abc", true, true, []string{}},
		{"all good (inline data uri)", true, "Genesis #1", "data:image/svg+xml;base64,AAA", true, true, []string{}},
		{"unverified collection", false, "x", "/api/v1/img/abc", true, false, []string{ReasonUnverifiedCollection}},
		{"no name", true, "  ", "/api/v1/img/abc", true, false, []string{ReasonNoMetadata}},
		{"remote image not stored", true, "x", "https://example.com/a.png", true, false, []string{ReasonImageMissing}},
		{"ipfs image not stored", true, "x", "ipfs://Qm", true, false, []string{ReasonImageMissing}},
		{"owner unknown", true, "x", "/api/v1/img/abc", false, false, []string{ReasonOwnerUnknown}},
		{"everything missing", false, "", "", false, false, []string{ReasonUnverifiedCollection, ReasonNoMetadata, ReasonImageMissing, ReasonOwnerUnknown}},
	}
	for _, c := range cases {
		ok, reasons := ComputeTokenBadge(c.cv, c.tokenName, c.image, c.ownerKnown)
		if ok != c.ok || !reflect.DeepEqual(reasons, c.reasons) {
			t.Fatalf("%s: got ok=%v reasons=%v, want ok=%v reasons=%v", c.name, ok, reasons, c.ok, c.reasons)
		}
	}
}

// ★ requires holder == creator AND minter == creator; the minter lookup fires
// once per page and only for candidate rows.
func TestFillTokenBadgesCreatorIsOwner(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)
	creator := "0x00000000000000000000000000000000000000c1"
	rows := []ListingRow{
		{Collection: "0xAAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TokenID: "1", Seller: creator, Name: "A", ImageURI: "/api/v1/img/a", CollectionVerified: true, CollectionCreator: creator},                                      // minted by creator → ★
		{Collection: "0xAAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TokenID: "2", Seller: creator, Name: "B", ImageURI: "/api/v1/img/b", CollectionVerified: true, CollectionCreator: creator},                                      // minted by someone else → no ★
		{Collection: "0xAAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TokenID: "3", Seller: "0x00000000000000000000000000000000000000d2", Name: "C", ImageURI: "/api/v1/img/c", CollectionVerified: true, CollectionCreator: creator}, // holder ≠ creator → not a candidate
	}
	mock.ExpectQuery(`SELECT t.collection, t.token_id::text, lower\(t.minter\)`).
		WithArgs([]string{"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, []string{"1", "2"}).
		WillReturnRows(pgxmock.NewRows([]string{"collection", "token_id", "minter"}).
			AddRow("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "1", creator).
			AddRow("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "2", "0x00000000000000000000000000000000000000d2"))

	q.fillTokenBadges(context.Background(), asBadgeRows[ListingRow, *ListingRow](rows))
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if !rows[0].Verified || !rows[0].CreatorIsOwner {
		t.Fatalf("row0: %+v", rows[0].TokenBadge)
	}
	if !rows[1].Verified || rows[1].CreatorIsOwner {
		t.Fatalf("row1 (minted by another): %+v", rows[1].TokenBadge)
	}
	if !rows[2].Verified || rows[2].CreatorIsOwner {
		t.Fatalf("row2 (holder not creator): %+v", rows[2].TokenBadge)
	}
}

// No candidate rows → no minter query at all (cheap for the common page).
func TestFillTokenBadgesNoCandidatesNoQuery(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)
	rows := []OfferRow{{Collection: "0xa", TokenID: "1", Name: "A", ImageURI: "ipfs://x", CollectionVerified: true}}
	q.fillTokenBadges(context.Background(), asBadgeRows[OfferRow, *OfferRow](rows))
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if rows[0].Verified || !reflect.DeepEqual(rows[0].VerifiedReason, []string{ReasonImageMissing}) {
		t.Fatalf("offer row: %+v", rows[0].TokenBadge)
	}
}

// A minter lookup failure withholds ★ but never fails the caller, and ✓ stays computed.
func TestFillTokenBadgesLookupFailureIsSoft(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)
	creator := "0x00000000000000000000000000000000000000c1"
	d := TokenDetail{Collection: "0xa", TokenID: "9", Owner: creator, Name: "N", ImageURI: "/img/x", CollectionVerified: true, CollectionCreator: creator}
	mock.ExpectQuery(`SELECT t.collection, t.token_id::text, lower\(t.minter\)`).WillReturnError(context.DeadlineExceeded)
	q.fillTokenBadges(context.Background(), []badgeRow{&d})
	if !d.Verified || d.CreatorIsOwner {
		t.Fatalf("detail: %+v", d.TokenBadge)
	}
}

func TestSetTokenMinterIsFirstWins(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)
	mock.ExpectExec(`INSERT INTO nft_tokens\(collection, token_id, minter\)`).
		WithArgs("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "7", "0x00000000000000000000000000000000000000c1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := q.SetTokenMinter(context.Background(), "0xAAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "7", "0x00000000000000000000000000000000000000C1"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
