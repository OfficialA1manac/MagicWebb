// Package imagestore — IMG-3: S3-compatible blob store backend.
//
// When IMG_STORE_BACKEND=s3, blob bodies are stored in S3/MinIO instead of
// Postgres BYTEA. Metadata (sha256, mime, byte_length, source_uri, collection,
// refcount, parent_hash, thumb_width) remains in nft_image_blobs for dedup,
// quota enforcement, and hash-based lookups.
//
// Benefits:
//   - Frees Postgres storage (BYTEA rows can be large)
//   - Frees Postgres connections (no large body transfers on every image GET)
//   - Works with any S3-compatible service (AWS S3, MinIO, Cloudflare R2,
//     Backblaze B2, DigitalOcean Spaces)
//
// S3 key format: blobs/<sha256hex> (e.g. blobs/abc123...def)
// The sha256 hex is the content hash of the body bytes, guaranteeing
// content-addressable dedup at the storage layer.
package imagestore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog/log"
)

// dbExecutor captures the subset of *pgxpool.Pool methods used by S3Store.
// Both *pgxpool.Pool and pgxmock.PgxPoolIface satisfy this interface,
// enabling unit tests without a live Postgres connection.
type dbExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// s3Client abstracts the minio S3 operations used by S3Store.
// *minio.Client satisfies this interface (via realS3Client adapter),
// and tests provide a mock implementation backed by an in-memory map.
type s3Client interface {
	PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error)
	GetObject(ctx context.Context, bucketName, objectName string, opts minio.GetObjectOptions) (io.ReadCloser, error)
}

// realS3Client adapts *minio.Client to the s3Client interface. Go requires
// exact method signature matches for interface satisfaction, so we wrap the
// concrete minio client and upcast *minio.Object to io.ReadCloser.
type realS3Client struct{ c *minio.Client }

func (r *realS3Client) PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	return r.c.PutObject(ctx, bucketName, objectName, reader, objectSize, opts)
}

func (r *realS3Client) GetObject(ctx context.Context, bucketName, objectName string, opts minio.GetObjectOptions) (io.ReadCloser, error) {
	return r.c.GetObject(ctx, bucketName, objectName, opts)
}

// S3Store implements Store using S3 for blob bodies and Postgres for metadata.
// Body bytes are stored under S3 key "blobs/<sha256hex>"; metadata (mime,
// source_uri, byte_length, etc.) remains in nft_image_blobs for SQL queries.
//
// The body column in nft_image_blobs is stored as an empty bytea (''::bytea)
// when S3 is the backend — the S3 key IS the sha256, so the body column is
// redundant and only retained for schema compatibility. Migrating BACK to
// Postgres-only requires a re-upload of all blobs.
type S3Store struct {
	pool       dbExecutor
	client     s3Client
	bucketName string
}

// NewS3Store creates an S3-backed blob store. The endpoint should be the
// S3-compatible service URL (e.g. "s3.amazonaws.com", "play.min.io:9000").
// For AWS S3, use "s3.<region>.amazonaws.com". For MinIO, include the port.
//
// useSSL controls HTTPS vs HTTP. Always use true for production; false is
// only for local MinIO development.
func NewS3Store(ctx context.Context, pool *pgxpool.Pool, endpoint, bucket, accessKey, secretKey string, useSSL bool) (*S3Store, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("s3store: create minio client: %w", err)
	}

	// Verify the bucket exists and is accessible.
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("s3store: bucket check failed for %q: %w", bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("s3store: bucket %q does not exist", bucket)
	}

	log.Info().Str("endpoint", endpoint).Str("bucket", bucket).Bool("ssl", useSSL).
		Msg("s3store: connected, bucket verified")

	return &S3Store{
		pool:       pool,
		client:     &realS3Client{c: client},
		bucketName: bucket,
	}, nil
}

// s3Key returns the S3 object key for a given sha256 hex hash.
func s3Key(sha256hex string) string {
	return "blobs/" + sha256hex
}

// ── Store interface implementation ───────────────────────────────────────

// PutImage uploads the body to S3 and inserts metadata into nft_image_blobs.
// The body column is stored as empty bytea since the bytes live in S3.
func (s *S3Store) PutImage(ctx context.Context, sha256hex, mime, collection, sourceURI string, body []byte) error {
	// Upload to S3 first — if this fails, nothing is written to Postgres.
	key := s3Key(sha256hex)
	_, err := s.client.PutObject(ctx, s.bucketName, key,
		bytes.NewReader(body), int64(len(body)),
		minio.PutObjectOptions{ContentType: mime})
	if err != nil {
		return fmt.Errorf("s3store: put object %s: %w", key, err)
	}

	// Insert metadata row with empty body (bytes live in S3).
	_, err = s.pool.Exec(ctx,
		`INSERT INTO nft_image_blobs(sha256, mime, byte_length, source_uri, body, collection)
		 VALUES($1,$2,$3,$4,''::bytea,$5)
		 ON CONFLICT(sha256) DO UPDATE
		   SET refcount     = nft_image_blobs.refcount + 1,
		       last_seen_at = now()`,
		sha256hex, mime, len(body), sourceURI, collection)
	if err != nil {
		return fmt.Errorf("s3store: insert metadata: %w", err)
	}
	return nil
}

// PutThumbnail uploads the thumbnail body to S3 and inserts metadata.
func (s *S3Store) PutThumbnail(ctx context.Context, sha256hex, mime, parentHash, collection, sourceURI string, body []byte, width int) error {
	key := s3Key(sha256hex)
	_, err := s.client.PutObject(ctx, s.bucketName, key,
		bytes.NewReader(body), int64(len(body)),
		minio.PutObjectOptions{ContentType: mime})
	if err != nil {
		return fmt.Errorf("s3store: put thumbnail %s: %w", key, err)
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO nft_image_blobs(sha256, mime, byte_length, source_uri, body, collection, parent_hash, thumb_width)
		 VALUES($1,$2,$3,$4,''::bytea,$5,$6,$7)
		 ON CONFLICT(sha256) DO UPDATE
		   SET refcount     = nft_image_blobs.refcount + 1,
		       last_seen_at = now()`,
		sha256hex, mime, len(body), sourceURI, collection, parentHash, width)
	if err != nil {
		return fmt.Errorf("s3store: insert thumbnail metadata: %w", err)
	}
	return nil
}

// GetImageByParent looks up a thumbnail's metadata from Postgres and fetches
// the body from S3. Returns pgx.ErrNoRows when no thumbnail exists.
func (s *S3Store) GetImageByParent(ctx context.Context, parentHash string, width int, preferWebP bool) (Blob, error) {
	var sha256hex, mime, sourceURI string
	orderClause := "ORDER BY CASE WHEN mime = 'image/jpeg' THEN 0 ELSE 1 END"
	if preferWebP {
		orderClause = "ORDER BY CASE WHEN mime = 'image/webp' THEN 0 ELSE 1 END"
	}
	err := s.pool.QueryRow(ctx,
		`SELECT sha256, mime, source_uri FROM nft_image_blobs
		  WHERE parent_hash = $1 AND thumb_width = $2
		  `+orderClause+`
		  LIMIT 1`, parentHash, width).
		Scan(&sha256hex, &mime, &sourceURI)
	if err != nil {
		return Blob{}, err
	}

	body, err := s.download(ctx, sha256hex)
	if err != nil {
		return Blob{}, fmt.Errorf("s3store: download thumbnail %s: %w", sha256hex, err)
	}

	return Blob{Body: body, Mime: mime, SourceURI: sourceURI}, nil
}

// GetImage fetches blob body from S3 and metadata from Postgres.
// Returns pgx.ErrNoRows when the hash is unknown.
func (s *S3Store) GetImage(ctx context.Context, sha256hex string) (Blob, error) {
	var mime, sourceURI string
	err := s.pool.QueryRow(ctx,
		`SELECT mime, source_uri FROM nft_image_blobs WHERE sha256=$1`, sha256hex).
		Scan(&mime, &sourceURI)
	if err != nil {
		return Blob{}, err
	}

	body, err := s.download(ctx, sha256hex)
	if err != nil {
		return Blob{}, fmt.Errorf("s3store: download %s: %w", sha256hex, err)
	}

	return Blob{Body: body, Mime: mime, SourceURI: sourceURI}, nil
}

// HasImage is a cheap existence check — uses Postgres metadata only.
func (s *S3Store) HasImage(ctx context.Context, sha256hex string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT 1 FROM nft_image_blobs WHERE sha256=$1`, sha256hex).Scan(&n)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return n == 1, err
}

// TotalBlobBytes returns the cumulative byte_length from Postgres metadata.
func (s *S3Store) TotalBlobBytes(ctx context.Context) (int64, error) {
	var total int64
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(sum(byte_length), 0) FROM nft_image_blobs`).Scan(&total)
	return total, err
}

// CountBlobsForCollection returns distinct blob count from Postgres metadata.
func (s *S3Store) CountBlobsForCollection(ctx context.Context, collection string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM nft_image_blobs WHERE collection=$1`, collection).Scan(&n)
	return n, err
}

// ── S3 helpers ──────────────────────────────────────────────────────────

// download fetches a blob's bytes from S3 by sha256 key.
// Uses a context-aware reader so the 30s timeout is actually enforced —
// io.Copy on its own ignores context cancellation.
func (s *S3Store) download(ctx context.Context, sha256hex string) ([]byte, error) {
	key := s3Key(sha256hex)
	obj, err := s.client.GetObject(ctx, s.bucketName, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()

	// Wrap the reader so Read() checks ctx.Done() before each chunk.
	// Without this, a stalled S3 connection would hang past the timeout
	// since io.Copy/io.ReadAll ignores context cancellation on the
	// underlying reader.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return io.ReadAll(&contextReader{ctx: ctx, r: obj})
}

// contextReader wraps an io.Reader and checks ctx.Err() before every Read.
// This makes io.ReadAll/io.Copy respect context cancellation — the standard
// library's io.Copy does NOT check context state on the source reader.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (cr *contextReader) Read(p []byte) (int, error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, err
	}
	return cr.r.Read(p)
}

// Compile-time assertion: S3Store implements Store.
var _ Store = (*S3Store)(nil)
