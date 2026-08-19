package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"fixthe/backend/internal/modules/projects/domain"
)

const KeyVersion int32 = 1

type AESGCM struct {
	aead   cipher.AEAD
	random io.Reader
}

func NewAESGCM(key []byte) (*AESGCM, error) {
	return newAESGCM(key, rand.Reader)
}

func newAESGCM(key []byte, random io.Reader) (*AESGCM, error) {
	if len(key) != 32 || random == nil {
		return nil, fmt.Errorf("project credential encryption requires a 32-byte key and random source")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create project credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create project credential GCM: %w", err)
	}
	return &AESGCM{aead: aead, random: random}, nil
}

func (c *AESGCM) Encrypt(projectID, secretID string, kind domain.SecretKind, plaintext []byte) ([]byte, []byte, int32, error) {
	if projectID == "" || secretID == "" || kind == "" || len(plaintext) == 0 {
		return nil, nil, 0, fmt.Errorf("project credential encryption context is invalid")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return nil, nil, 0, fmt.Errorf("generate project credential nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, plaintext, associatedData(projectID, secretID, kind))
	return ciphertext, nonce, KeyVersion, nil
}

func (c *AESGCM) Decrypt(projectID, secretID string, kind domain.SecretKind, ciphertext, nonce []byte) ([]byte, error) {
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, associatedData(projectID, secretID, kind))
	if err != nil {
		return nil, fmt.Errorf("decrypt project credential: authentication failed")
	}
	return plaintext, nil
}

func associatedData(projectID, secretID string, kind domain.SecretKind) []byte {
	return []byte(fmt.Sprintf("fixthe:project-secret:v%d:%s:%s:%s", KeyVersion, projectID, secretID, kind))
}

const webhookTokenContext = "webhook_token"

// EncryptWebhookToken 用独立 AAD 加密入站 token，不得复用凭据 kind 或 SigningSecretID。
func (c *AESGCM) EncryptWebhookToken(projectID, triggerID string, plaintext []byte) ([]byte, []byte, error) {
	if projectID == "" || triggerID == "" || len(plaintext) == 0 {
		return nil, nil, fmt.Errorf("webhook token encryption context is invalid")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate webhook token nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, plaintext, webhookTokenAssociatedData(projectID, triggerID))
	return ciphertext, nonce, nil
}

func (c *AESGCM) DecryptWebhookToken(projectID, triggerID string, ciphertext, nonce []byte) ([]byte, error) {
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, webhookTokenAssociatedData(projectID, triggerID))
	if err != nil {
		return nil, fmt.Errorf("decrypt webhook token: authentication failed")
	}
	return plaintext, nil
}

func webhookTokenAssociatedData(projectID, triggerID string) []byte {
	return []byte(fmt.Sprintf("fixthe:project-secret:v%d:%s:%s:%s", KeyVersion, projectID, triggerID, webhookTokenContext))
}
