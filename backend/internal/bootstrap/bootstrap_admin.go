package bootstrap

import (
	"context"
	"fmt"

	"mendry/backend/internal/modules/auth/adapter/password"
	authpostgres "mendry/backend/internal/modules/auth/adapter/postgres"
	"mendry/backend/internal/modules/auth/application"
	"mendry/backend/internal/platform/config"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

// BootstrapAdminOptions 包含一次性管理员创建的显式输入。
// Password 不得写入日志或错误，调用方在返回后负责清零原始 byte slice。
type BootstrapAdminOptions struct {
	Options
	Username string
	Password []byte
}

// RunBootstrapAdmin 只连接 PostgreSQL，幂等创建首个启用的 admin 用户。
// 该命令不加载 Redis，也不会修改已存在账号的 password hash。
func RunBootstrapAdmin(ctx context.Context, options BootstrapAdminOptions) (result error) {
	configuration, err := config.LoadBootstrapAdmin(options.Lookup)
	if err != nil {
		return fmt.Errorf("load bootstrap administrator configuration: %w", err)
	}
	logger, logSink, err := logger(options.Options, "mendry-bootstrap-admin", configuration.Common)
	if err != nil {
		return err
	}
	defer closeLogSink(&result, logSink)
	telemetryRuntime, err := telemetry(ctx, options.Options, "mendry-bootstrap-admin", configuration.Common.Environment)
	if err != nil {
		return err
	}
	ctx, processSpan := telemetryRuntime.Tracer("mendry/backend/bootstrap").Start(ctx, "process.bootstrap_admin",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	logStart(ctx, logger, "bootstrap-admin")
	postgresPool, err := openPostgreSQL(ctx, logger, telemetryRuntime, "mendry-bootstrap-admin", configuration.PostgreSQL)
	if err != nil {
		return finishProcess(ctx, processSpan, telemetryRuntime, configuration.Common.ShutdownTimeout, err)
	}
	repository, err := authpostgres.NewRepository(postgresPool)
	if err != nil {
		return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, configuration.Common.ShutdownTimeout,
			fmt.Errorf("create authentication user repository: %w", err),
		)
	}
	bootstrapper, err := application.NewBootstrapper(application.BootstrapOptions{
		Users: repository, Passwords: password.Bcrypt{}, NewUserID: newUUIDv7,
	})
	if err != nil {
		return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, configuration.Common.ShutdownTimeout,
			fmt.Errorf("create administrator bootstrap service: %w", err),
		)
	}
	_, created, err := bootstrapper.BootstrapAdmin(ctx, options.Username, options.Password)
	if err != nil {
		return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, configuration.Common.ShutdownTimeout,
			fmt.Errorf("bootstrap administrator: %w", err),
		)
	}
	message := "administrator already exists"
	if created {
		message = "administrator created"
	}
	if _, err := fmt.Fprintln(options.Output, message); err != nil {
		return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, configuration.Common.ShutdownTimeout,
			fmt.Errorf("write bootstrap administrator result: %w", err),
		)
	}
	logStopped(ctx, logger, "bootstrap-admin")
	return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, configuration.Common.ShutdownTimeout, nil)
}

func newUUIDv7() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
