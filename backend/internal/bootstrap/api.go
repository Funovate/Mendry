package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"time"

	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	"mendry/backend/internal/modules/auth/adapter/password"
	authpostgres "mendry/backend/internal/modules/auth/adapter/postgres"
	authredis "mendry/backend/internal/modules/auth/adapter/redis"
	authapplication "mendry/backend/internal/modules/auth/application"
	hookawssns "mendry/backend/internal/modules/hooks/adapter/awssns"
	hookhttp "mendry/backend/internal/modules/hooks/adapter/http"
	hookllm "mendry/backend/internal/modules/hooks/adapter/llm"
	hooklogging "mendry/backend/internal/modules/hooks/adapter/logging"
	hooktencentcls "mendry/backend/internal/modules/hooks/adapter/tencentcls"
	hookapplication "mendry/backend/internal/modules/hooks/application"
	incidenthttp "mendry/backend/internal/modules/incidents/adapter/http"
	incidentpostgres "mendry/backend/internal/modules/incidents/adapter/postgres"
	incidentapplication "mendry/backend/internal/modules/incidents/application"
	observationhttp "mendry/backend/internal/modules/observations/adapter/http"
	observationpostgres "mendry/backend/internal/modules/observations/adapter/postgres"
	observationapplication "mendry/backend/internal/modules/observations/application"
	overviewhttp "mendry/backend/internal/modules/overview/adapter/http"
	overviewpostgres "mendry/backend/internal/modules/overview/adapter/postgres"
	overviewapplication "mendry/backend/internal/modules/overview/application"
	projectgit "mendry/backend/internal/modules/projects/adapter/git"
	projecthttp "mendry/backend/internal/modules/projects/adapter/http"
	projectopenai "mendry/backend/internal/modules/projects/adapter/openai"
	projectpostgres "mendry/backend/internal/modules/projects/adapter/postgres"
	projectsecret "mendry/backend/internal/modules/projects/adapter/secret"
	projectapplication "mendry/backend/internal/modules/projects/application"
	remediationgit "mendry/backend/internal/modules/remediation/adapter/git"
	remediationhttp "mendry/backend/internal/modules/remediation/adapter/http"
	remediationlogging "mendry/backend/internal/modules/remediation/adapter/logging"
	remediationmcp "mendry/backend/internal/modules/remediation/adapter/mcp"
	remediationmetrics "mendry/backend/internal/modules/remediation/adapter/metrics"
	remediationopenai "mendry/backend/internal/modules/remediation/adapter/openai"
	remediationpostgres "mendry/backend/internal/modules/remediation/adapter/postgres"
	remediationsshlog "mendry/backend/internal/modules/remediation/adapter/sshlog"
	remediationapplication "mendry/backend/internal/modules/remediation/application"
	systemhttp "mendry/backend/internal/modules/system/adapter/http"
	"mendry/backend/internal/modules/system/application"
	"mendry/backend/internal/platform/config"
	"mendry/backend/internal/platform/httpserver"

	"go.opentelemetry.io/otel/trace"
)

// RunAPI 验证配置，组装 PostgreSQL、Redis 和 HTTP 边界，并持续服务到
// ctx 被取消或 server 失败。API 不会自动执行 migration。
func RunAPI(ctx context.Context, options Options) (result error) {
	// 先验证全部 API 配置再创建 logger、client 和 listener，避免无效配置留下部分资源。
	apiConfig, err := config.LoadAPI(options.Lookup)
	if err != nil {
		return fmt.Errorf("load API configuration: %w", err)
	}

	logger, logSink, err := logger(options, "mendry-api", apiConfig.Common)
	if err != nil {
		return err
	}
	defer closeLogSink(&result, logSink)
	telemetryRuntime, err := telemetry(ctx, options, "mendry-api", apiConfig.Common.Environment)
	if err != nil {
		return err
	}
	ctx, processSpan := telemetryRuntime.Tracer("mendry/backend/bootstrap").Start(ctx, "process.api",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	logStart(ctx, logger, "api")
	postgresPool, err := openPostgreSQL(ctx, logger, telemetryRuntime, "mendry-api", apiConfig.PostgreSQL)
	if err != nil {
		return finishProcess(ctx, processSpan, telemetryRuntime, apiConfig.Common.ShutdownTimeout, err)
	}
	redisClient, err := openRedis(ctx, logger, telemetryRuntime, "mendry-api", apiConfig.Redis)
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
	dummyPasswordHash, err := passwordHasher.Hash([]byte("mendry-dummy-login-password"))
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
		Service:      authService,
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
	containerProbe, err := remediationsshlog.NewContainerProbe(remediationsshlog.ContainerProbeOptions{
		Secrets: projectRepository, Cipher: projectCipher, Logger: logger,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create Docker container probe: %w", err),
		)
	}
	projectService, err := projectapplication.NewService(projectapplication.Options{
		Repository: projectRepository, Cipher: projectCipher, Git: projectgit.NewLister(logger), LLM: projectopenai.NewLister(nil, logger), Containers: containerProbe,
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
		Configs: runtimeLoaders, Secrets: runtimeLoaders, Cipher: projectCipher, Logger: logger,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation git reader: %w", err),
		)
	}
	evidenceReader, err := remediationsshlog.NewReader(remediationsshlog.Options{
		Sources: runtimeLoaders, Secrets: runtimeLoaders, Cipher: projectCipher, Logger: logger,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation ssh log reader: %w", err),
		)
	}
	mcpRuntime, err := remediationmcp.NewRuntime(remediationmcp.Options{
		Sources: runtimeLoaders, Secrets: runtimeLoaders, Cipher: projectCipher, Logger: logger,
	})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation MCP runtime: %w", err),
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
	webhookModel, err := hookllm.NewAdapter(modelClient)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create webhook classifier adapter: %w", err),
		)
	}
	webhookAnalyzer := hookapplication.NewFingerprintAnalyzerWithTimeout(webhookModel, apiConfig.WebhookAI.NormalizationTimeout)
	webhookFailureReporter, err := hooklogging.NewFailureReporter(logger)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create webhook failure reporter: %w", err),
		)
	}
	tencentDetailClient, err := hooktencentcls.NewClient(hooktencentcls.Options{Logger: logger})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create Tencent CLS detail client: %w", err),
		)
	}
	tencentDetailResolver, err := hooktencentcls.NewIncidentDetailResolver(tencentDetailClient, remediationStore, remediationStore, time.Now)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create Tencent CLS remediation detail resolver: %w", err),
		)
	}
	remediationObserver, err := remediationlogging.NewObserver(logger)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation observer: %w", err),
		)
	}
	// Phase 4：resilient_v1 恢复/生命周期事件的低基数 counters。observer 可选，
	// metrics 只携带枚举 kind/mode/reason code，绝不影响 coordinator 语义。
	remediationMetricsObserver, metricsErr := remediationmetrics.NewObserver(remediationmetrics.Options{
		Meter: telemetryRuntime.MeterProvider().Meter("mendry/backend/remediation"),
	})
	if metricsErr != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation metrics observer: %w", metricsErr),
		)
	}
	// 真实 Git / SSH 日志 / OpenAI 适配器替换占位实现；observer 只接收无凭据语义记录。
	remediationCoordinator := remediationapplication.NewRemediationCoordinatorWithDynamicRuntime(
		remediationStore, repositoryReader, evidenceReader, evidenceReader, modelClient, incidentLookup, repositoryReader,
		remediationStore, remediationStore, remediationObserver, runtimeLoaders, mcpRuntime, remediationStore,
	)
	remediationCoordinator.SetDockerEvidencePort(evidenceReader)
	remediationCoordinator.SetTencentCLSDetailPort(tencentDetailResolver)
	// evidence.read 允许模型按证据 ID 重新读取同 series 持久化证据的有界页面；
	// 无凭据、无 URL/路径参数，run 身份来自执行 catalog。
	remediationCoordinator.SetEvidenceReadPort(remediationStore)
	// SSH/Docker 成功结果先投影为 canonical evidence 并持久化，再进入模型上下文；
	// 没有 writer 时 observed 工具 fail closed，不会暴露未持久化的原始输出。
	remediationCoordinator.SetRuntimeEvidenceWriter(remediationStore)
	// 证据 gate 只通过项目/运行范围受限的 store 解析引用，并持久化最终 assessment。
	remediationCoordinator.SetEvidenceResolver(remediationStore)
	// 首轮 remediation 只加载触发 Observation 及其预运行 evidence；运行时
	// Git/SSH/日志读取仍由 Tool Gateway 按预算和策略驱动。
	remediationCoordinator.SetBootstrapEvidenceLoader(remediationStore)
	// durable working-memory checkpoint store（D2）。resilient_v1 项目的 run 在
	// coordinator 循环中 append/load checkpoint；默认 agentLoopMode=legacy，
	// 既有项目行为完全不变。
	remediationCheckpointStore, checkpointStoreErr := remediationpostgres.NewCheckpointStore(postgresPool)
	if checkpointStoreErr != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation checkpoint store: %w", checkpointStoreErr),
		)
	}
	remediationCoordinator.SetCheckpointStore(remediationCheckpointStore)
	remediationCoordinator.SetResilienceMetricObserver(remediationMetricsObserver)
	remediationFailureReporter, err := remediationlogging.NewFailureReporter(logger)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create remediation failure reporter: %w", err),
		)
	}
	remediationTrigger, err := remediationapplication.NewTriggerWithReporter(remediationStore, remediationCoordinator, remediationFailureReporter)
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
	awsSNSVerifier, err := hookawssns.NewVerifier(hookawssns.Options{})
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout,
			fmt.Errorf("create AWS SNS verifier: %w", err),
		)
	}
	hookService, err := hookapplication.NewService(hookapplication.Options{
		Tokens: projectService, Analyzer: webhookAnalyzer, Observations: observationService,
		Incidents: incidentService, Failures: webhookFailureReporter, Evidence: remediationStore,
		AWSSNS: awsSNSVerifier, Now: time.Now,
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
		// D8：resilient_v1 run 的 GET review 附加 durable checkpoint/recovery
		// projection；legacy run 与无 checkpoint 的 run 不受影响。
		Checkpoints: remediationCheckpointStore,
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

	overviewRepository, err := overviewpostgres.NewRepository(postgresPool)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout, err)
	}
	overviewService, err := overviewapplication.NewService(overviewRepository, projectService)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout, err)
	}
	overviewHandler, err := overviewhttp.NewHandler(overviewService, authHandler)
	if err != nil {
		return finishWithDataClients(processSpan, telemetryRuntime, redisClient, postgresPool, apiConfig.Common.ShutdownTimeout, err)
	}

	mux := http.NewServeMux()
	overviewHandler.Register(mux)
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
