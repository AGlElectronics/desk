package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func exercise(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	if err := s.Put(ctx, "test/a.txt", []byte("hello"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	r, size, err := s.Get(ctx, "test/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	_ = r.Close()
	if string(b) != "hello" || size != 5 {
		t.Fatalf("got %q (%d)", b, size)
	}
	if err := s.Delete(ctx, "test/a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "test/a.txt"); err != nil {
		t.Fatalf("deleting a missing object: %v", err)
	}
	if _, _, err := s.Get(ctx, "test/a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemory(t *testing.T) { exercise(t, NewMemory()) }

// DESK_TEST_S3_ENDPOINT=http://localhost:9000 runs this against the dev
// stack's SeaweedFS (DEV key conservices / conservicesdev).
func TestS3(t *testing.T) {
	endpoint := os.Getenv("DESK_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("DESK_TEST_S3_ENDPOINT is not set")
	}
	s, err := NewS3(context.Background(), S3Config{
		Endpoint: endpoint, Bucket: "desk-test", Region: "us-east-1", UsePathStyle: true, CreateBucket: true,
		AccessKeyID: envOr("DESK_TEST_S3_ACCESS_KEY_ID", "conservices"), SecretAccessKey: envOr("DESK_TEST_S3_SECRET_ACCESS_KEY", "conservicesdev"),
	})
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, s)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
