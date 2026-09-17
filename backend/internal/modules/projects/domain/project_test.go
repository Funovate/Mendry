package domain

import (
	"encoding/json"
	"strings"
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

func TestParseSSHSourceConfigNormalizesV1ToHostDeployment(t *testing.T) {
	legacy := json.RawMessage(`{
		"schemaVersion": 1,
		"host": "logs.example.invalid",
		"port": 22,
		"user": "collector",
		"projectFolder": "/srv/app",
		"logPath": "/var/log/app.log",
		"mode": "tail"
	}`)
	config, err := ParseSSHSourceConfig(legacy)
	if err != nil {
		t.Fatalf("ParseSSHSourceConfig() error = %v", err)
	}
	if config.SchemaVersion != 2 || config.Deployment.Kind != SSHDeploymentHost || config.Deployment.ContainerName != "" {
		t.Fatalf("normalized SSH config = %#v", config)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal normalized SSH config: %v", err)
	}
	roundTrip, err := ParseSSHSourceConfig(encoded)
	if err != nil || roundTrip.Deployment.Kind != SSHDeploymentHost || roundTrip.Host != config.Host {
		t.Fatalf("SSH config round trip = %#v error=%v", roundTrip, err)
	}
}

func TestParseSSHSourceConfigAcceptsDockerAndRejectsInvalidDeployment(t *testing.T) {
	valid := json.RawMessage(`{
		"schemaVersion": 2,
		"host": "logs.example.invalid",
		"port": 22,
		"user": "collector",
		"projectFolder": "/srv/app",
		"logPath": "/var/log/app.log",
		"mode": "tail",
		"deployment": {"kind": "docker", "containerName": "checkout-api"}
	}`)
	config, err := ParseSSHSourceConfig(valid)
	if err != nil || config.Deployment.Kind != SSHDeploymentDocker || config.Deployment.ContainerName != "checkout-api" {
		t.Fatalf("Docker SSH config = %#v error=%v", config, err)
	}
	for name, raw := range map[string]json.RawMessage{
		"missing deployment":  json.RawMessage(`{"schemaVersion":2,"host":"host","port":22,"user":"user","projectFolder":"/srv","logPath":"/var/log/app.log","mode":"tail"}`),
		"host container":      json.RawMessage(`{"schemaVersion":2,"host":"host","port":22,"user":"user","projectFolder":"/srv","logPath":"/var/log/app.log","mode":"tail","deployment":{"kind":"host","containerName":"unexpected"}}`),
		"docker without name": json.RawMessage(`{"schemaVersion":2,"host":"host","port":22,"user":"user","projectFolder":"/srv","logPath":"/var/log/app.log","mode":"tail","deployment":{"kind":"docker"}}`),
		"unknown field":       json.RawMessage(`{"schemaVersion":2,"host":"host","port":22,"user":"user","projectFolder":"/srv","logPath":"/var/log/app.log","mode":"tail","deployment":{"kind":"host"},"secret":"nope"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSSHSourceConfig(raw); err == nil {
				t.Fatal("ParseSSHSourceConfig() accepted invalid deployment")
			}
		})
	}
}

func TestParseSignedWebhookConfigNormalizesV1AndValidatesV2Provider(t *testing.T) {
	legacy := json.RawMessage(`{"schemaVersion":1,"eventTypes":["alarm"],"deduplicationKey":"title"}`)
	config, err := ParseSignedWebhookConfig(legacy)
	if err != nil || config.SchemaVersion != 2 || config.Provider != WebhookProviderGeneric {
		t.Fatalf("normalized webhook config = %#v error=%v", config, err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal normalized webhook config: %v", err)
	}
	roundTrip, err := ParseSignedWebhookConfig(encoded)
	if err != nil || roundTrip.Provider != WebhookProviderGeneric || roundTrip.DeduplicationKey != "title" {
		t.Fatalf("webhook config round trip = %#v error=%v", roundTrip, err)
	}
	tencent, err := ParseSignedWebhookConfig(json.RawMessage(`{"schemaVersion":2,"provider":"tencent_cls","eventTypes":["alarm"],"deduplicationKey":"title"}`))
	if err != nil || tencent.Provider != WebhookProviderTencentCLS {
		t.Fatalf("Tencent webhook config = %#v error=%v", tencent, err)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"schemaVersion":2,"provider":"unknown","eventTypes":["alarm"],"deduplicationKey":"title"}`),
		json.RawMessage(`{"schemaVersion":2,"eventTypes":["alarm"],"deduplicationKey":"title"}`),
		json.RawMessage(`{"schemaVersion":2,"provider":"generic","eventTypes":["alarm"],"deduplicationKey":"title","extra":true}`),
	} {
		if _, err := ParseSignedWebhookConfig(raw); err == nil {
			t.Fatalf("ParseSignedWebhookConfig() accepted invalid v2 config: %s", raw)
		}
	}
}

func TestParseSignedWebhookConfigValidatesAWSCloudWatchV3(t *testing.T) {
	config, err := ParseSignedWebhookConfig(json.RawMessage(`{"schemaVersion":3,"provider":"aws_cloudwatch","eventTypes":["alarm"],"deduplicationKey":"alarm_arn","awsCloudWatch":{"topicArn":"arn:aws:sns:us-east-1:123456789012:mendry-alarms"}}`))
	if err != nil || config.Provider != WebhookProviderAWSCloudWatch || config.AWSCloudWatch == nil || config.AWSCloudWatch.TopicARN == "" {
		t.Fatalf("AWS webhook config = %#v error=%v", config, err)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"schemaVersion":2,"provider":"aws_cloudwatch","eventTypes":["alarm"],"deduplicationKey":"title"}`),
		json.RawMessage(`{"schemaVersion":3,"provider":"generic","eventTypes":["alarm"],"deduplicationKey":"alarm_arn","awsCloudWatch":{"topicArn":"arn:aws:sns:us-east-1:123456789012:mendry-alarms"}}`),
		json.RawMessage(`{"schemaVersion":3,"provider":"aws_cloudwatch","eventTypes":["alarm"],"deduplicationKey":"title","awsCloudWatch":{"topicArn":"arn:aws:sns:us-east-1:123456789012:alarms"}}`),
		json.RawMessage(`{"schemaVersion":3,"provider":"aws_cloudwatch","eventTypes":["alarm","ok"],"deduplicationKey":"alarm_arn","awsCloudWatch":{"topicArn":"arn:aws:sns:us-east-1:123456789012:alarms"}}`),
		json.RawMessage(`{"schemaVersion":3,"provider":"aws_cloudwatch","eventTypes":["alarm"],"deduplicationKey":"alarm_arn","awsCloudWatch":{"topicArn":"arn:aws-cn:sns:cn-north-1:123456789012:alarms"}}`),
		json.RawMessage(`{"schemaVersion":3,"provider":"aws_cloudwatch","eventTypes":["alarm"],"deduplicationKey":"alarm_arn","awsCloudWatch":{"topicArn":"arn:aws:sns:us-east-1:123456789012:alarms.fifo"}}`),
	} {
		if _, err := ParseSignedWebhookConfig(raw); err == nil {
			t.Fatalf("ParseSignedWebhookConfig() accepted invalid AWS config: %s", raw)
		}
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

func TestValidateRemediationPolicyCompatibilityAndAutoRequirements(t *testing.T) {
	legacy := RemediationPolicy{AgentLoopMode: AgentLoopModeResilientV1}
	if err := ValidateRemediationPolicy(legacy); err != nil {
		t.Fatalf("legacy policy must remain analysis-only compatible: %v", err)
	}

	auto := DefaultRemediationPolicy()
	auto.AgentLoopMode = AgentLoopModeResilientV1
	auto.ExecutionMode = RemediationExecutionAutoHotfix
	auto.Publication.GitCredentialSecretID = "credential-id"
	if err := ValidateRemediationPolicy(auto); err != nil {
		t.Fatalf("valid basic auto policy rejected: %v", err)
	}

	enhanced := true
	auto.ValidationProfile.Enabled = &enhanced
	auto.ValidationProfile.ImageDigest = "sha256:" + strings.Repeat("a", 64)
	auto.ValidationProfile.WorkingDirectory = "."
	auto.ValidationProfile.RequiredCommands = []ValidationCommand{{ID: "unit", Version: 1, Argv: []string{"go", "test", "./..."}, TimeoutSeconds: 600}}
	if err := ValidateRemediationPolicy(auto); err != nil {
		t.Fatalf("valid auto policy rejected: %v", err)
	}

	auto.AgentLoopMode = AgentLoopModeLegacy
	if err := ValidateRemediationPolicy(auto); err == nil {
		t.Fatal("auto hotfix accepted legacy loop mode")
	}
	auto.AgentLoopMode = AgentLoopModeResilientV1
	auto.ValidationProfile.RequiredCommands = nil
	if err := ValidateRemediationPolicy(auto); err == nil {
		t.Fatal("auto hotfix accepted missing required commands")
	}
}
