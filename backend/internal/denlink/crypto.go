// Code generated from con-services shared/go/denlink; DO NOT EDIT. Run scripts/sync-shared.

package denlink

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// encPrefix marks an encrypted credential in den_link.credential:
// enc:v1:<key id>:<base64url(nonce || ciphertext)>. Anything else is a
// plaintext credential from before encryption, re-encrypted on first read.
const encPrefix = "enc:v1:"

// credentialAAD binds the ciphertext to its purpose, so a value encrypted
// for something else with the same key is not accepted.
var credentialAAD = []byte("con-services/den_link/credential")

type aeadKey struct {
	id   string
	aead cipher.AEAD
}

// keyring holds DEN_LINK_ENCRYPTION_KEY and, during a rotation,
// DEN_LINK_ENCRYPTION_KEY_PREVIOUS. New values are always sealed with the
// current key.
type keyring struct {
	current, previous *aeadKey
}

// ParseEncryptionKey decodes a base64 (standard or URL alphabet, with or
// without padding) AES-256 key and checks it is 32 bytes.
func ParseEncryptionKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			if len(b) != 32 {
				return nil, fmt.Errorf("must be 32 bytes after base64 decoding, got %d (generate one with: openssl rand -base64 32)", len(b))
			}
			return b, nil
		}
	}
	return nil, errors.New("is not valid base64 (generate one with: openssl rand -base64 32)")
}

func newAEADKey(name, s string) (*aeadKey, error) {
	raw, err := ParseEncryptionKey(s)
	if err != nil {
		return nil, fmt.Errorf("%s %w", name, err)
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return &aeadKey{id: hex.EncodeToString(sum[:4]), aead: aead}, nil
}

// newKeyring returns nil (no encryption) when current is empty.
func newKeyring(current, previous string) (*keyring, error) {
	if strings.TrimSpace(current) == "" {
		if strings.TrimSpace(previous) != "" {
			return nil, fmt.Errorf("%s is set but %s is not", EnvEncryptionKeyPrevious, EnvEncryptionKey)
		}
		return nil, nil
	}
	k := &keyring{}
	var err error
	if k.current, err = newAEADKey(EnvEncryptionKey, current); err != nil {
		return nil, err
	}
	if strings.TrimSpace(previous) != "" {
		if k.previous, err = newAEADKey(EnvEncryptionKeyPrevious, previous); err != nil {
			return nil, err
		}
	}
	return k, nil
}

func (k *keyring) seal(plaintext string) (string, error) {
	nonce := make([]byte, k.current.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := k.current.aead.Seal(nonce, nonce, []byte(plaintext), credentialAAD)
	return encPrefix + k.current.id + ":" + base64.RawURLEncoding.EncodeToString(out), nil
}

// open returns the plaintext of a stored value and whether it should be
// written back (plaintext, or sealed with the previous key). A nil keyring
// accepts only plaintext.
func (k *keyring) open(stored string) (plaintext string, rewrite bool, err error) {
	if !strings.HasPrefix(stored, encPrefix) {
		return stored, k != nil, nil
	}
	if k == nil {
		return "", false, fmt.Errorf("the stored Den credential is encrypted but %s is not set", EnvEncryptionKey)
	}
	id, data, ok := strings.Cut(strings.TrimPrefix(stored, encPrefix), ":")
	if !ok {
		return "", false, errors.New("the stored Den credential is malformed; pair again")
	}
	raw, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		return "", false, errors.New("the stored Den credential is malformed; pair again")
	}
	for _, key := range []*aeadKey{k.current, k.previous} {
		if key == nil || key.id != id {
			continue
		}
		n := key.aead.NonceSize()
		if len(raw) < n {
			break
		}
		plain, err := key.aead.Open(nil, raw[:n], raw[n:], credentialAAD)
		if err != nil {
			break
		}
		return string(plain), key != k.current, nil
	}
	return "", false, fmt.Errorf("the stored Den credential cannot be decrypted (was %s changed without keeping the old value in %s?); pair again", EnvEncryptionKey, EnvEncryptionKeyPrevious)
}
