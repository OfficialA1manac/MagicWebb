package imagestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
	"github.com/pashagolub/pgxmock/v4"
)

// ── Mock S3 client ──────────────────────────────────────────────────────

// mockS3Client is an in-memory s3Client implementation for unit tests.
// Objects are stored in a map keyed by "bucket/objectName".
type mockS3Client struct {
	objects map[string][]byte
	mu      sync.RWMutex

	// Error injection
	putErr error // if non-nil, PutObject returns this error
	getErr error // if non-nil, GetObject returns this error
}

func newMockS3Client() *mockS3Client {
	return &mockS3Client{
		objects: make(map[string][]byte),
	}
}

func (m *mockS3Client) key(bucket, objectName string) string {
	return bucket + "/" + objectName
}

func (m *mockS3Client) PutObject(_ context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
	if m.putErr != nil {
		return minio.UploadInfo{}, m.putErr
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return minio.UploadInfo{}, err
	}
	m.mu.Lock()
	m.objects[m.key(bucketName, objectName)] = body
	m.mu.Unlock()
	return minio.UploadInfo{Size: objectSize}, nil
}

func (m *mockS3Client) GetObject(_ context.Context, bucketName, objectName string, _ minio.GetObjectOptions) (io.ReadCloser, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	m.mu.RLock()
	body, ok := m.objects[m.key(bucketName, objectName)]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("The specified key does not exist.")
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

// ── Test helpers ────────────────────────────────────────────────────────

// newTestS3Store constructs an S3Store with a mock S3 client and pgxmock pool.
func newTestS3Store(t *testing.T, mock pgxmock.PgxPoolIface) (*S3Store, *mockS3Client) {
	t.Helper()
	s3 := newMockS3Client()
	return &S3Store{
		pool:       mock,
		client:     s3,
		bucketName: "test-bucket",
	}, s3
}

// ── PutImage tests ──────────────────────────────────────────────────────

func TestS3Store_PutImage_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)

	mock.ExpectExec(`INSERT INTO nft_image_blobs`).
		WithArgs("abc123", "image/png", 4, "https://example.com/img.png", "0xcoll").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.PutImage(context.Background(), "abc123", "image/png", "0xcoll", "https://example.com/img.png", []byte("test"))
	if err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestS3Store_PutImage_S3Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, s3 := newTestS3Store(t, mock)
	s3.putErr = errors.New("s3: network error")

	// No Postgres call should happen — S3 fails first.
	err = store.PutImage(context.Background(), "abc123", "image/png", "0xcoll", "https://example.com/img.png", []byte("test"))
	if err == nil {
		t.Fatal("expected error from S3 put failure, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestS3Store_PutImage_DBError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)

	mock.ExpectExec(`INSERT INTO nft_image_blobs`).
		WithArgs("abc123", "image/png", 4, "https://example.com/img.png", "0xcoll").
		WillReturnError(context.DeadlineExceeded)

	err = store.PutImage(context.Background(), "abc123", "image/png", "0xcoll", "https://example.com/img.png", []byte("test"))
	if err == nil {
		t.Fatal("expected error from DB insert failure, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── PutThumbnail tests ──────────────────────────────────────────────────

func TestS3Store_PutThumbnail_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)

	mock.ExpectExec(`INSERT INTO nft_image_blobs`).
		WithArgs("thumb123", "image/jpeg", 5, "https://example.com/img.png", "0xcoll", "parent123", 256).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.PutThumbnail(context.Background(), "thumb123", "image/jpeg", "parent123", "0xcoll", "https://example.com/img.png", []byte("thumb"), 256)
	if err != nil {
		t.Fatalf("PutThumbnail: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestS3Store_PutThumbnail_S3Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, s3 := newTestS3Store(t, mock)
	s3.putErr = errors.New("s3: upload failed")

	err = store.PutThumbnail(context.Background(), "thumb123", "image/jpeg", "parent123", "0xcoll", "https://example.com/img.png", []byte("thumb"), 256)
	if err == nil {
		t.Fatal("expected error from S3 put thumbnail failure, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── GetImage tests ──────────────────────────────────────────────────────

func TestS3Store_GetImage_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, s3 := newTestS3Store(t, mock)
	body := []byte("image bytes")

	// Pre-populate the mock S3 with the blob.
	s3.mu.Lock()
	s3.objects[s3.key("test-bucket", "blobs/abc123")] = body
	s3.mu.Unlock()

	// Expect metadata lookup.
	rows := mock.NewRows([]string{"mime", "source_uri"}).
		AddRow("image/png", "https://example.com/img.png")
	mock.ExpectQuery(`SELECT mime, source_uri FROM nft_image_blobs WHERE sha256=\$1`).
		WithArgs("abc123").
		WillReturnRows(rows)

	blob, err := store.GetImage(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if string(blob.Body) != string(body) {
		t.Fatalf("body = %q, want %q", blob.Body, body)
	}
	if blob.Mime != "image/png" {
		t.Fatalf("mime = %q, want image/png", blob.Mime)
	}
	if blob.SourceURI != "https://example.com/img.png" {
		t.Fatalf("sourceURI = %q", blob.SourceURI)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestS3Store_GetImage_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)

	mock.ExpectQuery(`SELECT mime, source_uri FROM nft_image_blobs WHERE sha256=\$1`).
		WithArgs("nonexistent").
		WillReturnError(pgx.ErrNoRows)

	_, err = store.GetImage(context.Background(), "nonexistent")
	if err != pgx.ErrNoRows {
		t.Fatalf("want pgx.ErrNoRows, got: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestS3Store_GetImage_DownloadError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, s3 := newTestS3Store(t, mock)
	s3.getErr = errors.New("s3: download failed")

	// Metadata lookup succeeds, but S3 download fails.
	rows := mock.NewRows([]string{"mime", "source_uri"}).
		AddRow("image/png", "https://example.com/img.png")
	mock.ExpectQuery(`SELECT mime, source_uri FROM nft_image_blobs WHERE sha256=\$1`).
		WithArgs("abc123").
		WillReturnRows(rows)

	_, err = store.GetImage(context.Background(), "abc123")
	if err == nil {
		t.Fatal("expected error from S3 download failure, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestS3Store_GetImage_S3KeyNotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)

	// PG metadata exists, but S3 has no object at that key.
	rows := mock.NewRows([]string{"mime", "source_uri"}).
		AddRow("image/png", "https://example.com/img.png")
	mock.ExpectQuery(`SELECT mime, source_uri FROM nft_image_blobs WHERE sha256=\$1`).
		WithArgs("orphaned").
		WillReturnRows(rows)

	_, err = store.GetImage(context.Background(), "orphaned")
	if err == nil {
		t.Fatal("expected error when S3 key is missing, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── GetImageByParent tests ──────────────────────────────────────────────

func TestS3Store_GetImageByParent_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, s3 := newTestS3Store(t, mock)
	body := []byte("thumb bytes")

	s3.mu.Lock()
	s3.objects[s3.key("test-bucket", "blobs/thumb123")] = body
	s3.mu.Unlock()

	rows := mock.NewRows([]string{"sha256", "mime", "source_uri"}).
		AddRow("thumb123", "image/jpeg", "https://example.com/img.png")
	mock.ExpectQuery(`SELECT sha256, mime, source_uri FROM nft_image_blobs`).
		WithArgs("parent123", 256).
		WillReturnRows(rows)

	blob, err := store.GetImageByParent(context.Background(), "parent123", 256, false)
	if err != nil {
		t.Fatalf("GetImageByParent: %v", err)
	}
	if string(blob.Body) != string(body) {
		t.Fatalf("body = %q, want %q", blob.Body, body)
	}
	if blob.Mime != "image/jpeg" {
		t.Fatalf("mime = %q, want image/jpeg", blob.Mime)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestS3Store_GetImageByParent_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)

	mock.ExpectQuery(`SELECT sha256, mime, source_uri FROM nft_image_blobs`).
		WithArgs("parent123", 128).
		WillReturnError(pgx.ErrNoRows)

	_, err = store.GetImageByParent(context.Background(), "parent123", 128, false)
	if err != pgx.ErrNoRows {
		t.Fatalf("want pgx.ErrNoRows, got: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestS3Store_GetImageByParent_PrefersWebP(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, s3 := newTestS3Store(t, mock)
	body := []byte("webp thumb")

	s3.mu.Lock()
	s3.objects[s3.key("test-bucket", "blobs/thumb456")] = body
	s3.mu.Unlock()

	// When preferWebP=true, the ORDER BY should prefer image/webp.
	rows := mock.NewRows([]string{"sha256", "mime", "source_uri"}).
		AddRow("thumb456", "image/webp", "https://example.com/img.png")
	mock.ExpectQuery(`SELECT sha256, mime, source_uri FROM nft_image_blobs`).
		WithArgs("parent123", 256).
		WillReturnRows(rows)

	blob, err := store.GetImageByParent(context.Background(), "parent123", 256, true)
	if err != nil {
		t.Fatalf("GetImageByParent(preferWebP): %v", err)
	}
	if blob.Mime != "image/webp" {
		t.Fatalf("mime = %q, want image/webp", blob.Mime)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── HasImage tests ──────────────────────────────────────────────────────

func TestS3Store_HasImage_True(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)

	rows := mock.NewRows([]string{"1"}).AddRow(1)
	mock.ExpectQuery(`SELECT 1 FROM nft_image_blobs WHERE sha256=\$1`).
		WithArgs("abc123").
		WillReturnRows(rows)

	ok, err := store.HasImage(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("HasImage: %v", err)
	}
	if !ok {
		t.Fatal("expected HasImage=true")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestS3Store_HasImage_False(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)

	mock.ExpectQuery(`SELECT 1 FROM nft_image_blobs WHERE sha256=\$1`).
		WithArgs("nonexistent").
		WillReturnError(pgx.ErrNoRows)

	ok, err := store.HasImage(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("HasImage: %v", err)
	}
	if ok {
		t.Fatal("expected HasImage=false")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── TotalBlobBytes test ─────────────────────────────────────────────────

func TestS3Store_TotalBlobBytes(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)

	rows := mock.NewRows([]string{"sum"}).AddRow(int64(42_000))
	mock.ExpectQuery(`SELECT COALESCE\(sum\(byte_length\), 0\) FROM nft_image_blobs`).
		WillReturnRows(rows)

	total, err := store.TotalBlobBytes(context.Background())
	if err != nil {
		t.Fatalf("TotalBlobBytes: %v", err)
	}
	if total != 42_000 {
		t.Fatalf("TotalBlobBytes = %d, want 42000", total)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── CountBlobsForCollection test ────────────────────────────────────────

func TestS3Store_CountBlobsForCollection(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)

	rows := mock.NewRows([]string{"count"}).AddRow(7)
	mock.ExpectQuery(`SELECT count\(\*\) FROM nft_image_blobs WHERE collection=\$1`).
		WithArgs("0xcoll").
		WillReturnRows(rows)

	n, err := store.CountBlobsForCollection(context.Background(), "0xcoll")
	if err != nil {
		t.Fatalf("CountBlobsForCollection: %v", err)
	}
	if n != 7 {
		t.Fatalf("CountBlobsForCollection = %d, want 7", n)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── Round-trip tests ────────────────────────────────────────────────────

func TestS3Store_PutThenGet_RoundTrip(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)
	original := []byte("round-trip payload")

	// Step 1: PutImage — S3 stores it, PG inserts metadata.
	mock.ExpectExec(`INSERT INTO nft_image_blobs`).
		WithArgs("hash123", "image/png", len(original), "https://example.com/img.png", "0xcoll").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.PutImage(context.Background(), "hash123", "image/png", "0xcoll", "https://example.com/img.png", original)
	if err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	// Step 2: GetImage — blob is in the mock S3, metadata in PG.
	rows := mock.NewRows([]string{"mime", "source_uri"}).
		AddRow("image/png", "https://example.com/img.png")
	mock.ExpectQuery(`SELECT mime, source_uri FROM nft_image_blobs WHERE sha256=\$1`).
		WithArgs("hash123").
		WillReturnRows(rows)

	blob, err := store.GetImage(context.Background(), "hash123")
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if string(blob.Body) != string(original) {
		t.Fatalf("round-trip body mismatch: got %q, want %q", blob.Body, original)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── Interface assertion test ────────────────────────────────────────────

func TestS3Store_ImplementsStore(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)
	var s Store = store
	if s == nil {
		t.Fatal("S3Store should not be nil Store")
	}
}

// ── s3Key helper test ───────────────────────────────────────────────────

func TestS3Store_s3Key(t *testing.T) {
	got := s3Key("abc123def456")
	want := "blobs/abc123def456"
	if got != want {
		t.Fatalf("s3Key = %q, want %q", got, want)
	}
}

// ── dbExecutor interface satisfaction ──────────────────────────────────

func TestS3Store_dbExecutorSatisfied(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	store, _ := newTestS3Store(t, mock)

	if store.pool == nil {
		t.Fatal("dbExecutor pool must not be nil")
	}

	rows := mock.NewRows([]string{"1"}).AddRow(1)
	mock.ExpectQuery(`SELECT 1`).WillReturnRows(rows)

	var n int
	err = store.pool.QueryRow(context.Background(), `SELECT 1`).Scan(&n)
	if err != nil {
		t.Fatalf("QueryRow via dbExecutor: %v", err)
	}
	if n != 1 {
		t.Fatalf("n = %d, want 1", n)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── mockS3Client unit tests ─────────────────────────────────────────────

func TestMockS3Client_PutThenGet(t *testing.T) {
	m := newMockS3Client()
	body := []byte("test data")

	_, err := m.PutObject(context.Background(), "bucket", "key", bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	rc, err := m.GetObject(context.Background(), "bucket", "key", minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer rc.Close()

	got, _ := io.ReadAll(rc)
	if string(got) != string(body) {
		t.Fatalf("round-trip mismatch: %q vs %q", got, body)
	}
}

func TestMockS3Client_GetNotFound(t *testing.T) {
	m := newMockS3Client()

	_, err := m.GetObject(context.Background(), "bucket", "nonexistent", minio.GetObjectOptions{})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestMockS3Client_PutError(t *testing.T) {
	m := newMockS3Client()
	m.putErr = errors.New("injected put error")

	_, err := m.PutObject(context.Background(), "bucket", "key", bytes.NewReader([]byte("x")), 1, minio.PutObjectOptions{})
	if err == nil {
		t.Fatal("expected error from PutObject")
	}
}

func TestMockS3Client_GetError(t *testing.T) {
	m := newMockS3Client()
	m.getErr = errors.New("injected get error")

	_, err := m.GetObject(context.Background(), "bucket", "key", minio.GetObjectOptions{})
	if err == nil {
		t.Fatal("expected error from GetObject")
	}
}
