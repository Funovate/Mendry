package domain

import (
	"encoding/json"
	"testing"
)

func TestValidateConfigurationAcceptsPrototypeFields(t *testing.T) {
	credentialID := "019ff544-405c-7d24-9f10-cb3fc579605c"
	configuration := validConfiguration()
	configuration.Repository.CredentialSecretID = &credentialID
	configuration.Source.CredentialSecretID = &credentialID
	configuration.Trigger.SigningSecretID = &credentialID
	if err := ValidateConfiguration(configuration); err != nil {
		t.Fatalf("ValidateConfiguration() error = %v", err)
	}
}

func TestValidateConfigurationRejectsSecretLeakAndInvalidReferences(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Configuration)
	}{
		{name: "remote credential", mutate: func(value *Configuration) { value.Repository.RemoteURL = "https://user:token@example.com/repo.git" }},
		{name: "invalid secret ID", mutate: func(value *Configuration) { invalid := "not-a-uuid"; value.Source.CredentialSecretID = &invalid }},
		{name: "authorization header", mutate: func(value *Configuration) {
			value.Source.Config = json.RawMessage(`{"schemaVersion":1,"endpoint":"https://mcp.example.com","transport":"http","headers":{"Authorization":"Bearer secret"},"evidenceProfile":"default","queryScope":"logs"}`)
		}},
		{name: "trailing config", mutate: func(value *Configuration) {
			value.Source.Config = json.RawMessage(`{"schemaVersion":1,"endpoint":"https://mcp.example.com","transport":"http","headers":{},"evidenceProfile":"default","queryScope":"logs"}{}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := validConfiguration()
			test.mutate(&configuration)
			if err := ValidateConfiguration(configuration); err == nil {
				t.Fatal("ValidateConfiguration() error = nil")
			}
		})
	}
}

func validConfiguration() Configuration {
	return Configuration{
		Environment: Environment{Key: "production", Name: "Production"},
		Repository:  Repository{RemoteURL: "https://github.com/example/service.git", SCMProvider: "github", Transport: "https", ProductionBranch: "main", DeployedCommit: "0123456789abcdef0123456789abcdef01234567"},
		Source:      Source{Kind: "mcp", Config: json.RawMessage(`{"schemaVersion":1,"endpoint":"https://mcp.example.com","transport":"http","headers":{"X-Tenant":"payments"},"evidenceProfile":"default","queryScope":"logs"}`), Capabilities: []string{"pull_collection", "context_collection"}, Enabled: true},
		Trigger: Trigger{Kind: "signed_webhook", Config: json.RawMessage(`{"schemaVersion":1,"eventTypes":["error"],"deduplicationKey":"fingerprint"}`), Enabled: true,
			SigningSecretID: stringPointer("019ff544-405c-7d24-9f10-cb3fc579605c")},
		LLM: &LLMProvider{Provider: "openai", BaseURL: "https://api.openai.com", CredentialSecretID: "019ff544-405c-7d24-9f10-cb3fc579605c", Model: "gpt-5.6"},
	}
}

func stringPointer(value string) *string { return &value }

func TestValidateSecretMetadataAllowsNameOnlyUpdates(t *testing.T) {
	secret := Secret{Name: "git-token", Kind: SecretGitCredential}
	if err := ValidateSecretMetadata(secret); err != nil {
		t.Fatalf("ValidateSecretMetadata() error = %v", err)
	}
	if err := ValidateSecret(secret, []byte("replacement")); err != nil {
		t.Fatalf("ValidateSecret() error = %v", err)
	}
}

func TestValidateSecretRejectsMissingPlaintextAndInvalidMetadata(t *testing.T) {
	if err := ValidateSecret(Secret{Name: "git-token", Kind: SecretGitCredential}, nil); err == nil {
		t.Fatal("ValidateSecret() accepted empty rotation material")
	}
	if err := ValidateSecretMetadata(Secret{Name: "", Kind: SecretGitCredential}); err == nil {
		t.Fatal("ValidateSecretMetadata() accepted an empty name")
	}
	if err := ValidateSecretMetadata(Secret{Name: "git-token", Kind: "not-a-kind"}); err == nil {
		t.Fatal("ValidateSecretMetadata() accepted an unknown kind")
	}
}

func TestValidateConfigurationAcceptsWebhookWithoutSigningSecret(t *testing.T) {
	configuration := validConfiguration()
	configuration.Trigger.SigningSecretID = nil
	if err := ValidateConfiguration(configuration); err != nil {
		t.Fatalf("ValidateConfiguration() error = %v", err)
	}
}

func TestValidateWebhookTokenColumns(t *testing.T) {
	if err := ValidateWebhookTokenColumns(nil, nil, nil); err != nil {
		t.Fatalf("empty columns error = %v", err)
	}
	hash := make([]byte, 32)
	nonce := make([]byte, 12)
	if err := ValidateWebhookTokenColumns(hash, []byte("cipher"), nonce); err != nil {
		t.Fatalf("complete columns error = %v", err)
	}
	if err := ValidateWebhookTokenColumns(hash, nil, nonce); err == nil {
		t.Fatal("partial columns accepted")
	}
	if _, err := ParseWebhookToken("short"); err == nil {
		t.Fatal("short token accepted")
	}
	token := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"
	if _, err := ParseWebhookToken(token); err != nil {
		t.Fatalf("ParseWebhookToken() error = %v", err)
	}
	url, err := InboundWebhookURL("http://127.0.0.1:8080", token)
	if err != nil || url != "http://127.0.0.1:8080/hooks/"+token {
		t.Fatalf("InboundWebhookURL() = %q, %v", url, err)
	}
}
