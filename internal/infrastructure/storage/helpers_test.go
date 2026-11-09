package storage

import (
	"os"

	"github.com/minio/minio-go/v7"
)

func minioRemoveOpts() minio.RemoveObjectOptions { return minio.RemoveObjectOptions{} }

// minioImage defaults to a locally available image; override with MINIO_TEST_IMAGE.
func minioImage() string {
	if v := os.Getenv("MINIO_TEST_IMAGE"); v != "" {
		return v
	}
	return "cgr.dev/chainguard/minio:latest"
}
