package secret

import (
	"bytes"
	"testing"

	"mendry/backend/internal/modules/projects/domain"
)

func TestAESGCMUsesUniqueNonceAndBindsProjectContext(t *testing.T) {
	cipher, err := NewAESGCM(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewAESGCM() error = %v", err)
	}
	plaintext := []byte("credential-value")
	first, firstNonce, version, err := cipher.Encrypt("project-a", "secret-a", domain.SecretHTTPBearer, plaintext)
	if err != nil || version != KeyVersion {
		t.Fatalf("Encrypt() = version %d, error %v", version, err)
	}
	second, secondNonce, _, err := cipher.Encrypt("project-a", "secret-a", domain.SecretHTTPBearer, plaintext)
	if err != nil {
		t.Fatalf("second Encrypt() error = %v", err)
	}
	if bytes.Equal(firstNonce, secondNonce) || bytes.Equal(first, plaintext) || bytes.Equal(first, second) {
		t.Fatal("encryption reused nonce or exposed deterministic plaintext")
	}
	decrypted, err := cipher.Decrypt("project-a", "secret-a", domain.SecretHTTPBearer, first, firstNonce)
	if err != nil || !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypt = %q, %v", decrypted, err)
	}
	for _, context := range []struct {
		project, secret string
		kind            domain.SecretKind
	}{
		{project: "project-b", secret: "secret-a", kind: domain.SecretHTTPBearer},
		{project: "project-a", secret: "secret-b", kind: domain.SecretHTTPBearer},
		{project: "project-a", secret: "secret-a", kind: domain.SecretHTTPHeader},
	} {
		if _, err := cipher.Decrypt(context.project, context.secret, context.kind, first, firstNonce); err == nil {
			t.Fatalf("context %#v decrypted ciphertext", context)
		}
	}
}

func TestAESGCMWebhookTokenUsesDistinctContext(t *testing.T) {
	cipher, err := NewAESGCM(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewAESGCM() error = %v", err)
	}
	plaintext := []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")
	ciphertext, nonce, err := cipher.EncryptWebhookToken("project-a", "trigger-a", plaintext)
	if err != nil {
		t.Fatalf("EncryptWebhookToken() error = %v", err)
	}
	decrypted, err := cipher.DecryptWebhookToken("project-a", "trigger-a", ciphertext, nonce)
	if err != nil || !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypt = %q, %v", decrypted, err)
	}
	if _, err := cipher.DecryptWebhookToken("project-b", "trigger-a", ciphertext, nonce); err == nil {
		t.Fatal("foreign project decrypted webhook token")
	}
	if _, err := cipher.Decrypt("project-a", "trigger-a", domain.SecretWebhookHMAC, ciphertext, nonce); err == nil {
		t.Fatal("secret cipher accepted webhook token ciphertext")
	}
}

func TestAESGCMReadsLegacyAssociatedData(t *testing.T) {
	cipher, err := NewAESGCM(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewAESGCM() error = %v", err)
	}
	nonce := bytes.Repeat([]byte{0x17}, cipher.aead.NonceSize())
	secretCiphertext := cipher.aead.Seal(nil, nonce, []byte("legacy-secret"), legacyAssociatedData("project-a", "secret-a", domain.SecretHTTPBearer))
	plaintext, err := cipher.Decrypt("project-a", "secret-a", domain.SecretHTTPBearer, secretCiphertext, nonce)
	if err != nil || string(plaintext) != "legacy-secret" {
		t.Fatalf("legacy secret decrypt = %q, %v", plaintext, err)
	}
	webhookCiphertext := cipher.aead.Seal(nil, nonce, []byte("legacy-token"), legacyWebhookTokenAssociatedData("project-a", "trigger-a"))
	plaintext, err = cipher.DecryptWebhookToken("project-a", "trigger-a", webhookCiphertext, nonce)
	if err != nil || string(plaintext) != "legacy-token" {
		t.Fatalf("legacy webhook decrypt = %q, %v", plaintext, err)
	}
}

func TestAESGCMRequiresThirtyTwoByteKey(t *testing.T) {
	for _, key := range [][]byte{nil, bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{1}, 31), bytes.Repeat([]byte{1}, 33)} {
		if _, err := NewAESGCM(key); err == nil {
			t.Errorf("key length %d accepted", len(key))
		}
	}
}
