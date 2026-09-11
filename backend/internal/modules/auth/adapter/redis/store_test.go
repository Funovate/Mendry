package redis

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/auth/application"
	"mendry/backend/internal/modules/auth/domain"

	redisclient "github.com/redis/go-redis/v9"
)

type fakeCommands struct {
	values  map[string][]byte
	lastKey string
	lastTTL time.Duration
}

func (f *fakeCommands) SetNX(_ context.Context, key string, value any, ttl time.Duration) *redisclient.BoolCmd {
	f.lastKey, f.lastTTL = key, ttl
	command := redisclient.NewBoolCmd(context.Background())
	if _, exists := f.values[key]; exists {
		command.SetVal(false)
		return command
	}
	f.values[key] = append([]byte(nil), value.([]byte)...)
	command.SetVal(true)
	return command
}

func (f *fakeCommands) Get(_ context.Context, key string) *redisclient.StringCmd {
	command := redisclient.NewStringCmd(context.Background())
	value, exists := f.values[key]
	if !exists {
		command.SetErr(redisclient.Nil)
		return command
	}
	command.SetVal(string(value))
	return command
}

func (f *fakeCommands) Del(_ context.Context, keys ...string) *redisclient.IntCmd {
	command := redisclient.NewIntCmd(context.Background())
	for _, key := range keys {
		delete(f.values, key)
	}
	command.SetVal(int64(len(keys)))
	return command
}

func TestStoreHashesTokenAndRoundTripsSession(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	commands := &fakeCommands{values: make(map[string][]byte)}
	store, err := NewStore(StoreOptions{
		Client: commands, Random: bytes.NewReader(bytes.Repeat([]byte{7}, tokenBytes)), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	session := application.Session{User: domain.User{
		ID: "019ff544-405c-7d10-8f10-cb3fc579605c", Username: "admin", Role: domain.RoleAdmin, Enabled: true,
	}, ExpiresAt: now.Add(time.Hour)}
	token, err := store.Create(context.Background(), session)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if strings.Contains(commands.lastKey, token) || !strings.HasPrefix(commands.lastKey, sessionKeyPrefix) {
		t.Fatalf("Redis key = %q", commands.lastKey)
	}
	if commands.lastTTL != time.Hour || strings.Contains(string(commands.values[commands.lastKey]), token) {
		t.Fatalf("TTL = %s, value = %q", commands.lastTTL, commands.values[commands.lastKey])
	}

	loaded, err := store.Get(context.Background(), token)
	if err != nil || loaded.User.Username != "admin" || loaded.User.Role != domain.RoleAdmin {
		t.Fatalf("Get() = %#v, %v", loaded, err)
	}
	if err := store.Delete(context.Background(), token); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Get(context.Background(), token); err != application.ErrSessionNotFound {
		t.Fatalf("Get(deleted) error = %v", err)
	}
}

func TestStoreReadsAndDeletesLegacySessionKey(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	commands := &fakeCommands{values: make(map[string][]byte)}
	store, err := NewStore(StoreOptions{Client: commands, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, tokenBytes))
	commands.values[legacySessionKey(token)] = []byte(`{"userId":"019ff544-405c-7d10-8f10-cb3fc579605c","username":"admin","role":"admin","expiresAt":"2026-08-13T02:02:03Z"}`)
	if session, err := store.Get(context.Background(), token); err != nil || session.User.Username != "admin" {
		t.Fatalf("Get(legacy) = %#v, %v", session, err)
	}
	if err := store.Delete(context.Background(), token); err != nil {
		t.Fatalf("Delete(legacy) error = %v", err)
	}
	if _, ok := commands.values[legacySessionKey(token)]; ok {
		t.Fatal("legacy session key was not deleted")
	}
}

func TestStoreRejectsMalformedTokenWithoutRedisLookup(t *testing.T) {
	commands := &fakeCommands{values: make(map[string][]byte)}
	store, err := NewStore(StoreOptions{Client: commands, Now: time.Now})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Get(context.Background(), "not-a-token"); err != application.ErrSessionNotFound {
		t.Fatalf("Get() error = %v", err)
	}
}

func TestStoreRejectsExpiredAndCorruptSessionValues(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	commands := &fakeCommands{values: make(map[string][]byte)}
	store, err := NewStore(StoreOptions{Client: commands, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, tokenBytes))
	key := sessionKey(token)

	commands.values[key] = []byte(`{"userId":"not-a-uuid","username":"Admin","role":"owner","expiresAt":"2026-08-13T02:02:03Z"}`)
	if _, err := store.Get(context.Background(), token); err == nil || err == application.ErrSessionNotFound {
		t.Fatalf("Get(corrupt) error = %v", err)
	}

	commands.values[key] = []byte(`{"userId":"019ff544-405c-7d10-8f10-cb3fc579605c","username":"admin","role":"admin","expiresAt":"2026-08-13T01:02:03Z"}`)
	if _, err := store.Get(context.Background(), token); err != application.ErrSessionNotFound {
		t.Fatalf("Get(expired) error = %v", err)
	}
	if _, exists := commands.values[key]; exists {
		t.Fatal("expired session was not deleted")
	}
}
