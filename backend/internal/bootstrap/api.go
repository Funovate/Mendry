package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"time"

	authhttp "fixthe/backend/internal/modules/auth/adapter/http"
	"fixthe/backend/internal/modules/auth/adapter/password"
	authpostgres "fixthe/backend/internal/modules/auth/adapter/postgres"
	authredis "fixthe/backend/internal/modules/auth/adapter/redis"
	authapplication "fixthe/backend/internal/modules/auth/application"
	hookhttp "fixthe/backend/internal/modules/hooks/adapter/http"
	hookapplication "fixthe/backend/internal/modules/hooks/application"
	incidenthttp "fixthe/backend/internal/modules/incidents/adapter/http"
	incidentpostgres "fixthe/backend/internal/modules/incidents/adapter/postgres"
	incidentapplication "fixthe/backend/internal/modules/incidents/application"
	observationhttp "fixthe/backend/internal/modules/observations/adapter/http"
	observationpostgres "fixthe/backend/internal/modules/observations/adapter/postgres"
	observationapplication "fixthe/backend/internal/modules/observations/application"
	projectgit "fixthe/backend/internal/modules/projects/adapter/git"
	projecthttp "fixthe/backend/internal/modules/projects/adapter/http"
	projectopenai "fixthe/backend/internal/modules/projects/adapter/openai"
	projectpostgres "fixthe/backend/internal/modules/projects/adapter/postgres"
	projectsecret "fixthe/backend/internal/modules/projects/adapter/secret"
	projectapplication "fixthe/backend/internal/modules/projects/application"
	remediationgit "fixthe/backend/internal/modules/remediation/adapter/git"
	remediationhttp "fixthe/backend/internal/modules/remediation/adapter/http"
	remediationopenai "fixthe/backend/internal/modules/remediation/adapter/openai"
	remediationpostgres "fixthe/backend/internal/modules/remediation/adapter/postgres"
	remediationsshlog "fixthe/backend/internal/modules/remediation/adapter/sshlog"
	remediationapplication "fixthe/backend/internal/modules/remediation/application"
	systemhttp "fixthe/backend/internal/modules/system/adapter/http"
	"fixthe/backend/internal/modules/system/application"
	"fixthe/backend/internal/platform/config"
	"fixthe/backend/internal/platform/httpserver"

	"go.opentelemetry.io/otel/trace"
)

// RunAPI 验证配置，组装 PostgreSQL、Redis 和 HTTP 边界，并持续服务到
// ctx 被取消或 server 失败。API 不会自动执行 migration。
func RunAPI(ctx context.Context, options Options) error {
	// 先验证全部 API 配置再创建 logger、client 和 listener，避免无效配置留下部分资源。
	apiConfig, err := config.LoadAPI(options.Lookup)
	if err != nil {
		return fmt.Errorf("load API configuration: %w", err)
	}

	logger, err := logger(options, "fixthe-api", apiConfig.Common)
	if err != nil {
		return err
	}
	telemetryRuntime, err := telemetry(ctx, options, "fixthe-api", apiConfig.Common.Environment)
	if err != nil {
		return err
	}
	ctx, processSpan := telemetryRuntime.Tracer("fixthe/backend/bootstrap").Start(ctx, "process.api",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	logStart(ctx, logger, "api")
	postgresPool, err := openPostgreSQL(ctx, logger, telemetryRuntime, "fixthe-api", apiConfig.PostgreSQL)
	if err != nil {
		return finishProcess(ctx, processSpan, telemetryRuntime, apiConfig.Common.ShutdownTimeout, err)
	}
	redisClient, err := openRedis(ctx, logger, telemetryRuntime, "fixthe-api", apiConfig.Redis)
	if err != nil {
		return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, apiConfig.Common.ShutdownTimeout, err)
	}
	dependencies := []application.Dependency{postgresDependency(postgresPool)}
	readinessTimeout := apiConfig.PostgreSQL.HealthTimeout
	if redisClient != nil {
		dependencies = append(dependencies, redisDependency(redisClient))
		readinessTimeout = max(readinessTimeout, apiConfig.Redis.HealthTimeout)
	}
	systemService, err := application.NewService(readinessTimeout, dependencies...)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout, err)
	}
	authRepository, err := authpostgres.NewRepository(postgresPool)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create authentication user repository: %w", err),
		)
	}
	sessionStore, err := authredis.NewStore(authredis.StoreOptions{Client: redisClient, Now: time.Now})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create authentication session store: %w", err),
		)
	}
	passwordHasher := password.Bcrypt{}
	dummyPasswordHash, err := passwordHasher.Hash([]byte("fixthe-dummy-login-password"))
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create authentication password verifier: %w", err),
		)
	}
	authService, err := authapplication.NewService(authapplication.Options{
		Users: authRepository, Sessions: sessionStore, Passwords: passwordHasher,
		DummyPasswordHash: dummyPasswordHash, SessionTTL: apiConfig.Auth.SessionTTL, Now: time.Now, NewUserID: newUUIDv7,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create authentication service: %w", err),
		)
	}
	authHandler, err := authhttp.NewHandler(authhttp.HandlerOptions{
		Service: authService, UserCreator: authService,
		SecureCookie: apiConfig.Common.Environment == "staging" || apiConfig.Common.Environment == "production", Now: time.Now,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create authentication HTTP handler: %w", err),
		)
	}
	projectRepository, err := projectpostgres.NewRepository(postgresPool)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create project repository: %w", err),
		)
	}
	projectCipher, err := projectsecret.NewAESGCM(apiConfig.Encryption.Key)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create project credential cipher: %w", err),
		)
	}
	projectService, err := projectapplication.NewService(projectapplication.Options{
		Repository: projectRepository, Cipher: projectCipher, Git: projectgit.NewLister(logger), LLM: projectopenai.NewLister(nil, logger),
		NewID: newUUIDv7, PublicURL: apiConfig.PublicURL,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create project service: %w", err),
		)
	}
	projectHandler, err := projecthttp.NewHandler(projecthttp.HandlerOptions{Service: projectService, Authentication: authHandler})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create project HTTP handler: %w", err),
		)
	}
	observationRepository, err := observationpostgres.NewRepository(postgresPool)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create observation repository: %w", err),
		)
	}
	observationService, err := observationapplication.NewService(observationapplication.Options{
		Projects: projectService, Repository: observationRepository, NewID: newUUIDv7, Now: time.Now,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create observation service: %w", err),
		)
	}
	observationHandler, err := observationhttp.NewHandler(observationhttp.HandlerOptions{Service: observationService, Authentication: authHandler})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create observation HTTP handler: %w", err),
		)
	}
	incidentRepository, err := incidentpostgres.NewRepository(postgresPool)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create incident repository: %w", err),
		)
	}
	projectBaseline, err := incidentpostgres.NewProjectBaseline(projectRepository)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create project baseline: %w", err),
		)
	}
	remediationStore, err := remediationpostgres.NewRunStore(postgresPool)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation store: %w", err),
		)
	}
	incidentLookup, err := incidentpostgres.NewIncidentLookup(incidentRepository)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create incident lookup: %w", err),
		)
	}
	runtimeLoaders, err := newProjectRuntimeLoaders(projectRepository)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation project loaders: %w", err),
		)
	}
	repositoryReader, err := remediationgit.NewReader(remediationgit.Options{
		Configs: runtimeLoaders, Secrets: runtimeLoaders, Cipher: projectCipher,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation git reader: %w", err),
		)
	}
	evidenceReader, err := remediationsshlog.NewReader(remediationsshlog.Options{
		Sources: runtimeLoaders, Secrets: runtimeLoaders, Cipher: projectCipher,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation ssh log reader: %w", err),
		)
	}
	modelClient, err := remediationopenai.NewClient(remediationopenai.Options{
		Configs: runtimeLoaders, Secrets: runtimeLoaders, Cipher: projectCipher, Logger: logger,
		Timeout: apiConfig.Remediation.ModelTurnTimeout,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation openai client: %w", err),
		)
	}
	// 真实 Git / SSH 日志 / OpenAI 适配器替换占位实现；凭据只在适配器内解密。
	remediationCoordinator := remediationapplication.NewRemediationCoordinatorWithRuntime(
		remediationStore, repositoryReader, evidenceReader, modelClient, incidentLookup, repositoryReader,
		remediationStore, remediationStore,
	)
	// evidence.read allows bounded re-reading of persisted evidence within the current run series.
	remediationCoordinator.SetEvidenceReadPort(remediationStore)
	// Durable working-memory checkpoints are enabled only for resilient_v1 run snapshots.
	remediationCheckpointStore, checkpointStoreErr := remediationpostgres.NewCheckpointStore(postgresPool)
	if checkpointStoreErr != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation checkpoint store: %w", checkpointStoreErr),
		)
	}
	remediationCoordinator.SetCheckpointStore(remediationCheckpointStore)
	remediationTrigger, err := remediationapplication.NewTrigger(remediationStore, remediationCoordinator)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation trigger: %w", err),
		)
	}
	incidentService, err := incidentapplication.NewService(incidentapplication.Options{
		Repository: incidentRepository, Projects: projectService, Baseline: projectBaseline,
		Remediation: remediationTrigger, NewIncidentID: newUUIDv7, Now: time.Now,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create incident service: %w", err),
		)
	}
	incidentHandler, err := incidenthttp.NewHandler(incidenthttp.HandlerOptions{Service: incidentService, Authentication: authHandler})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create incident HTTP handler: %w", err),
		)
	}
	hookService, err := hookapplication.NewService(hookapplication.Options{
		Tokens: projectService, Observations: observationService, Incidents: incidentService, Now: time.Now,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create webhook ingest service: %w", err),
		)
	}
	hookHandler, err := hookhttp.NewHandler(hookhttp.HandlerOptions{Service: hookService})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create webhook HTTP handler: %w", err),
		)
	}
	remediationService, err := remediationapplication.NewService(remediationapplication.ServiceOptions{
		Projects: projectService, Incidents: incidentLookup, Trigger: remediationTrigger, Reviews: remediationStore,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation service: %w", err),
		)
	}
	remediationHandler, err := remediationhttp.NewHandler(remediationhttp.HandlerOptions{
		Service: remediationService, Authentication: authHandler,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation HTTP handler: %w", err),
		)
	}

	mux := http.NewServeMux()
	systemHandler := systemhttp.NewHandler(systemService)
	systemHandler.Register(mux)
	authHandler.Register(mux)
	projectHandler.Register(mux)
	observationHandler.Register(mux)
	incidentHandler.Register(mux)
	hookHandler.Register(mux)
	remediationHandler.Register(mux)
	httpBoundary, err := httpserver.Boundary(httpserver.BoundaryOptions{
		Handler:           mux,
		Logger:            logger,
		MaxBodyBytes:      apiConfig.HTTP.MaxBodyBytes,
		CORSAllowedOrigin: apiConfig.HTTP.CORSAllowedOrigin,
		RequestDebug:      apiConfig.HTTP.RequestDebug,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create HTTP boundary: %w", err),
		)
	}

	server, err := httpserver.New(httpserver.Options{
		Address:           apiConfig.HTTP.Address,
		ReadHeaderTimeout: apiConfig.HTTP.ReadHeaderTimeout,
		ReadTimeout:       apiConfig.HTTP.ReadTimeout,
		WriteTimeout:      apiConfig.HTTP.WriteTimeout,
		IdleTimeout:       apiConfig.HTTP.IdleTimeout,
		ShutdownTimeout:   apiConfig.Common.ShutdownTimeout,
		Handler:           telemetryRuntime.HTTPHandler(httpBoundary),
		Logger:            logger,
		Component:         "api",
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create HTTP server: %w", err),
		)
	}

	if err := server.Run(ctx); err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout, err)
	}

	logStopped(ctx, logger, "api")
	return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout, nil)
}
