package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mendry/backend/internal/modules/auth/domain"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthenticated    = errors.New("authentication required")
	ErrInvalidInput       = errors.New("invalid authentication input")
	ErrUserNotFound       = errors.New("user not found")
	ErrUserConflict       = errors.New("user already exists")
	ErrSessionNotFound    = errors.New("session not found")
)

const (
	minimumPasswordBytes = 12
	maximumPasswordBytes = 72
)

// Account 是 PostgreSQL adapter 与认证用例之间的 credential contract。
// PasswordHash 只能用于验证密码，不得进入 HTTP response、Session 或日志。
type Account struct {
	User         domain.User
	PasswordHash string
}

// Session 是 Redis 中可撤销的服务端认证状态。
type Session struct {
	User      domain.User
	ExpiresAt time.Time
}

// LoginResult 只在 application 到 HTTP 的边界携带一次 raw session token。
type LoginResult struct {
	User      domain.User
	Token     string
	ExpiresAt time.Time
}

type UserRepository interface {
	GetByUsername(context.Context, string) (Account, error)
	Create(context.Context, Account) (domain.User, error)
}

type SessionStore interface {
	Create(context.Context, Session) (string, error)
	Get(context.Context, string) (Session, error)
	Delete(context.Context, string) error
}

type PasswordHasher interface {
	Hash([]byte) (string, error)
	Compare(string, []byte) error
}

// Options 注入认证边界依赖、Session 生命周期和可测试的时钟。
type Options struct {
	Users             UserRepository
	Sessions          SessionStore
	Passwords         PasswordHasher
	DummyPasswordHash string
	SessionTTL        time.Duration
	Now               func() time.Time
	NewUserID         func() (string, error)
}

// Service 组合本地用户、密码验证和 Redis Session 用例。
type Service struct {
	users             UserRepository
	sessions          SessionStore
	passwords         PasswordHasher
	dummyPasswordHash string
	sessionTTL        time.Duration
	now               func() time.Time
	newUserID         func() (string, error)
}

// NewService 在处理 credential 前验证全部依赖。
func NewService(options Options) (*Service, error) {
	if options.Users == nil || options.Sessions == nil || options.Passwords == nil ||
		options.DummyPasswordHash == "" || options.SessionTTL <= 0 || options.Now == nil || options.NewUserID == nil {
		return nil, fmt.Errorf("authentication dependencies are required")
	}
	return &Service{
		users:             options.Users,
		sessions:          options.Sessions,
		passwords:         options.Passwords,
		dummyPasswordHash: options.DummyPasswordHash,
		sessionTTL:        options.SessionTTL,
		now:               options.Now,
		newUserID:         options.NewUserID,
	}, nil
}

// Login 使用相同的公开错误处理未知用户、停用用户和错误密码。
func (s *Service) Login(ctx context.Context, username string, password []byte, previousToken string) (LoginResult, error) {
	normalized, normalizeErr := domain.NormalizeUsername(username)
	validPassword := len(password) > 0 && len(password) <= maximumPasswordBytes
	if normalizeErr != nil || !validPassword {
		_ = s.passwords.Compare(s.dummyPasswordHash, boundedPassword(password))
		return LoginResult{}, ErrInvalidCredentials
	}

	account, err := s.users.GetByUsername(ctx, normalized)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			_ = s.passwords.Compare(s.dummyPasswordHash, password)
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, fmt.Errorf("load login account: %w", err)
	}
	passwordError := s.passwords.Compare(account.PasswordHash, password)
	if !account.User.Enabled || passwordError != nil {
		return LoginResult{}, ErrInvalidCredentials
	}

	expiresAt := s.now().UTC().Add(s.sessionTTL)
	token, err := s.sessions.Create(ctx, Session{User: account.User, ExpiresAt: expiresAt})
	if err != nil {
		return LoginResult{}, fmt.Errorf("create login session: %w", err)
	}
	if previousToken != "" && previousToken != token {
		if err := s.sessions.Delete(ctx, previousToken); err != nil && !errors.Is(err, ErrSessionNotFound) {
			_ = s.sessions.Delete(ctx, token)
			return LoginResult{}, fmt.Errorf("rotate login session: %w", err)
		}
	}
	return LoginResult{User: account.User, Token: token, ExpiresAt: expiresAt}, nil
}

// Authenticate 将 raw cookie token 解析为当前 Principal，并同时检查绝对过期时间。
func (s *Service) Authenticate(ctx context.Context, token string) (domain.User, error) {
	if token == "" {
		return domain.User{}, ErrUnauthenticated
	}
	session, err := s.sessions.Get(ctx, token)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return domain.User{}, ErrUnauthenticated
		}
		return domain.User{}, fmt.Errorf("load login session: %w", err)
	}
	if !session.User.Enabled || !session.ExpiresAt.After(s.now().UTC()) {
		_ = s.sessions.Delete(ctx, token)
		return domain.User{}, ErrUnauthenticated
	}
	return session.User, nil
}

// Logout 撤销已有 token；空 token 和已经过期的 token 保持幂等成功。
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.sessions.Delete(ctx, token); err != nil && !errors.Is(err, ErrSessionNotFound) {
		return fmt.Errorf("delete login session: %w", err)
	}
	return nil
}

// BootstrapOptions 注入一次性管理员创建需要的最小依赖。
type BootstrapOptions struct {
	Users     UserRepository
	Passwords PasswordHasher
	NewUserID func() (string, error)
}

// Bootstrapper 只依赖 PostgreSQL user repository，不要求 API 的 Redis Session store。
type Bootstrapper struct {
	users     UserRepository
	passwords PasswordHasher
	newUserID func() (string, error)
}

func NewBootstrapper(options BootstrapOptions) (*Bootstrapper, error) {
	if options.Users == nil || options.Passwords == nil || options.NewUserID == nil {
		return nil, fmt.Errorf("administrator bootstrap dependencies are required")
	}
	return &Bootstrapper{users: options.Users, passwords: options.Passwords, newUserID: options.NewUserID}, nil
}

// BootstrapAdmin 幂等创建首个管理员，绝不覆盖已有账号或 password hash。
func (s *Bootstrapper) BootstrapAdmin(ctx context.Context, username string, password []byte) (domain.User, bool, error) {
	normalized, err := domain.NormalizeUsername(username)
	if err != nil || len(password) < minimumPasswordBytes || len(password) > maximumPasswordBytes {
		return domain.User{}, false, ErrInvalidInput
	}

	existing, err := s.users.GetByUsername(ctx, normalized)
	if err == nil {
		return existingAdmin(existing.User)
	}
	if !errors.Is(err, ErrUserNotFound) {
		return domain.User{}, false, fmt.Errorf("check bootstrap administrator: %w", err)
	}

	passwordHash, err := s.passwords.Hash(password)
	if err != nil {
		return domain.User{}, false, fmt.Errorf("hash bootstrap administrator password: %w", err)
	}
	id, err := s.newUserID()
	if err != nil {
		return domain.User{}, false, fmt.Errorf("generate bootstrap administrator ID: %w", err)
	}
	created, err := s.users.Create(ctx, Account{User: domain.User{
		ID: id, Username: normalized, Enabled: true,
	}, PasswordHash: passwordHash})
	if err == nil {
		return created, true, nil
	}
	if !errors.Is(err, ErrUserConflict) {
		return domain.User{}, false, fmt.Errorf("create bootstrap administrator: %w", err)
	}

	// 并发 bootstrap 可能同时观察到不存在；唯一约束获胜后重新读取以保持幂等。
	existing, err = s.users.GetByUsername(ctx, normalized)
	if err != nil {
		return domain.User{}, false, fmt.Errorf("reload bootstrap administrator: %w", err)
	}
	return existingAdmin(existing.User)
}

func existingAdmin(user domain.User) (domain.User, bool, error) {
	if !user.Enabled {
		return domain.User{}, false, ErrUserConflict
	}
	return user, false, nil
}

func boundedPassword(password []byte) []byte {
	if len(password) == 0 {
		return []byte("invalid-password")
	}
	if len(password) > maximumPasswordBytes {
		return password[:maximumPasswordBytes]
	}
	return password
}
