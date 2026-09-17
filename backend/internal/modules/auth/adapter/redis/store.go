package redis

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"mendry/backend/internal/modules/auth/application"
	"mendry/backend/internal/modules/auth/domain"

	"github.com/google/uuid"
	redisclient "github.com/redis/go-redis/v9"
)

const (
	sessionKeyPrefix       = "mendry:session:v1:"
	legacySessionKeyPrefix = "fixthe:session:v1:"
	tokenBytes             = 32
	createAttempts         = 3
)

type commandClient interface {
	SetNX(context.Context, string, any, time.Duration) *redisclient.BoolCmd
	Get(context.Context, string) *redisclient.StringCmd
	Del(context.Context, ...string) *redisclient.IntCmd
}

type sessionValue struct {
	UserID    string    `json:"userId"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// StoreOptions 提供 Redis command surface、随机源和时钟。
type StoreOptions struct {
	Client commandClient
	Random io.Reader
	Now    func() time.Time
}

// Store 持有 auth Session 的唯一 Redis 编码和 key 规则。
type Store struct {
	client commandClient
	random io.Reader
	now    func() time.Time
}

func NewStore(options StoreOptions) (*Store, error) {
	if options.Client == nil {
		return nil, fmt.Errorf("auth Redis client is required")
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Store{client: options.Client, random: options.Random, now: options.Now}, nil
}

func (s *Store) Create(ctx context.Context, session application.Session) (string, error) {
	ttl := time.Until(session.ExpiresAt)
	if s.now != nil {
		ttl = session.ExpiresAt.Sub(s.now().UTC())
	}
	if ttl <= 0 {
		return "", fmt.Errorf("session expiry must be in the future")
	}
	payload, err := json.Marshal(sessionValue{
		UserID: session.User.ID, Username: session.User.Username, ExpiresAt: session.ExpiresAt.UTC(),
	})
	if err != nil {
		return "", fmt.Errorf("encode Redis session: %w", err)
	}
	for range createAttempts {
		token, err := s.newToken()
		if err != nil {
			return "", fmt.Errorf("generate session token: %w", err)
		}
		created, err := s.client.SetNX(ctx, sessionKey(token), payload, ttl).Result()
		if err != nil {
			return "", fmt.Errorf("store Redis session: %w", err)
		}
		if created {
			return token, nil
		}
	}
	return "", fmt.Errorf("store Redis session after token collision")
}

func (s *Store) Get(ctx context.Context, token string) (application.Session, error) {
	if !validToken(token) {
		return application.Session{}, application.ErrSessionNotFound
	}
	payload, err := s.client.Get(ctx, sessionKey(token)).Bytes()
	key := sessionKey(token)
	if err == redisclient.Nil {
		key = legacySessionKey(token)
		payload, err = s.client.Get(ctx, key).Bytes()
	}
	if err == redisclient.Nil {
		return application.Session{}, application.ErrSessionNotFound
	}
	if err != nil {
		return application.Session{}, fmt.Errorf("read Redis session: %w", err)
	}
	var value sessionValue
	if err := json.Unmarshal(payload, &value); err != nil {
		return application.Session{}, fmt.Errorf("decode Redis session: %w", err)
	}
	userID, idError := uuid.Parse(value.UserID)
	username, usernameError := domain.NormalizeUsername(value.Username)
	if idError != nil || userID.Version() != 7 || usernameError != nil || username != value.Username || value.ExpiresAt.IsZero() {
		return application.Session{}, fmt.Errorf("Redis session value is invalid")
	}
	if !value.ExpiresAt.After(s.now().UTC()) {
		_ = s.client.Del(ctx, key).Err()
		return application.Session{}, application.ErrSessionNotFound
	}
	return application.Session{User: domain.User{
		ID: value.UserID, Username: value.Username, Enabled: true,
	}, ExpiresAt: value.ExpiresAt.UTC()}, nil
}

func (s *Store) Delete(ctx context.Context, token string) error {
	if !validToken(token) {
		return nil
	}
	if err := s.client.Del(ctx, sessionKey(token), legacySessionKey(token)).Err(); err != nil {
		return fmt.Errorf("delete Redis session: %w", err)
	}
	return nil
}

func (s *Store) newToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := io.ReadFull(s.random, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validToken(token string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(raw) == tokenBytes
}

func sessionKey(token string) string {
	digest := sha256.Sum256([]byte(token))
	return sessionKeyPrefix + hex.EncodeToString(digest[:])
}

func legacySessionKey(token string) string {
	digest := sha256.Sum256([]byte(token))
	return legacySessionKeyPrefix + hex.EncodeToString(digest[:])
}
