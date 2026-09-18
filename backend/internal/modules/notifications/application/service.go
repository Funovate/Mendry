package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"mendry/backend/internal/modules/notifications/domain"
	projectdomain "mendry/backend/internal/modules/projects/domain"
)

type Store interface {
	ListChannels(context.Context, string) ([]domain.Channel, error)
	GetChannel(context.Context, string, string) (domain.Channel, error)
	SaveChannel(context.Context, domain.Channel, bool) (domain.Channel, error)
	DeleteChannel(context.Context, string, string) error
	ListDeliveries(context.Context, string) ([]domain.Delivery, error)
	RetryDelivery(context.Context, string, string) error
	Claim(context.Context) (domain.Delivery, error)
	Finish(context.Context, domain.Delivery, error) error
}
type Cipher interface {
	Encrypt(string, string, projectdomain.SecretKind, []byte) ([]byte, []byte, int32, error)
	Decrypt(string, string, projectdomain.SecretKind, []byte, []byte) ([]byte, error)
}
type Sender interface {
	Send(context.Context, string, domain.Credentials, string) error
}
type Service struct {
	store    Store
	cipher   Cipher
	sender   Sender
	validate func(string, domain.Credentials) error
}

const credentialKind projectdomain.SecretKind = "notification_channel"

func NewService(store Store, cipher Cipher, sender Sender, validate func(string, domain.Credentials) error) *Service {
	return &Service{store: store, cipher: cipher, sender: sender, validate: validate}
}
func (s *Service) ListChannels(ctx context.Context, projectID string) ([]domain.Channel, error) {
	return s.store.ListChannels(ctx, projectID)
}
func (s *Service) SaveChannel(ctx context.Context, projectID, id string, input domain.ChannelInput) (domain.Channel, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 100 {
		return domain.Channel{}, domain.ErrInvalidInput
	}
	create := id == ""
	c := domain.Channel{ProjectID: projectID, Name: input.Name, Platform: input.Platform, Enabled: input.Enabled}
	if create {
		c.ID = uuid.NewString()
		if input.Credentials == nil {
			return c, domain.ErrInvalidInput
		}
	} else {
		if _, err := uuid.Parse(id); err != nil {
			return c, domain.ErrInvalidInput
		}
		existing, err := s.store.GetChannel(ctx, projectID, id)
		if err != nil {
			return c, err
		}
		if input.Platform != existing.Platform {
			return c, domain.ErrInvalidInput
		}
		c.ID = id
		c.Ciphertext = existing.Ciphertext
		c.Nonce = existing.Nonce
	}
	if input.Credentials != nil {
		if err := s.validate(c.Platform, *input.Credentials); err != nil {
			return c, err
		}
		plaintext, err := json.Marshal(input.Credentials)
		if err != nil {
			return c, domain.ErrInvalidInput
		}
		c.Ciphertext, c.Nonce, _, err = s.cipher.Encrypt(projectID, c.ID, credentialKind, plaintext)
		if err != nil {
			return c, err
		}
	}
	return s.store.SaveChannel(ctx, c, create)
}
func (s *Service) DeleteChannel(ctx context.Context, projectID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return domain.ErrInvalidInput
	}
	return s.store.DeleteChannel(ctx, projectID, id)
}
func (s *Service) TestChannel(ctx context.Context, projectID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return domain.ErrInvalidInput
	}
	c, err := s.store.GetChannel(ctx, projectID, id)
	if err != nil {
		return err
	}
	credentials, err := s.decrypt(c.ProjectID, c.ID, c.Ciphertext, c.Nonce)
	if err != nil {
		return err
	}
	return s.sender.Send(ctx, c.Platform, credentials, "Mendry notification test")
}
func (s *Service) ListDeliveries(ctx context.Context, projectID string) ([]domain.Delivery, error) {
	return s.store.ListDeliveries(ctx, projectID)
}
func (s *Service) RetryDelivery(ctx context.Context, projectID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return domain.ErrInvalidInput
	}
	return s.store.RetryDelivery(ctx, projectID, id)
}
func (s *Service) decrypt(projectID, id string, ciphertext, nonce []byte) (domain.Credentials, error) {
	if len(nonce) != 12 || len(ciphertext) < 17 {
		return domain.Credentials{}, errors.New("invalid notification credential envelope")
	}
	plaintext, err := s.cipher.Decrypt(projectID, id, credentialKind, ciphertext, nonce)
	if err != nil {
		return domain.Credentials{}, errors.New("notification credential decryption failed")
	}
	var c domain.Credentials
	if err := json.Unmarshal(plaintext, &c); err != nil {
		return c, errors.New("invalid notification credential")
	}
	return c, nil
}

// Run polls durable jobs until cancellation. A 60s database lease exceeds the 20s send budget.
func (s *Service) Run(ctx context.Context, observe func(string)) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		processed, err := s.DeliverOne(ctx)
		if err != nil && observe != nil {
			observe("notification_worker_error")
		}
		delay := time.Second
		if processed && err == nil {
			delay = 0
		}
		timer.Reset(delay)
	}
}
func (s *Service) DeliverOne(ctx context.Context) (bool, error) {
	d, err := s.store.Claim(ctx)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	credentials, sendErr := s.decrypt(d.ProjectID, d.ChannelID, d.Ciphertext, d.Nonce)
	if sendErr == nil {
		sendErr = s.sender.Send(sendCtx, d.Platform, credentials, d.Message)
	}
	cancel()
	// Persist a failed send even during shutdown; a lost ack still yields at-least-once retries.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	return true, s.store.Finish(finishCtx, d, sendErr)
}
