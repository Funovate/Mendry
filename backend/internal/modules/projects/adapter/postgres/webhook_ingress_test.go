package postgres

import (
	"errors"
	"testing"

	"mendry/backend/internal/modules/projects/adapter/postgres/projectdb"
	"mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/modules/projects/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestWebhookIngressFromRowValidatesStoredConfiguration(t *testing.T) {
	newUUID := func() pgtype.UUID {
		value, err := uuid.NewV7()
		if err != nil {
			t.Fatal(err)
		}
		return pgtype.UUID{Bytes: value, Valid: true}
	}
	base := projectdb.LookupWebhookTokenRow{ProjectID: newUUID(), SourceID: newUUID(), TriggerID: newUUID()}

	tests := []struct {
		name         string
		config       string
		wantProvider domain.WebhookProvider
		wantTopic    string
		wantNotFound bool
	}{
		{name: "legacy v1", config: `{"schemaVersion":1,"eventTypes":["alarm"],"deduplicationKey":"title"}`, wantProvider: domain.WebhookProviderGeneric},
		{name: "explicit generic v2", config: `{"schemaVersion":2,"provider":"generic","eventTypes":["alarm"],"deduplicationKey":"title"}`, wantProvider: domain.WebhookProviderGeneric},
		{name: "aws v3", config: `{"schemaVersion":3,"provider":"aws_cloudwatch","awsCloudWatch":{"topicArn":"arn:aws:sns:us-east-1:123456789012:mendry-alarms"},"eventTypes":["alarm"],"deduplicationKey":"alarm_arn"}`, wantProvider: domain.WebhookProviderAWSCloudWatch, wantTopic: "arn:aws:sns:us-east-1:123456789012:mendry-alarms"},
		{name: "unknown provider", config: `{"schemaVersion":3,"source":"cloud","severity":"warning","provider":"aws"}`, wantNotFound: true},
		{name: "incomplete aws", config: `{"schemaVersion":3,"source":"cloud","severity":"warning","provider":"aws_cloudwatch"}`, wantNotFound: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := base
			row.TriggerConfig = []byte(test.config)
			ingress, err := webhookIngressFromRow(row)
			if test.wantNotFound {
				if !errors.Is(err, application.ErrNotFound) {
					t.Fatalf("webhookIngressFromRow() error = %v", err)
				}
				return
			}
			if err != nil || ingress.Provider != test.wantProvider || ingress.TopicARN != test.wantTopic || ingress.ProjectID == "" || ingress.SourceID == "" || ingress.TriggerID == "" {
				t.Fatalf("webhookIngressFromRow() = %#v, %v", ingress, err)
			}
		})
	}
}
