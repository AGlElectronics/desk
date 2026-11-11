package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func mustNew(t *testing.T, current string, previous ...string) *Keyring {
	t.Helper()
	k, err := New(current, previous...)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	k := mustNew(t, "current-key-0123456789abcdef0123456789")
	enc, err := k.Seal("hunter2", "mailbox:1:imap_password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "v1."+k.CurrentKeyID()+".") {
		t.Fatalf("unexpected format %q", enc)
	}
	plain, err := k.Open(enc, "mailbox:1:imap_password")
	if err != nil || plain != "hunter2" {
		t.Fatalf("got %q, %v", plain, err)
	}
	if k.NeedsReseal(enc) {
		t.Fatal("fresh value should not need resealing")
	}
}

func TestEmptyStaysEmpty(t *testing.T) {
	k := mustNew(t, "k")
	enc, _ := k.Seal("", "x")
	if enc != "" {
		t.Fatalf("got %q", enc)
	}
	if plain, err := k.Open("", "x"); plain != "" || err != nil {
		t.Fatalf("got %q, %v", plain, err)
	}
}

func TestAdditionalDataBindsThePlace(t *testing.T) {
	k := mustNew(t, "k")
	enc, _ := k.Seal("secret", "mailbox:1:imap_password")
	for _, other := range []string{"mailbox:2:imap_password", "mailbox:1:smtp_password", ""} {
		if _, err := k.Open(enc, other); !errors.Is(err, ErrUndecryptable) {
			t.Errorf("opened with aad %q: %v", other, err)
		}
	}
}

func TestWrongKey(t *testing.T) {
	enc, _ := mustNew(t, "old").Seal("secret", "a")
	if _, err := mustNew(t, "new").Open(enc, "a"); !errors.Is(err, ErrUndecryptable) {
		t.Fatalf("expected ErrUndecryptable, got %v", err)
	}
}

func TestRotation(t *testing.T) {
	enc, _ := mustNew(t, "old").Seal("secret", "a")
	rotated := mustNew(t, "new", "old")
	plain, err := rotated.Open(enc, "a")
	if err != nil || plain != "secret" {
		t.Fatalf("previous key should open: %q %v", plain, err)
	}
	if !rotated.NeedsReseal(enc) {
		t.Fatal("value under the previous key needs resealing")
	}
	again, _ := rotated.Seal(plain, "a")
	if rotated.NeedsReseal(again) {
		t.Fatal("resealed value should be current")
	}
	if _, err := mustNew(t, "new").Open(again, "a"); err != nil {
		t.Fatalf("after reseal the previous key is no longer needed: %v", err)
	}
}

func TestTamperedValue(t *testing.T) {
	k := mustNew(t, "k")
	enc, _ := k.Seal("secret", "a")
	b := []byte(enc)
	b[len(b)-3] ^= 1
	if _, err := k.Open(string(b), "a"); !errors.Is(err, ErrUndecryptable) {
		t.Fatalf("tampered value opened: %v", err)
	}
	if _, err := k.Open("v1.deadbeef.AAAA", "a"); !errors.Is(err, ErrUndecryptable) {
		t.Fatalf("unknown key id opened: %v", err)
	}
}

// Values written by the MVP: base64(nonce|ct), key = sha256(secret), no aad.
func TestLegacyFormat(t *testing.T) {
	sum := sha256.Sum256([]byte("legacy"))
	block, _ := aes.NewCipher(sum[:])
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	enc := base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte("pw"), nil))

	k := mustNew(t, "new", "legacy")
	plain, err := k.Open(enc, "mailbox:1:imap_password")
	if err != nil || plain != "pw" {
		t.Fatalf("legacy value: %q %v", plain, err)
	}
	if !k.NeedsReseal(enc) {
		t.Fatal("legacy value needs resealing")
	}
}
