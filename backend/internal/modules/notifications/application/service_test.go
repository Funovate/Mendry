package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"mendry/backend/internal/modules/notifications/adapter/platform"
	"mendry/backend/internal/modules/notifications/application"
	"mendry/backend/internal/modules/notifications/domain"
	projectsecret "mendry/backend/internal/modules/projects/adapter/secret"
)

type memoryStore struct {
	channels      map[string]domain.Channel
	delivery      *domain.Delivery
	finishedError error
	finishCount   int
	claimError    error
}

func (s *memoryStore) ListChannels(context.Context, string) ([]domain.Channel, error) {
	return nil, nil
}
func (s *memoryStore) GetChannel(_ context.Context, projectID, id string) (domain.Channel, error) {
	c, ok := s.channels[id]
	if !ok || c.ProjectID != projectID {
		return domain.Channel{}, domain.ErrNotFound
	}
	return c, nil
}
func (s *memoryStore) SaveChannel(_ context.Context, c domain.Channel, _ bool) (domain.Channel, error) {
	s.channels[c.ID] = c
	return c, nil
}
func (s *memoryStore) DeleteChannel(context.Context, string, string) error { return nil }
func (s *memoryStore) ListDeliveries(context.Context, string) ([]domain.Delivery, error) {
	return nil, nil
}
func (s *memoryStore) RetryDelivery(context.Context, string, string) error { return nil }
func (s *memoryStore) Claim(context.Context) (domain.Delivery, error) {
	if s.claimError != nil {
		return domain.Delivery{}, s.claimError
	}
	if s.delivery == nil {
		return domain.Delivery{}, domain.ErrNotFound
	}
	return *s.delivery, nil
}
func (s *memoryStore) Finish(_ context.Context, _ domain.Delivery, err error) error {
	s.finishedError = err
	s.finishCount++
	return nil
}

type sender struct {
	credentials domain.Credentials
	calls       int
	err         error
}

func (s *sender) Send(_ context.Context, _ string, c domain.Credentials, _ string) error {
	s.credentials = c
	s.calls++
	return s.err
}
func service(t *testing.T) (*application.Service, *memoryStore, *sender) {
	t.Helper()
	cipher, err := projectsecret.NewAESGCM(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{channels: map[string]domain.Channel{}}
	out := &sender{}
	return application.NewService(store, cipher, out, platform.Validate), store, out
}
func input() domain.ChannelInput {
	return domain.ChannelInput{Name: "On call", Platform: "telegram", Enabled: true, Credentials: &domain.Credentials{BotToken: "123456:abcdefghijklmnop", ChatID: "-100123"}}
}
func TestChannelEncryptionScopedUpdateAndRedaction(t *testing.T) {
	ctx := context.Background()
	svc, store, out := service(t)
	projectID := uuid.NewString()
	c, err := svc.SaveChannel(ctx, projectID, "", input())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(c.Ciphertext), "abcdefghijklmnop") {
		t.Fatal("credentials plaintext")
	}
	body, _ := json.Marshal(c)
	if strings.Contains(string(body), "ciphertext") || strings.Contains(string(body), "abcdefghijklmnop") || strings.Contains(string(body), "nonce") {
		t.Fatalf("credential leak %s", body)
	}
	updated, err := svc.SaveChannel(ctx, projectID, c.ID, domain.ChannelInput{Name: "Ops renamed", Platform: "telegram", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if string(updated.Ciphertext) != string(c.Ciphertext) {
		t.Fatal("metadata update replaced credentials")
	}
	if err := svc.TestChannel(ctx, projectID, c.ID); err != nil || out.calls != 1 || out.credentials.BotToken != input().Credentials.BotToken {
		t.Fatalf("test failed %v %#v", err, out)
	}
	if _, err := svc.SaveChannel(ctx, uuid.NewString(), c.ID, input()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross project update %v", err)
	}
	if _, err := svc.SaveChannel(ctx, projectID, c.ID, domain.ChannelInput{Name: "Ops", Platform: "feishu"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("platform mutation %v", err)
	}
	store.delivery = &domain.Delivery{ID: uuid.NewString(), ProjectID: projectID, ChannelID: c.ID, Ciphertext: c.Ciphertext, Nonce: c.Nonce, Platform: c.Platform, Message: "event"}
	delete(store.channels, c.ID)
	store.claimError = domain.ErrNotFound
	done, err := svc.DeliverOne(ctx)
	if err != nil || done || out.calls != 1 || store.finishCount != 0 {
		t.Fatalf("worker sent delivery rejected by claim after deletion: %t %v", done, err)
	}
}
func TestWorkerUsesEncryptedCredentialsReturnedByCurrentChannelClaim(t *testing.T) {
	ctx := context.Background()
	svc, store, out := service(t)
	original, err := svc.SaveChannel(ctx, uuid.NewString(), "", input())
	if err != nil {
		t.Fatal(err)
	}
	replacement := input()
	replacement.Credentials = &domain.Credentials{BotToken: "999999:replacementabcdefghijklmnop", ChatID: "-100999"}
	current, err := svc.SaveChannel(ctx, original.ProjectID, original.ID, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if string(current.Ciphertext) == string(original.Ciphertext) {
		t.Fatal("rotation did not replace encrypted envelope")
	}
	store.delivery = &domain.Delivery{ID: uuid.NewString(), ProjectID: current.ProjectID, ChannelID: current.ID, Platform: current.Platform, Ciphertext: current.Ciphertext, Nonce: current.Nonce}
	if processed, err := svc.DeliverOne(ctx); err != nil || !processed || out.calls != 1 || out.credentials != *replacement.Credentials || store.finishedError != nil {
		t.Fatalf("worker did not send claimed current credentials: %t %v", processed, err)
	}
}

func TestWorkerPersistsFailedSendAndFencesCredentialContext(t *testing.T) {
	ctx := context.Background()
	svc, store, out := service(t)
	c, err := svc.SaveChannel(ctx, uuid.NewString(), "", input())
	if err != nil {
		t.Fatal(err)
	}
	store.delivery = &domain.Delivery{ID: uuid.NewString(), ProjectID: c.ProjectID, ChannelID: c.ID, Ciphertext: c.Ciphertext, Nonce: c.Nonce, Platform: c.Platform}
	out.err = errors.New("failed")
	if processed, err := svc.DeliverOne(ctx); err != nil || !processed || store.finishedError == nil || store.finishCount != 1 {
		t.Fatalf("failed send not persisted: %v", err)
	}
	store.delivery.ProjectID = uuid.NewString()
	out.err = nil
	if _, err := svc.DeliverOne(ctx); err != nil || store.finishedError == nil || out.calls != 1 {
		t.Fatalf("cross project decrypt accepted: %v", err)
	}
	store.delivery = nil
	if processed, err := svc.DeliverOne(ctx); processed || err != nil {
		t.Fatalf("empty queue: %t %v", processed, err)
	}
	store.claimError = errors.New("db unavailable")
	if processed, err := svc.DeliverOne(ctx); processed || err == nil {
		t.Fatalf("db error suppressed: %t %v", processed, err)
	}
}
