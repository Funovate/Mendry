package postgres

import (
	"context"
	"errors"
	"fmt"

	"mendry/backend/internal/modules/auth/adapter/postgres/authdb"
	"mendry/backend/internal/modules/auth/application"
	"mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/platform/errtrace"
	platformpostgres "mendry/backend/internal/platform/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Repository 使用 auth-owned sqlc queries 持久化本地用户。
type Repository struct {
	queries *authdb.Queries
}

func NewRepository(database authdb.DBTX) (*Repository, error) {
	if database == nil {
		return nil, fmt.Errorf("auth PostgreSQL database is required")
	}
	return &Repository{queries: authdb.New(database)}, nil
}

func (r *Repository) GetByUsername(ctx context.Context, username string) (application.Account, error) {
	row, err := r.queries.GetUserByUsername(platformpostgres.WithOperation(ctx, "auth.user.get_by_username"), username)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.Account{}, application.ErrUserNotFound
	}
	if err != nil {
		return application.Account{}, newRepositoryError("query user by username", err)
	}
	return mapAccount(row)
}

func (r *Repository) Create(ctx context.Context, account application.Account) (domain.User, error) {
	id, err := uuidParameter(account.User.ID)
	if err != nil {
		return domain.User{}, err
	}
	row, err := r.queries.CreateUser(platformpostgres.WithOperation(ctx, "auth.user.create"), authdb.CreateUserParams{
		ID: id, Username: account.User.Username, PasswordHash: account.PasswordHash, Role: string(account.User.Role),
	})
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return domain.User{}, application.ErrUserConflict
		}
		return domain.User{}, newRepositoryError("insert user", err)
	}
	mapped, err := mapAccount(row)
	return mapped.User, err
}

type repositoryError struct {
	operation string
	cause     error
	stack     errtrace.Trace
}

func newRepositoryError(operation string, cause error) repositoryError {
	return repositoryError{operation: operation, cause: cause, stack: errtrace.Capture(1)}
}

func (e repositoryError) Error() string              { return e.operation + " failed" }
func (e repositoryError) Unwrap() error              { return e.cause }
func (e repositoryError) StackTrace() errtrace.Trace { return e.stack }

func mapAccount(row authdb.User) (application.Account, error) {
	if !row.ID.Valid {
		return application.Account{}, fmt.Errorf("user row has invalid ID")
	}
	role, err := domain.ParseRole(row.Role)
	if err != nil {
		return application.Account{}, fmt.Errorf("map user role: %w", err)
	}
	return application.Account{User: domain.User{
		ID: uuid.UUID(row.ID.Bytes).String(), Username: row.Username, Role: role, Enabled: row.Enabled,
	}, PasswordHash: row.PasswordHash}, nil
}

func uuidParameter(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Version() != 7 {
		return pgtype.UUID{}, fmt.Errorf("user ID must be UUIDv7")
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
