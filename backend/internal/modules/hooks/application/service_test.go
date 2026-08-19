package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fixthe/backend/internal/modules/hooks/application"
	incidentdomain "fixthe/backend/internal/modules/incidents/domain"
	observationdomain "fixthe/backend/internal/modules/observations/domain"
	projectapplication "fixthe/backend/internal/modules/projects/application"
)

type fakeTokens struct {
	token   string
	ingress projectapplication.WebhookIngress
	err     error
}

func (f *fakeTokens) LookupWebhookToken(_ context.Context, token string) (projectapplication.WebhookIngress, error) {
	f.token = token
	return f.ingress, f.err
}

type fakeObservations struct {
	projectID, sourceID, message, fingerprint string
	err                                       error
}

func (f *fakeObservations) CreateInbound(_ context.Context, projectID, sourceID, message, fingerprint string, _ time.Time) (observationdomain.Observation, error) {
	f.projectID, f.sourceID, f.message, f.fingerprint = projectID, sourceID, message, fingerprint
	return observationdomain.Observation{ID: "obs"}, f.err
}

type fakeIncidents struct {
	projectID, sourceID, title, fingerprint string
	incident                                incidentdomain.Incident
	created                                 bool
	err                                     error
}

func (f *fakeIncidents) IngestInbound(_ context.Context, projectID, sourceID, title, fingerprint string, _ time.Time) (incidentdomain.Incident, bool, error) {
	f.projectID, f.sourceID, f.title, f.fingerprint = projectID, sourceID, title, fingerprint
	return f.incident, f.created, f.err
}

func TestNormalizeInboundUsesFirstNonEmptyLine(t *testing.T) {
	title, fingerprint, message, err := application.NormalizeInbound("\n  【告警】测试信息  \n告警等级：\n")
	if err != nil || title != "【告警】测试信息" || fingerprint != "【告警】测试信息" || !strings.Contains(message, "告警等级：") {
		t.Fatalf("NormalizeInbound() = %q %q %q %v", title, fingerprint, message, err)
	}
	if _, _, _, err := application.NormalizeInbound("   \n\t"); !errors.Is(err, application.ErrInvalidInput) {
		t.Fatalf("empty body error = %v", err)
	}
	if _, _, _, err := application.NormalizeInbound(string([]byte{0xff, 0xfe})); !errors.Is(err, application.ErrInvalidInput) {
		t.Fatalf("invalid UTF-8 error = %v", err)
	}
	title, fingerprint, _, err = application.NormalizeInbound(strings.Repeat("告", 300) + "\n触发时间：2026-08-10")
	if err != nil || len([]rune(title)) != 240 || len(fingerprint) > 255 {
		t.Fatalf("truncated inbound = titleRunes=%d fingerprintBytes=%d err=%v", len([]rune(title)), len(fingerprint), err)
	}
}

func TestIngestCreatesIncidentAfterObservation(t *testing.T) {
	tokens := &fakeTokens{ingress: projectapplication.WebhookIngress{ProjectID: "project", SourceID: "source"}}
	observations := &fakeObservations{}
	incidents := &fakeIncidents{incident: incidentdomain.Incident{Number: 2049}, created: true}
	service, err := application.NewService(application.Options{Tokens: tokens, Observations: observations, Incidents: incidents, Now: func() time.Time {
		return time.Date(2026, 8, 19, 11, 41, 44, 0, time.UTC)
	}})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	result, err := service.Ingest(context.Background(), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", "【告警】测试信息\n触发时间：2026-08-10")
	if err != nil || result.IncidentID != "INC-2049" || !result.Created {
		t.Fatalf("Ingest() = %#v err=%v", result, err)
	}
	if tokens.token != "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ" || observations.message == "" || incidents.title != "【告警】测试信息" {
		t.Fatalf("tokens=%#v observations=%#v incidents=%#v", tokens, observations, incidents)
	}
}

func TestIngestMapsUnknownToken(t *testing.T) {
	service, err := application.NewService(application.Options{
		Tokens: &fakeTokens{err: projectapplication.ErrNotFound}, Observations: &fakeObservations{}, Incidents: &fakeIncidents{}, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.Ingest(context.Background(), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", "alert"); !errors.Is(err, application.ErrWebhookNotFound) {
		t.Fatalf("unknown token error = %v", err)
	}
}
