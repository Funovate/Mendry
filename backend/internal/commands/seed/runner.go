package seed

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fixthe/backend/dev"
	"fixthe/backend/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

type transactionBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// Runner 原子写入幂等的开发项目、配置和事故数据。
type Runner struct {
	database transactionBeginner
	sql      string
}

func NewRunner(database transactionBeginner) (*Runner, error) {
	sql := dev.SeedSQL()
	if database == nil || strings.TrimSpace(sql) == "" {
		return nil, fmt.Errorf("development seed dependencies are required")
	}
	return &Runner{database: database, sql: sql}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	tx, err := r.database.BeginTx(postgres.WithOperation(ctx, "seed.begin"), pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin development seed: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(postgres.WithOperation(rollbackContext, "seed.rollback"))
	}()

	if _, err := tx.Exec(postgres.WithOperation(ctx, "seed.apply"), r.sql); err != nil {
		return fmt.Errorf("apply development seed: %w", err)
	}
	if err := tx.Commit(postgres.WithOperation(ctx, "seed.commit")); err != nil {
		return fmt.Errorf("commit development seed: %w", err)
	}
	committed = true
	return nil
}
