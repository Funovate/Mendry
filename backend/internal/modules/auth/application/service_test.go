package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"mendry/backend/internal/modules/auth/domain"
)

type fakeUsers struct {
	accounts map[string]Account
	created  *Account
	getError error
}

func (f *fakeUsers) GetByUsername(_ context.Context, username string) (Account, error) {
	if f.getError != nil {
		return Account{}, f.getError
	}
	account, ok := f.accounts[username]
	if !ok {
		return Account{}, ErrUserNotFound
	}
	return account, nil
}

func (f *fakeUsers) Create(_ context.Context, account Account) (domain.User, error) {
	f.created = &account
	if _, exists := f.accounts[account.User.Username]; exists {
		return domain.User{}, ErrUserConflict
	}
	f.accounts[account.User.Username] = account
	return account.User, nil
}

type fakeSessions struct {
	sessions    map[string]Session
	created     Session
	deleted     []string
	deleteError error
}

func (f *fakeSessions) Create(_ context.Context, session Session) (string, error) {
	f.created = session
	f.sessions["session-token"] = session
	return "session-token", nil
}

func (f *fakeSessions) Get(_ context.Context, token string) (Session, error) {
	session, ok := f.sessions[token]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	return session, nil
}

func (f *fakeSessions) Delete(_ context.Context, token string) error {
	f.deleted = append(f.deleted, token)
	if f.deleteError != nil {
		return f.deleteError
	}
	delete(f.sessions, token)
	return nil
}

type fakePasswords struct {
	comparedHashes []string
}

func (*fakePasswords) Hash(password []byte) (string, error) {
	return "hashed:" + string(password), nil
}

func (f *fakePasswords) Compare(hash string, password []byte) error {
	f.comparedHashes = append(f.comparedHashes, hash)
	if hash != "hashed:"+string(password) {
		return errors.New("password mismatch")
	}
	return nil
}

func TestLoginCreatesSessionAndHidesCredentialFailures(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	users := &fakeUsers{accounts: map[string]Account{
		"admin": {User: domain.User{ID: "user-1", Username: "admin", Enabled: true}, PasswordHash: "hashed:correct-password"},
	}}
	sessions := &fakeSessions{sessions: make(map[string]Session)}
	passwords := &fakePasswords{}
	service := newTestService(t, users, sessions, passwords, now)

	result, err := service.Login(context.Background(), " Admin ", []byte("correct-password"), "old-token")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.Token != "session-token" || result.User.Username != "admin" || !result.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("Login() = %#v", result)
	}
	if len(sessions.deleted) != 1 || sessions.deleted[0] != "old-token" {
		t.Fatalf("rotated sessions = %#v", sessions.deleted)
	}

	for _, test := range []struct {
		username string
		password string
	}{
		{username: "missing", password: "correct-password"},
		{username: "admin", password: "wrong-password"},
		{username: "!", password: "wrong-password"},
	} {
		if _, err := service.Login(context.Background(), test.username, []byte(test.password), ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Login(%q) error = %v", test.username, err)
		}
	}
	if len(passwords.comparedHashes) != 4 {
		t.Fatalf("password compare count = %d", len(passwords.comparedHashes))
	}
}

func TestAuthenticateRejectsMissingAndExpiredSessions(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	sessions := &fakeSessions{sessions: map[string]Session{
		"active":  {User: domain.User{ID: "1", Username: "viewer", Enabled: true}, ExpiresAt: now.Add(time.Hour)},
		"expired": {User: domain.User{ID: "1", Username: "viewer", Enabled: true}, ExpiresAt: now},
	}}
	service := newTestService(t, &fakeUsers{accounts: make(map[string]Account)}, sessions, &fakePasswords{}, now)

	user, err := service.Authenticate(context.Background(), "active")
	if err != nil || user.Username != "viewer" {
		t.Fatalf("Authenticate(active) = %#v, %v", user, err)
	}
	for _, token := range []string{"", "missing", "expired"} {
		if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("Authenticate(%q) error = %v", token, err)
		}
	}
	if len(sessions.deleted) != 1 || sessions.deleted[0] != "expired" {
		t.Fatalf("deleted = %#v", sessions.deleted)
	}
}

func TestLoginRemovesNewSessionWhenRotationFails(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	users := &fakeUsers{accounts: map[string]Account{
		"admin": {User: domain.User{ID: "user-1", Username: "admin", Enabled: true}, PasswordHash: "hashed:correct-password"},
	}}
	sessions := &fakeSessions{sessions: make(map[string]Session), deleteError: errors.New("Redis unavailable")}
	service := newTestService(t, users, sessions, &fakePasswords{}, now)

	if _, err := service.Login(context.Background(), "admin", []byte("correct-password"), "old-token"); err == nil {
		t.Fatal("Login() error = nil")
	}
	if len(sessions.deleted) != 2 || sessions.deleted[0] != "old-token" || sessions.deleted[1] != "session-token" {
		t.Fatalf("deleted = %#v", sessions.deleted)
	}
}

func TestDisabledAccountStillVerifiesPassword(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	passwords := &fakePasswords{}
	users := &fakeUsers{accounts: map[string]Account{
		"disabled": {User: domain.User{ID: "user-1", Username: "disabled", Enabled: false}, PasswordHash: "hashed:correct-password"},
	}}
	service := newTestService(t, users, &fakeSessions{sessions: make(map[string]Session)}, passwords, now)
	if _, err := service.Login(context.Background(), "disabled", []byte("correct-password"), ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v", err)
	}
	if len(passwords.comparedHashes) != 1 || passwords.comparedHashes[0] != "hashed:correct-password" {
		t.Fatalf("compared hashes = %#v", passwords.comparedHashes)
	}
}

func TestBootstrapAdminIsIdempotentWithoutReplacingPassword(t *testing.T) {
	users := &fakeUsers{accounts: make(map[string]Account)}
	service, err := NewBootstrapper(BootstrapOptions{
		Users: users, Passwords: &fakePasswords{}, NewUserID: func() (string, error) { return "user-v7", nil },
	})
	if err != nil {
		t.Fatalf("NewBootstrapper() error = %v", err)
	}

	user, created, err := service.BootstrapAdmin(context.Background(), "Admin", []byte("long-enough-password"))
	if err != nil || !created || user.Username != "admin" {
		t.Fatalf("BootstrapAdmin() = %#v, %t, %v", user, created, err)
	}
	firstHash := users.created.PasswordHash
	user, created, err = service.BootstrapAdmin(context.Background(), "admin", []byte("different-password"))
	if err != nil || created || user.Username != "admin" {
		t.Fatalf("second BootstrapAdmin() = %#v, %t, %v", user, created, err)
	}
	if users.accounts["admin"].PasswordHash != firstHash {
		t.Fatal("idempotent bootstrap replaced password hash")
	}

	if _, _, err := service.BootstrapAdmin(context.Background(), "other", []byte("short")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("short password error = %v", err)
	}
}

func newTestService(t *testing.T, users UserRepository, sessions SessionStore, passwords PasswordHasher, now time.Time) *Service {
	t.Helper()
	service, err := NewService(Options{
		Users: users, Sessions: sessions, Passwords: passwords, DummyPasswordHash: "dummy",
		SessionTTL: time.Hour, Now: func() time.Time { return now },
		NewUserID: func() (string, error) { return "019ff544-405c-7d10-8f10-cb3fc579605c", nil },
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}
