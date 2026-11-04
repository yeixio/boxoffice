package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"

	"golang.org/x/crypto/scrypt"
)

// An encrypted backup is magic, a 16-byte salt, a 12-byte nonce, then the
// gzipped dump sealed with AES-256-GCM under a key scrypt derives from the
// passphrase.
var magic = []byte("BOXOFFICE-ENC1\n")

func isEncrypted(data []byte) bool { return bytes.HasPrefix(data, magic) }

func key(passphrase string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(passphrase), salt, 1<<15, 8, 1, 32)
}

func encrypt(plain []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	rand.Read(salt)
	rand.Read(nonce)
	k, err := key(passphrase, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	out := append(append(append([]byte{}, magic...), salt...), nonce...)
	return gcm.Seal(out, nonce, plain, magic), nil
}

func decrypt(data []byte, passphrase string) ([]byte, error) {
	rest := data[len(magic):]
	if len(rest) < 28 {
		return nil, errors.New("the encrypted backup is damaged")
	}
	salt, nonce, sealed := rest[:16], rest[16:28], rest[28:]
	k, err := key(passphrase, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, sealed, magic)
	if err != nil {
		return nil, errors.New("the passphrase doesn't open this backup")
	}
	return plain, nil
}
