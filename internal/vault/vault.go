package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/pbkdf2"
)

const (
	SaltSize = 16
	KeyLen   = 32 // AES-256
)

func deriveKey(passphrase string, salt []byte) []byte {
	return pbkdf2.Key([]byte(passphrase), salt, 10000, KeyLen, sha256.New)
}

func EncryptData(plaintext []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// 打包存储格式: salt (16 bytes) + nonce (12 bytes) + ciphertext
	finalData := make([]byte, 0, len(salt)+len(nonce)+len(ciphertext))
	finalData = append(finalData, salt...)
	finalData = append(finalData, nonce...)
	finalData = append(finalData, ciphertext...)

	return finalData, nil
}

func DecryptData(data []byte, passphrase string) ([]byte, error) {
	if len(data) < SaltSize+12 {
		return nil, fmt.Errorf("加密数据无效或长度过短")
	}

	salt := data[:SaltSize]
	nonce := data[SaltSize : SaltSize+12]
	ciphertext := data[SaltSize+12:]

	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("解密失败：密码错误或数据损坏")
	}

	return plaintext, nil
}
