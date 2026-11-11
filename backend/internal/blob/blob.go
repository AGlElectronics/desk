// Package blob stores Desk's files: attachments and the raw copy of every
// imported message. Production uses S3 (any S3-compatible service); tests
// use Memory. Objects are private: Desk streams them to signed-in users
// after its own access checks and never hands out bucket URLs.
package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound means the object does not exist.
var ErrNotFound = errors.New("blob not found")

// Store is where Desk keeps files.
type Store interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Get returns the object and its size. The caller closes it.
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	// Delete removes an object; a missing object is not an error.
	Delete(ctx context.Context, key string) error
}

// S3Config is the S3_* configuration.
type S3Config struct {
	Endpoint        string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Region          string
	UsePathStyle    bool
	// CreateBucket creates a missing bucket (development only); otherwise a
	// missing bucket is a startup error.
	CreateBucket bool
}

// S3 is a Store on an S3 bucket.
type S3 struct {
	mc     *minio.Client
	bucket string
}

// NewS3 connects and checks that the bucket exists.
func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse S3_ENDPOINT: %w", err)
	}
	lookup := minio.BucketLookupAuto
	if cfg.UsePathStyle {
		lookup = minio.BucketLookupPath
	}
	mc, err := minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:       u.Scheme == "https",
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	exists, err := mc.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket %q: %w", cfg.Bucket, err)
	}
	if !exists {
		if !cfg.CreateBucket {
			return nil, fmt.Errorf("bucket %q does not exist; create it first (docs: modules/desk/setup)", cfg.Bucket)
		}
		if err := mc.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, fmt.Errorf("create bucket %q: %w", cfg.Bucket, err)
		}
	}
	return &S3{mc: mc, bucket: cfg.Bucket}, nil
}

// Put implements Store.
func (s *S3) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.mc.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	return err
}

// Get implements Store.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := s.mc.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	return obj, info.Size, nil
}

// Delete implements Store.
func (s *S3) Delete(ctx context.Context, key string) error {
	err := s.mc.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil && minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return nil
	}
	return err
}

// Memory is an in-memory Store for tests.
type Memory struct {
	mu      sync.Mutex
	objects map[string][]byte
	// FailPut makes Put fail for keys with this prefix (tests).
	FailPut string
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{objects: map[string][]byte{}} }

// Put implements Store.
func (m *Memory) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailPut != "" && strings.HasPrefix(key, m.FailPut) {
		return errors.New("memory store: put refused")
	}
	m.objects[key] = append([]byte(nil), data...)
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[key]
	if !ok {
		return nil, 0, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

// Keys lists the stored keys (tests).
func (m *Memory) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.objects))
	for k := range m.objects {
		out = append(out, k)
	}
	return out
}
