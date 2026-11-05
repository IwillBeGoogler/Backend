package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

// BlobSource reads the old server's content-addressed blobs.
type BlobSource interface {
	// Get returns errBlobMissing when the old storage has no such blob.
	Get(ctx context.Context, hash string) ([]byte, error)
}

// BlobSink is the new object store (internal/infrastructure/storage.MinIO).
type BlobSink interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (application.Object, error)
}

// blobKey is the key the Go server keeps an asset under.
func blobKey(hash string) string { return "diagram/" + hash }

// localBlobs is the old LocalDiskStore: <dir>/ab/cd/<sha256>.
type localBlobs struct{ dir string }

func (l localBlobs) Get(_ context.Context, hash string) ([]byte, error) {
	if !diagram.ValidHash(hash) {
		return nil, errBlobMissing
	}
	b, err := os.ReadFile(filepath.Join(l.dir, hash[:2], hash[2:4], hash))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errBlobMissing
	}
	return b, err
}

// s3Blobs is the old S3Store: objects under assets/<sha256>.
type s3Blobs struct {
	c      *minio.Client
	bucket string
}

type s3Config struct {
	Endpoint, Bucket, AccessKey, SecretKey, Region string
	SSL                                            bool
}

func newS3Blobs(c s3Config) (s3Blobs, error) {
	cl, err := minio.New(c.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure: c.SSL, Region: c.Region,
	})
	return s3Blobs{cl, c.Bucket}, err
}

func (s s3Blobs) Get(ctx context.Context, hash string) ([]byte, error) {
	if !diagram.ValidHash(hash) {
		return nil, errBlobMissing
	}
	o, err := s.c.GetObject(ctx, s.bucket, "assets/"+hash, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer o.Close()
	b, err := io.ReadAll(o)
	var resp minio.ErrorResponse
	if errors.As(err, &resp) && resp.Code == minio.NoSuchKey {
		return nil, errBlobMissing
	}
	return b, err
}
