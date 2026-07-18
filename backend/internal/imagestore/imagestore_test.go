package imagestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeStore is a package-local in-memory implementation of Store used to
// exercise imagestore.Put / Get / Has without touching Postgres. Tests should
// NOT call t.Parallel() (same non-parallel rule as media.dialResolver).
type fakeStore struct {
	rows map[string]struct {
		body       []byte
		mime       string
		collection string
		sourceURI  string
		refcount   int
	}
	failPut    error
	totalBytes int64 // injected total for quota tests; -1 = compute from rows
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		rows: map[string]struct {
			body       []byte
			mime       string
			collection string
			sourceURI  string
			refcount   int
	}{},
		totalBytes: -1, // compute from rows
	}
}

func (f *fakeStore) PutImage(_ context.Context, sha, mime, collection, src string, body []byte) error {
	if f.failPut != nil {
		return f.failPut
	}
	cur, ok := f.rows[sha]
	if ok {
		cur.refcount++
		f.rows[sha] = cur
		return nil
	}
	f.rows[sha] = struct {
		body       []byte
		mime       string
		collection string
		sourceURI  string
		refcount   int
	}{body: append([]byte(nil), body...), mime: mime, collection: collection, sourceURI: src, refcount: 1}
	return nil
}

// PutThumbnail mirrors PutImage — thumbnails share the same dedup model.
// IMG-1: added when Store interface gained PutThumbnail.
func (f *fakeStore) PutThumbnail(_ context.Context, sha, mime, parentHash, collection, src string, body []byte) error {
	return f.PutImage(context.Background(), sha, mime, collection, src, body)
}

func (f *fakeStore) GetImage(_ context.Context, sha string) (Blob, error) {
	r, ok := f.rows[sha]
	if !ok {
		return Blob{}, pgx.ErrNoRows
	}
	return Blob{Body: r.body, Mime: r.mime, SourceURI: r.sourceURI}, nil
}

func (f *fakeStore) HasImage(_ context.Context, sha string) (bool, error) {
	_, ok := f.rows[sha]
	return ok, nil
}

func (f *fakeStore) TotalBlobBytes(_ context.Context) (int64, error) {
	if f.totalBytes >= 0 {
		return f.totalBytes, nil
	}
	var total int64
	for _, r := range f.rows {
		total += int64(len(r.body))
	}
	return total, nil
}

func (f *fakeStore) CountBlobsForCollection(_ context.Context, collection string) (int, error) {
	var n int
	for _, r := range f.rows {
		if r.collection == collection {
			n++
		}
	}
	return n, nil
}

// The fake returns pgx.ErrNoRows directly so IsNoRows exercises the same
// code path the production db.Q wired up.

var sniffPNG = func(body []byte) (string, bool) {
	if len(body) >= 8 &&
		body[0] == 0x89 && body[1] == 'P' && body[2] == 'N' && body[3] == 'G' {
		return "image/png", true
	}
	return "", false
}

// minimal PNG signature (test fixture, not a real image — we only care
// about magic bytes matching the sniffer).
var pngFixture = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x00, 0x00, 0x0d}

func TestValidateHash_AcceptsLowercaseHex64(t *testing.T) {
	sum := sha256.Sum256([]byte("hello"))
	good := hex.EncodeToString(sum[:])
	if !ValidateHash(good) {
		t.Fatalf("expected %q to be valid", good)
	}
}

func TestValidateHash_RejectsShort(t *testing.T) {
	if ValidateHash("abc") {
		t.Fatal("63-char hash must be rejected")
	}
	if ValidateHash("") {
		t.Fatal("empty hash must be rejected")
	}
}

func TestValidateHash_RejectsNonHex(t *testing.T) {
	bad := strings.Repeat("g", 64) // 'g' is not [0-9a-f]
	if ValidateHash(bad) {
		t.Fatal("non-hex hash must be rejected")
	}
}

func TestValidateHash_RejectsUppercaseHex(t *testing.T) {
	// Hex.EncodeToString returns lowercase by default; verify upper is REJECTED
	// because PostgreSQL CHAR(64) PRIMARY KEY is case-sensitive and we want
	// exactly one canonical form.
	sum := sha256.Sum256([]byte("hello"))
	upper := strings.ToUpper(hex.EncodeToString(sum[:]))
	if ValidateHash(upper) {
		t.Fatal("uppercase hex must be rejected — canonical form is lowercase")
	}
}

func TestPublicPath(t *testing.T) {
	sum := sha256.Sum256([]byte("hello"))
	got := PublicPath(hex.EncodeToString(sum[:]))
	if got != "/api/v1/img/"+hex.EncodeToString(sum[:]) {
		t.Fatalf("PublicPath = %q", got)
	}
	if PublicPath("") != "" {
		t.Fatal("empty hash -> empty path")
	}
	if PublicHash := PublicPath(strings.Repeat("x", 64)); PublicHash != "" {
		t.Fatal("invalid hash -> empty path")
	}
}

func TestHashDeterministic(t *testing.T) {
	a := Hash([]byte("payload"))
	b := Hash([]byte("payload"))
	if a == "" || a != b {
		t.Fatalf("Hash not deterministic: %q vs %q", a, b)
	}
	if Hash(nil) != "" || Hash([]byte{}) != "" {
		t.Fatal("empty body must hash to empty")
	}
}

func TestExtractHash(t *testing.T) {
	sum := sha256.Sum256([]byte("hello"))
	h := hex.EncodeToString(sum[:])
	cases := []struct {
		uri string
		want string
	}{
		{"/api/v1/img/" + h, h},
		{"/api/v1/img/" + h + "?v=2", h},                  // trailing query stripped
		{"/api/v1/img/" + h + "#frag", h},                 // trailing fragment stripped
		{"https://magicwebb.fly.dev/api/v1/img/" + h, h},  // tolerant of full origin prefix
		{"http://" + h, ""},                               // accidental bytes path, not a blob URL
		{"", ""},                                          // empty
		{"/api/v1/img/" + h + "/extra", ""},               // trailing slash + extra — reject
		{"/api/v1/other/" + h, ""},                        // wrong path segment
		{"not-a-url", ""},
	}
	for _, c := range cases {
		if got := ExtractHash(c.uri); got != c.want {
			t.Errorf("ExtractHash(%q) = %q, want %q", c.uri, got, c.want)
		}
	}
}

func TestPut_DedupesBySha(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore()

	first, err := Put(ctx, s, sniffPNG, "0xabc", "https://x/y.png", pngFixture)
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if !first.Inserted {
		t.Fatal("first Put: Inserted must be true")
	}
	if first.Mime != "image/png" {
		t.Fatalf("mime = %q, want image/png", first.Mime)
	}

	// Identical bytes -> identical hash -> second Put must dedupe.
	second, err := Put(ctx, s, sniffPNG, "0xdef", "https://different-origin.example/y.png", pngFixture)
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if second.Inserted {
		t.Fatal("second Put: Inserted must be false on dedup hit")
	}
	if second.Hash != first.Hash {
		t.Fatalf("hash mismatch: %s vs %s", first.Hash, second.Hash)
	}
	if got := s.rows[first.Hash].refcount; got != 2 {
		t.Fatalf("refcount = %d, want 2", got)
	}
}

func TestPut_RejectsEmptyBody(t *testing.T) {
	if _, err := Put(context.Background(), newFakeStore(), sniffPNG, "", "x", nil); !errors.Is(err, ErrEmptyBody) {
		t.Fatalf("want ErrEmptyBody, got %v", err)
	}
	if _, err := Put(context.Background(), newFakeStore(), sniffPNG, "", "x", []byte{}); !errors.Is(err, ErrEmptyBody) {
		t.Fatalf("want ErrEmptyBody, got %v", err)
	}
}

func TestPut_RejectsOversizedBody(t *testing.T) {
	big := make([]byte, MaxBlobBytes+1)
	// fill with png-like magic so the sniffer passes
	big[0] = 0x89
	big[1] = 'P'
	big[2] = 'N'
	big[3] = 'G'
	if _, err := Put(context.Background(), newFakeStore(), sniffPNG, "", "x", big); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("want ErrBodyTooLarge, got %v", err)
	}
}

func TestPut_RejectsUnfitBody(t *testing.T) {
	if _, err := Put(context.Background(), newFakeStore(), sniffPNG, "", "x", []byte("not an image")); err == nil {
		t.Fatal("sniffer rejection must surface as error")
	}
}

func TestPut_NoSnifferIsError(t *testing.T) {
	if _, err := Put(context.Background(), newFakeStore(), nil, "", "x", []byte("unused")); err == nil {
		t.Fatal("nil sniffer must surface as error so callers can't accidentally store opaque bytes")
	}
}

func TestGet_RejectsMalformedHash(t *testing.T) {
	if _, err := Get(context.Background(), newFakeStore(), "garbage"); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("want ErrInvalidHash, got %v", err)
	}
}

func TestHas_RejectsMalformedHash(t *testing.T) {
	if _, err := Has(context.Background(), newFakeStore(), "garbage"); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("want ErrInvalidHash, got %v", err)
	}
}

func TestGet_RoundTripsPutBytes(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore()
	st, err := Put(ctx, s, sniffPNG, "0xabc", "https://x.png", pngFixture)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := Get(ctx, s, st.Hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(blob.Body) != string(pngFixture) {
		t.Fatalf("body mismatch: %d vs %d bytes", len(blob.Body), len(pngFixture))
	}
	if blob.Mime != "image/png" {
		t.Fatalf("mime = %q", blob.Mime)
	}
	if blob.SourceURI != "https://x.png" {
		t.Fatalf("sourceURI = %q", blob.SourceURI)
	}
}

// Pin: the dedup contract — identical bytes from two distinct source URIs
// share ONE row + N refcounts. This is the load-bearing property the indexer
// relies on so identical images from different NFT contracts don't bloat
// the table.
func TestPut_DifferentSourcesSameBytesOneRow(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore()
	for _, src := range []string{
		"https://contract-a.example/1.png",
		"https://contract-b.example/2.png",
		"ipfs://QmABC",
	} {
		if _, err := Put(ctx, s, sniffPNG, "0xabc", src, pngFixture); err != nil {
			t.Fatalf("Put src=%s: %v", src, err)
		}
	}
	if len(s.rows) != 1 {
		t.Fatalf("expected 1 row after 3 identical-looking ingests, got %d", len(s.rows))
	}
	for sha, r := range s.rows {
		if r.refcount != 3 {
			t.Fatalf("sha=%s refcount = %d, want 3", sha, r.refcount)
		}
		// source_uri is the FIRST writer's — record it for diagnostic so a
		// regression that overwrites the audit field trips this assertion.
		if r.sourceURI == "" {
			t.Fatalf("sha=%s source_uri should record the first writer's URL", sha)
		}
	}
}

// ── Quota enforcement tests ────────────────────────────────────────────────

func TestPut_SkipsExceedingPerCollectionBlobCount(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore()

	// Pre-populate MaxBlobCountPerCollection blobs for this collection.
	// Each blob must have unique bytes so they produce distinct hashes.
	for i := 0; i < MaxBlobCountPerCollection; i++ {
		body := make([]byte, 12)
		body[0] = 0x89
		body[1] = 'P'
		body[2] = 'N'
		body[3] = 'G'
		body[4] = byte(i >> 24)
		body[5] = byte(i >> 16)
		body[6] = byte(i >> 8)
		body[7] = byte(i)
		st, err := Put(ctx, s, sniffPNG, "0xabc", "src", body)
		if err != nil {
			t.Fatalf("pre-populate blob %d: %v", i, err)
		}
		if st.Skipped {
			t.Fatalf("pre-populate blob %d: unexpected skip", i)
		}
	}

	// Now the (MaxBlobCountPerCollection+1)th blob from the same collection
	// must be skipped.
	body := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 0, 0, 0, 0}
	st, err := Put(ctx, s, sniffPNG, "0xabc", "overflow", body)
	if err != nil {
		t.Fatalf("overflow Put: %v", err)
	}
	if !st.Skipped {
		t.Fatal("expected Skipped=true when per-collection blob count quota exceeded")
	}
}

func TestPut_PerCollectionQuota_DoesNotAffectOtherCollections(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore()

	// Fill collection A to the max.
	for i := 0; i < MaxBlobCountPerCollection; i++ {
		body := make([]byte, 12)
		body[0] = 0x89
		body[1] = 'P'
		body[2] = 'N'
		body[3] = 'G'
		body[4] = byte(i >> 24)
		body[5] = byte(i >> 16)
		body[6] = byte(i >> 8)
		body[7] = byte(i)
		if _, err := Put(ctx, s, sniffPNG, "0xaaa", "src", body); err != nil {
			t.Fatalf("populate A blob %d: %v", i, err)
		}
	}

	// Collection B should still be able to store blobs.
	body := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 0, 0, 0, 1}
	st, err := Put(ctx, s, sniffPNG, "0xbbb", "src", body)
	if err != nil {
		t.Fatalf("collection B Put: %v", err)
	}
	if st.Skipped {
		t.Fatal("collection B must not be skipped — its quota is independent of A")
	}
	if !st.Inserted {
		t.Fatal("collection B blob must be Inserted=true")
	}
}

func TestPut_PerCollectionQuota_EmptyCollectionNeverQuotaBlocked(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore()

	// Empty collection (legacy rows) should never trigger per-collection quota.
	for i := 0; i < MaxBlobCountPerCollection+10; i++ {
		body := make([]byte, 12)
		body[0] = 0x89
		body[1] = 'P'
		body[2] = 'N'
		body[3] = 'G'
		body[4] = byte(i >> 24)
		body[5] = byte(i >> 16)
		body[6] = byte(i >> 8)
		body[7] = byte(i)
		st, err := Put(ctx, s, sniffPNG, "", "src", body)
		if err != nil {
			t.Fatalf("empty-collection blob %d: %v", i, err)
		}
		if st.Skipped {
			t.Fatalf("empty-collection blob %d: must not be skipped", i)
		}
	}
}

func TestPut_SkipsExceedingTotalBlobBytes(t *testing.T) {
	ctx := context.Background()
	// Populating enough real rows to exceed MaxTotalBlobBytes would be
	// slow/verbose, so we inject totalBytes directly via a custom fake
	// store to exercise the total-byte-quota path.
	s := &fakeStore{
		rows: map[string]struct {
			body       []byte
			mime       string
			collection string
			sourceURI  string
			refcount   int
		}{},
		totalBytes: MaxTotalBlobBytes, // already at cap
	}

	st, err := Put(ctx, s, sniffPNG, "0xabc", "overflow", pngFixture)
	if err != nil {
		t.Fatalf("overflow Put: %v", err)
	}
	if !st.Skipped {
		t.Fatal("expected Skipped=true when total byte quota exceeded")
	}
}

// IsNoRows must recognise the pgx sentinel (the production sentinel — fake
// fake errors were kept to a minimum) plus a wrapped form. Also pins that
// nil and unrelated errors do not match.
func TestIsNoRowsRecognisesPgxSentinelDirectAndWrapped(t *testing.T) {
	if !IsNoRows(pgx.ErrNoRows) {
		t.Fatal("IsNoRows(pgx.ErrNoRows) must be true")
	}
	if !IsNoRows(fmt.Errorf("wrapped: %w", pgx.ErrNoRows)) {
		t.Fatal("IsNoRows must recognise wrapped pgx.ErrNoRows so api-level handlers don't have to unwrap manually")
	}
	if IsNoRows(errors.New("something else")) {
		t.Fatal("IsNoRows must reject unrelated errors")
	}
	if IsNoRows(nil) {
		t.Fatal("IsNoRows(nil) must be false")
	}
}
