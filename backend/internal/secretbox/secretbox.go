// Package secretbox encrypts small secrets (mailbox passwords) at rest with
// AES-256-GCM under DESK_SECRET_KEY.
//
// A sealed value is "v1.<key id>.<base64(nonce|ciphertext)>". The key id
// names the key that sealed it, so after a rotation the previous keys
// (DESK_SECRET_KEY_PREVIOUS) still open old values until `server reencrypt`
// has sealed them again under the current key. Every value is bound to its
// place (additional data such as "mailbox:<id>:imap_password"), so a
// ciphertext copied to another row or column does not open.
//
// Values written before key ids existed (plain base64, no additional data,
// key = SHA-256 of the secret) still open; NeedsReseal reports them.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const version = "v1"

// ErrUndecryptable means no configured key opens the value.
var ErrUndecryptable = errors.New("stored secret cannot be decrypted with DESK_SECRET_KEY or DESK_SECRET_KEY_PREVIOUS")

type key struct {
	id     string
	aead   cipher.AEAD
	legacy cipher.AEAD
}

// Keyring holds the current key and the previous ones.
type Keyring struct {
	current key
	byID    map[string]key
	all     []key
}

// New builds a keyring. current seals; previous keys only open.
func New(current string, previous ...string) (*Keyring, error) {
	if current == "" {
		return nil, errors.New("secretbox: empty current key")
	}
	k := &Keyring{byID: map[string]key{}}
	for i, secret := range append([]string{current}, previous...) {
		if secret == "" {
			continue
		}
		kk, err := deriveKey(secret)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			k.current = kk
		}
		if _, dup := k.byID[kk.id]; !dup {
			k.byID[kk.id] = kk
			k.all = append(k.all, kk)
		}
	}
	return k, nil
}

func deriveKey(secret string) (key, error) {
	sum := sha256.Sum256([]byte("desk/secretbox/v1\x00" + secret))
	aead, err := newAEAD(sum[:])
	if err != nil {
		return key{}, err
	}
	legacySum := sha256.Sum256([]byte(secret))
	legacy, err := newAEAD(legacySum[:])
	if err != nil {
		return key{}, err
	}
	idSum := sha256.Sum256([]byte("desk/secretbox/key-id\x00" + secret))
	return key{id: hex.EncodeToString(idSum[:4]), aead: aead, legacy: legacy}, nil
}

func newAEAD(k []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// CurrentKeyID is the id of the sealing key (shown in logs, never secret).
func (k *Keyring) CurrentKeyID() string { return k.current.id }

// Seal encrypts plain for the place named by aad. "" stays "".
func (k *Keyring) Seal(plain, aad string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, k.current.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := k.current.aead.Seal(nonce, nonce, []byte(plain), []byte(aad))
	return version + "." + k.current.id + "." + base64.StdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a value sealed for aad.
func (k *Keyring) Open(enc, aad string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if v, id, data, ok := split(enc); ok && v == version {
		kk, known := k.byID[id]
		if !known {
			return "", ErrUndecryptable
		}
		return open(kk.aead, data, []byte(aad))
	}
	for _, kk := range k.all {
		if plain, err := open(kk.legacy, enc, nil); err == nil {
			return plain, nil
		}
	}
	return "", ErrUndecryptable
}

// NeedsReseal reports whether enc is not sealed with the current key in
// the current format.
func (k *Keyring) NeedsReseal(enc string) bool {
	if enc == "" {
		return false
	}
	v, id, _, ok := split(enc)
	return !ok || v != version || id != k.current.id
}

func split(enc string) (v, id, data string, ok bool) {
	parts := strings.SplitN(enc, ".", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func open(aead cipher.AEAD, b64 string, aad []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) < aead.NonceSize() {
		return "", ErrUndecryptable
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], aad)
	if err != nil {
		return "", ErrUndecryptable
	}
	return string(plain), nil
}
