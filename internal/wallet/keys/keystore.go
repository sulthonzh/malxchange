package keys

import (
	"encoding/json"
	"errors"
	"fmt"

	"crypto/rand"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// The hot keystore (ADR-0008): the ONLY online key material. One HD
// account per chain family, serialized as xprv strings, encrypted with
// XChaCha20-Poly1305 under a key derived from the env-file passphrase
// with argon2id. The file is created offline by walletctl's key
// ceremony and decrypted only in the signer's memory.

const (
	keystoreVersion = 1
	keystoreAAD     = "malxchange-hot-keystore-v1"
	argonTime       = 1
	argonMemoryKB   = 65536 // 64 MiB
	argonThreads    = 2
)

// HotKeystore is the plaintext payload: the account xprv per family.
type HotKeystore struct {
	Accounts map[string]AccountKeys `json:"accounts"`
}

// AccountKeys is one family's hot account.
type AccountKeys struct {
	XPrv       string `json:"xprv"`
	HotAddress string `json:"hotAddress"`
}

type keystoreFile struct {
	Version int    `json:"version"`
	KDF     string `json:"kdf"`
	Salt    []byte `json:"salt"` // hex-encoded in JSON
	Nonce   []byte `json:"nonce"`
	CT      []byte `json:"ct"`
}

// EncryptHot seals the keystore under the passphrase.
func EncryptHot(hk HotKeystore, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("hot keystore passphrase must not be empty")
	}
	plain, err := json.Marshal(hk)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemoryKB, argonThreads, chacha20poly1305.KeySize)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	ct := aead.Seal(nil, nonce, plain, []byte(keystoreAAD))
	out, err := json.Marshal(keystoreFile{
		Version: keystoreVersion, KDF: "argon2id",
		Salt: salt, Nonce: nonce, CT: ct,
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DecryptHot opens the keystore in the signer's memory. A wrong
// passphrase is ErrBadPassphrase.
func DecryptHot(file []byte, passphrase string) (HotKeystore, error) {
	var kf keystoreFile
	if err := json.Unmarshal(file, &kf); err != nil {
		return HotKeystore{}, fmt.Errorf("hot keystore format: %w", err)
	}
	if kf.Version != keystoreVersion {
		return HotKeystore{}, fmt.Errorf("hot keystore version %d unsupported", kf.Version)
	}
	key := argon2.IDKey([]byte(passphrase), kf.Salt, argonTime, argonMemoryKB, argonThreads, chacha20poly1305.KeySize)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return HotKeystore{}, err
	}
	plain, err := aead.Open(nil, kf.Nonce, kf.CT, []byte(keystoreAAD))
	if err != nil {
		return HotKeystore{}, ErrBadPassphrase
	}
	var hk HotKeystore
	if err := json.Unmarshal(plain, &hk); err != nil {
		return HotKeystore{}, fmt.Errorf("hot keystore payload: %w", err)
	}
	return hk, nil
}
