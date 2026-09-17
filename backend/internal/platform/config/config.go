// Package config 负责在进程启动时读取、规范化并验证环境配置。
// 该包的错误只描述字段和约束，不回显可能包含敏感信息的原始值。
package config

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 环境变量 key 由 config 包统一维护，composition root 不应散落字符串字面量。
const (
	EnvironmentKey                  = "MENDRY_ENVIRONMENT"
	LogLevelKey                     = "MENDRY_LOG_LEVEL"
	LogFormatKey                    = "MENDRY_LOG_FORMAT"
	LogFileKey                      = "MENDRY_LOG_FILE"
	ShutdownKey                     = "MENDRY_SHUTDOWN_TIMEOUT"
	HTTPAddressKey                  = "MENDRY_HTTP_ADDR"
	HTTPReadHeaderKey               = "MENDRY_HTTP_READ_HEADER_TIMEOUT"
	HTTPReadKey                     = "MENDRY_HTTP_READ_TIMEOUT"
	HTTPWriteKey                    = "MENDRY_HTTP_WRITE_TIMEOUT"
	HTTPIdleKey                     = "MENDRY_HTTP_IDLE_TIMEOUT"
	HTTPMaxBodyBytesKey             = "MENDRY_HTTP_MAX_BODY_BYTES"
	HTTPCORSAllowedOriginKey        = "MENDRY_HTTP_CORS_ALLOWED_ORIGIN"
	HTTPRequestDebugKey             = "MENDRY_HTTP_REQUEST_DEBUG"
	PublicURLKey                    = "MENDRY_PUBLIC_URL"
	AuthSessionTTLKey               = "MENDRY_AUTH_SESSION_TTL"
	WebhookAITimeoutKey             = "MENDRY_WEBHOOK_AI_TIMEOUT"
	RemediationRecoveryIntervalKey  = "MENDRY_REMEDIATION_RECOVERY_INTERVAL"
	RemediationConcurrencyKey       = "MENDRY_REMEDIATION_CONCURRENCY"
	RemediationModelTimeoutKey      = "MENDRY_REMEDIATION_MODEL_TIMEOUT"
	RemediationArtifactRootKey      = "MENDRY_REMEDIATION_ARTIFACT_ROOT"
	RemediationWorkspaceRootKey     = "MENDRY_REMEDIATION_WORKSPACE_ROOT"
	RemediationDockerCommandKey     = "MENDRY_REMEDIATION_DOCKER_COMMAND"
	RemediationGitCommandKey        = "MENDRY_REMEDIATION_GIT_COMMAND"
	RemediationSSHKnownHostsFileKey = "MENDRY_REMEDIATION_SSH_KNOWN_HOSTS_FILE"
	RemediationGoBuilderImageKey    = "MENDRY_REMEDIATION_GO_BUILDER_IMAGE"
	RemediationNodeBuilderImageKey  = "MENDRY_REMEDIATION_NODE_BUILDER_IMAGE"
	EncryptionKey                   = "MENDRY_ENCRYPTION_KEY"
	BootstrapAdminPasswordKey       = "MENDRY_BOOTSTRAP_ADMIN_PASSWORD"
	PostgresURLKey                  = "MENDRY_POSTGRES_URL"
	PostgresConnectTimeoutKey       = "MENDRY_POSTGRES_CONNECT_TIMEOUT"
	PostgresAcquireTimeoutKey       = "MENDRY_POSTGRES_ACQUIRE_TIMEOUT"
	PostgresStatementTimeoutKey     = "MENDRY_POSTGRES_STATEMENT_TIMEOUT"
	PostgresHealthTimeoutKey        = "MENDRY_POSTGRES_HEALTH_TIMEOUT"
	PostgresMinConnsKey             = "MENDRY_POSTGRES_MIN_CONNS"
	PostgresMaxConnsKey             = "MENDRY_POSTGRES_MAX_CONNS"
	PostgresMaxLifetimeKey          = "MENDRY_POSTGRES_MAX_CONN_LIFETIME"
	PostgresMaxIdleTimeKey          = "MENDRY_POSTGRES_MAX_CONN_IDLE_TIME"
	PostgresHealthPeriodKey         = "MENDRY_POSTGRES_HEALTH_CHECK_PERIOD"
	PostgresSlowQueryKey            = "MENDRY_POSTGRES_SLOW_QUERY_THRESHOLD"
	PostgresQueryDebugKey           = "MENDRY_POSTGRES_QUERY_DEBUG"
	PostgresMigrationLockKey        = "MENDRY_POSTGRES_MIGRATION_LOCK_TIMEOUT"
	RedisURLKey                     = "MENDRY_REDIS_URL"
	RedisDialTimeoutKey             = "MENDRY_REDIS_DIAL_TIMEOUT"
	RedisReadTimeoutKey             = "MENDRY_REDIS_READ_TIMEOUT"
	RedisWriteTimeoutKey            = "MENDRY_REDIS_WRITE_TIMEOUT"
	RedisPoolTimeoutKey             = "MENDRY_REDIS_POOL_TIMEOUT"
	RedisHealthTimeoutKey           = "MENDRY_REDIS_HEALTH_TIMEOUT"
	RedisPoolSizeKey                = "MENDRY_REDIS_POOL_SIZE"
	RedisMinIdleConnsKey            = "MENDRY_REDIS_MIN_IDLE_CONNS"
	RedisMaxRetriesKey              = "MENDRY_REDIS_MAX_RETRIES"
	RedisMinRetryBackoffKey         = "MENDRY_REDIS_MIN_RETRY_BACKOFF"
	RedisMaxRetryBackoffKey         = "MENDRY_REDIS_MAX_RETRY_BACKOFF"
	RedisMaxIdleTimeKey             = "MENDRY_REDIS_MAX_CONN_IDLE_TIME"
	RedisMaxLifetimeKey             = "MENDRY_REDIS_MAX_CONN_LIFETIME"
	RedisDatabaseKey                = "MENDRY_REDIS_DB"
	RedisSlowCommandKey             = "MENDRY_REDIS_SLOW_COMMAND_THRESHOLD"
)

// Lookup 抽象环境变量读取，使配置测试无需修改进程级 environment。
type Lookup func(string) (string, bool)

func legacyEnvironmentKey(key string) string {
	if strings.HasPrefix(key, "MENDRY_") {
		return "FIXTHE_" + strings.TrimPrefix(key, "MENDRY_")
	}
	return key
}

// lookupValue 按新配置、旧配置的顺序读取，并保留显式空值的优先级。
func lookupValue(lookup Lookup, key string) (string, bool) {
	if value, ok := lookup(key); ok {
		return value, true
	}
	legacyKey := legacyEnvironmentKey(key)
	if legacyKey != key {
		return lookup(legacyKey)
	}
	return "", false
}

// Common 包含所有后端进程共享的运行参数。
type Common struct {
	Environment     string
	LogLevel        string
	LogFormat       string
	LogFile         string
	ShutdownTimeout time.Duration
}

// HTTP 包含 API server 的监听地址、超时边界和入站调试开关。
type HTTP struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxBodyBytes      int64
	CORSAllowedOrigin string
	// RequestDebug 为 true 时，AccessLog 会把完整请求/响应写入
	// http.request.completed；默认关闭，且不得把这些内容复制到 span 或 metric。
	RequestDebug bool
}

// API 聚合 API 进程启动所需的全部已验证配置。
type API struct {
	Common      Common
	HTTP        HTTP
	Auth        Auth
	WebhookAI   WebhookAI
	Remediation Remediation
	Encryption  Encryption
	// PublicURL 是派生公开 webhook 入站地址的部署级基址。
	PublicURL  string
	PostgreSQL PostgreSQL
	Redis      Redis
}

// WebhookAI 包含 webhook AI 归一化调用的资源边界。
type WebhookAI struct {
	NormalizationTimeout time.Duration
}

// Remediation 包含自动修复模型调用的部署级资源边界。
type Remediation struct {
	RecoveryInterval  time.Duration
	Concurrency       int
	ModelTurnTimeout  time.Duration
	ArtifactRoot      string
	WorkspaceRoot     string
	DockerCommand     string
	GitCommand        string
	SSHKnownHostsFile string
	GoBuilderImage    string
	NodeBuilderImage  string
}

// Auth 包含 API 服务端 Session 的绝对生命周期。
type Auth struct {
	SessionTTL time.Duration
}

// Encryption 包含项目 credential 使用的部署级 AES-256 key。
// Key 只在进程内存中使用，不得进入日志或错误。
type Encryption struct {
	Key []byte
}

// Migrate 聚合显式 migration 命令所需的配置。
type Migrate struct {
	Common               Common
	PostgreSQL           PostgreSQL
	MigrationLockTimeout time.Duration
}

// BootstrapAdmin 聚合一次性管理员命令所需配置，不加载 Redis 或 HTTP。
type BootstrapAdmin struct {
	Common     Common
	PostgreSQL PostgreSQL
}

// Seed 聚合显式开发数据命令所需配置，不加载 Redis、HTTP 或加密密钥。
type Seed struct {
	Common     Common
	PostgreSQL PostgreSQL
}

// PostgreSQL 声明每个进程独占 pgxpool 的连接和资源边界。
// URL 可能携带 credential，禁止进入日志、错误、span 或 metric。
type PostgreSQL struct {
	URL                string
	ConnectTimeout     time.Duration
	AcquireTimeout     time.Duration
	StatementTimeout   time.Duration
	HealthTimeout      time.Duration
	MinConnections     int32
	MaxConnections     int32
	MaxConnLifetime    time.Duration
	MaxConnIdleTime    time.Duration
	HealthCheckPeriod  time.Duration
	SlowQueryThreshold time.Duration
	QueryDebug         bool
}

// Redis 声明 API session store 的 client 和全部资源边界。
// URL 可能携带 credential；API 要求 Enabled=true，独立 loader 仍保留 disabled 值供测试。
type Redis struct {
	Enabled              bool
	URL                  string
	DialTimeout          time.Duration
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	PoolTimeout          time.Duration
	HealthTimeout        time.Duration
	PoolSize             int
	MinIdleConnections   int
	MaxRetries           int
	MinRetryBackoff      time.Duration
	MaxRetryBackoff      time.Duration
	MaxConnIdleTime      time.Duration
	MaxConnLifetime      time.Duration
	Database             int
	SlowCommandThreshold time.Duration
}

// LoadAPI 从 lookup 加载并验证 API 配置。
// 返回错误只会包含环境变量 key 和约束，不包含调用方提供的原始值。
func LoadAPI(lookup Lookup) (API, error) {
	common, err := LoadCommon(lookup)
	if err != nil {
		return API{}, err
	}

	httpConfig, err := loadHTTP(lookup)
	if err != nil {
		return API{}, err
	}
	postgresConfig, err := LoadPostgreSQL(lookup)
	if err != nil {
		return API{}, err
	}
	redisConfig, err := LoadRedis(lookup)
	if err != nil {
		return API{}, err
	}
	if !redisConfig.Enabled {
		return API{}, fieldError(RedisURLKey, "is required for API sessions")
	}
	sessionTTL, err := durationValue(lookup, AuthSessionTTLKey, 24*time.Hour, 5*time.Minute, 30*24*time.Hour)
	if err != nil {
		return API{}, err
	}
	webhookAITimeout, err := durationValue(lookup, WebhookAITimeoutKey, 60*time.Second, 100*time.Millisecond, 10*time.Minute)
	if err != nil {
		return API{}, err
	}
	remediationModelTimeout, err := durationValue(lookup, RemediationModelTimeoutKey, 5*time.Minute, 30*time.Second, 20*time.Minute)
	if err != nil {
		return API{}, err
	}
	remediationStorage, err := loadRemediationStorage(lookup, remediationModelTimeout)
	if err != nil {
		return API{}, err
	}

	encryptionKey, err := encryptionKeyValue(lookup)
	if err != nil {
		return API{}, err
	}
	publicURL, err := publicURLValue(lookup)
	if err != nil {
		return API{}, err
	}

	return API{
		Common: common, HTTP: httpConfig, Auth: Auth{SessionTTL: sessionTTL},
		WebhookAI:   WebhookAI{NormalizationTimeout: webhookAITimeout},
		Remediation: remediationStorage,
		Encryption:  Encryption{Key: encryptionKey}, PublicURL: publicURL,
		PostgreSQL: postgresConfig, Redis: redisConfig,
	}, nil
}

func loadRemediationStorage(lookup Lookup, modelTimeout time.Duration) (Remediation, error) {
	recoveryInterval, err := durationValue(lookup, RemediationRecoveryIntervalKey, 15*time.Second, time.Second, 5*time.Minute)
	if err != nil {
		return Remediation{}, err
	}
	concurrency, err := integerValue(lookup, RemediationConcurrencyKey, 4, 1, 32)
	if err != nil {
		return Remediation{}, err
	}
	artifactRoot := stringValue(lookup, RemediationArtifactRootKey, "")
	workspaceRoot := stringValue(lookup, RemediationWorkspaceRootKey, "")
	if artifactRoot == "" && workspaceRoot == "" {
		artifactRoot, err = filepath.Abs(filepath.Join(".var", "remediation", "artifacts"))
		if err != nil {
			return Remediation{}, fieldError(RemediationArtifactRootKey, "cannot resolve default remediation storage path")
		}
		workspaceRoot, err = filepath.Abs(filepath.Join(".var", "remediation", "workspaces"))
		if err != nil {
			return Remediation{}, fieldError(RemediationWorkspaceRootKey, "cannot resolve default remediation storage path")
		}
	}
	for key, value := range map[string]string{
		RemediationArtifactRootKey: artifactRoot, RemediationWorkspaceRootKey: workspaceRoot,
	} {
		if strings.ContainsAny(value, "\x00\r\n") {
			return Remediation{}, fieldError(key, "must be a valid absolute path")
		}
	}
	if (artifactRoot == "") != (workspaceRoot == "") {
		if artifactRoot == "" {
			return Remediation{}, fieldError(RemediationArtifactRootKey, "must be configured together with the workspace root")
		}
		return Remediation{}, fieldError(RemediationWorkspaceRootKey, "must be configured together with the artifact root")
	}
	if artifactRoot != "" {
		if !filepath.IsAbs(artifactRoot) || !filepath.IsAbs(workspaceRoot) {
			return Remediation{}, fieldError(RemediationArtifactRootKey, "artifact and workspace roots must be absolute paths")
		}
		artifactRoot = filepath.Clean(artifactRoot)
		workspaceRoot = filepath.Clean(workspaceRoot)
		if pathsOverlap(artifactRoot, workspaceRoot) {
			return Remediation{}, fieldError(RemediationWorkspaceRootKey, "must be separate from the artifact root")
		}
	}
	dockerCommand := stringValue(lookup, RemediationDockerCommandKey, "docker")
	gitCommand := stringValue(lookup, RemediationGitCommandKey, "git")
	knownHosts := stringValue(lookup, RemediationSSHKnownHostsFileKey, "")
	goBuilderImage := stringValue(lookup, RemediationGoBuilderImageKey, "")
	nodeBuilderImage := stringValue(lookup, RemediationNodeBuilderImageKey, "")
	for key, value := range map[string]string{
		RemediationDockerCommandKey: dockerCommand, RemediationGitCommandKey: gitCommand,
	} {
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return Remediation{}, fieldError(key, "must be a valid command")
		}
	}
	if strings.ContainsAny(knownHosts, "\x00\r\n") {
		return Remediation{}, fieldError(RemediationSSHKnownHostsFileKey, "must be a valid absolute path")
	}
	if knownHosts != "" && !filepath.IsAbs(knownHosts) {
		return Remediation{}, fieldError(RemediationSSHKnownHostsFileKey, "must be an absolute path")
	}
	for key, image := range map[string]string{
		RemediationGoBuilderImageKey: goBuilderImage, RemediationNodeBuilderImageKey: nodeBuilderImage,
	} {
		if image != "" && !immutableContainerImage(image) {
			return Remediation{}, fieldError(key, "must be an immutable sha256 image reference")
		}
	}
	return Remediation{
		RecoveryInterval: recoveryInterval, Concurrency: int(concurrency),
		ModelTurnTimeout: modelTimeout, ArtifactRoot: artifactRoot, WorkspaceRoot: workspaceRoot,
		DockerCommand: dockerCommand, GitCommand: gitCommand,
		SSHKnownHostsFile: knownHosts, GoBuilderImage: goBuilderImage, NodeBuilderImage: nodeBuilderImage,
	}, nil
}

func immutableContainerImage(value string) bool {
	if strings.HasPrefix(value, "sha256:") {
		return len(value) == 71 && allLowerHex(value[len("sha256:"):])
	}
	separator := strings.LastIndex(value, "@sha256:")
	return separator > 0 && !strings.ContainsAny(value[:separator], " \t\r\n@") &&
		len(value[separator+1:]) == 71 && allLowerHex(value[separator+len("@sha256:"):])
}

func allLowerHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func pathsOverlap(left, right string) bool {
	leftRelative, leftErr := filepath.Rel(left, right)
	rightRelative, rightErr := filepath.Rel(right, left)
	return leftErr != nil || rightErr != nil || leftRelative == "." || rightRelative == "." ||
		(leftRelative != ".." && !strings.HasPrefix(leftRelative, ".."+string(filepath.Separator))) ||
		(rightRelative != ".." && !strings.HasPrefix(rightRelative, ".."+string(filepath.Separator)))
}

func publicURLValue(lookup Lookup) (string, error) {
	value := stringValue(lookup, PublicURLKey, "")
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", fieldError(PublicURLKey, "is required and must be an absolute http or https URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Path == "/" || strings.HasSuffix(parsed.Path, "/") {
		return "", fieldError(PublicURLKey, "must be an absolute http or https URL without credentials, query, fragment, or a trailing slash")
	}
	return value, nil
}

func encryptionKeyValue(lookup Lookup) ([]byte, error) {
	encoded := stringValue(lookup, EncryptionKey, "")
	if encoded == "" || strings.ContainsAny(encoded, "\x00\r\n") {
		return nil, fieldError(EncryptionKey, "is required and must be base64-encoded")
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, fieldError(EncryptionKey, "must decode to exactly 32 bytes")
	}
	return key, nil
}

// LoadMigrate 从 lookup 加载并验证显式 migration 命令配置。
func LoadMigrate(lookup Lookup) (Migrate, error) {
	common, err := LoadCommon(lookup)
	if err != nil {
		return Migrate{}, err
	}
	postgresConfig, err := LoadPostgreSQL(lookup)
	if err != nil {
		return Migrate{}, err
	}
	lockTimeout, err := durationValue(lookup, PostgresMigrationLockKey, 30*time.Second, time.Second, 10*time.Minute)
	if err != nil {
		return Migrate{}, err
	}
	return Migrate{
		Common:               common,
		PostgreSQL:           postgresConfig,
		MigrationLockTimeout: lockTimeout,
	}, nil
}

// LoadBootstrapAdmin 加载一次性管理员命令配置；密码由 cmd 边界单独读取，避免进入日志配置。
func LoadBootstrapAdmin(lookup Lookup) (BootstrapAdmin, error) {
	common, err := LoadCommon(lookup)
	if err != nil {
		return BootstrapAdmin{}, err
	}
	postgresConfig, err := LoadPostgreSQL(lookup)
	if err != nil {
		return BootstrapAdmin{}, err
	}
	return BootstrapAdmin{Common: common, PostgreSQL: postgresConfig}, nil
}

// LoadSeed 加载显式开发数据命令配置。
func LoadSeed(lookup Lookup) (Seed, error) {
	common, err := LoadCommon(lookup)
	if err != nil {
		return Seed{}, err
	}
	postgresConfig, err := LoadPostgreSQL(lookup)
	if err != nil {
		return Seed{}, err
	}
	return Seed{Common: common, PostgreSQL: postgresConfig}, nil
}

// LoadCommon 从 lookup 加载所有进程共用的配置并应用安全默认值。
func LoadCommon(lookup Lookup) (Common, error) {
	if lookup == nil {
		return Common{}, fmt.Errorf("configuration lookup is required")
	}

	environment, err := enumValue(lookup, EnvironmentKey, "development", map[string]struct{}{
		"development": {},
		"test":        {},
		"staging":     {},
		"production":  {},
	})
	if err != nil {
		return Common{}, err
	}

	logLevel, err := enumValue(lookup, LogLevelKey, "info", map[string]struct{}{
		"debug": {},
		"info":  {},
		"warn":  {},
		"error": {},
	})
	if err != nil {
		return Common{}, err
	}
	defaultLogFormat := "json"
	if environment == "development" {
		defaultLogFormat = "console"
	}
	logFormat, err := enumValue(lookup, LogFormatKey, defaultLogFormat, map[string]struct{}{
		"console": {},
		"json":    {},
	})
	if err != nil {
		return Common{}, err
	}
	logFile := stringValue(lookup, LogFileKey, "")
	if len(logFile) > 4096 || strings.ContainsAny(logFile, "\x00\r\n") {
		return Common{}, fieldError(LogFileKey, "must be empty or a single-line path up to 4096 bytes")
	}

	shutdownTimeout, err := durationValue(lookup, ShutdownKey, 15*time.Second, time.Second, 2*time.Minute)
	if err != nil {
		return Common{}, err
	}

	return Common{
		Environment:     environment,
		LogLevel:        logLevel,
		LogFormat:       logFormat,
		LogFile:         logFile,
		ShutdownTimeout: shutdownTimeout,
	}, nil
}

// LoadPostgreSQL 加载外部 PostgreSQL endpoint 和有界 pgxpool 参数。
// 连接字符串没有不安全的本地默认值，所有进程都必须显式提供。
func LoadPostgreSQL(lookup Lookup) (PostgreSQL, error) {
	if lookup == nil {
		return PostgreSQL{}, fmt.Errorf("configuration lookup is required")
	}

	connectionURL := stringValue(lookup, PostgresURLKey, "")
	if connectionURL == "" || strings.ContainsAny(connectionURL, "\x00\r\n") {
		return PostgreSQL{}, fieldError(PostgresURLKey, "is required and must be a single-line pgx connection string")
	}

	connectTimeout, err := durationValue(lookup, PostgresConnectTimeoutKey, 5*time.Second, 100*time.Millisecond, time.Minute)
	if err != nil {
		return PostgreSQL{}, err
	}
	acquireTimeout, err := durationValue(lookup, PostgresAcquireTimeoutKey, 2*time.Second, 100*time.Millisecond, time.Minute)
	if err != nil {
		return PostgreSQL{}, err
	}
	statementTimeout, err := durationValue(lookup, PostgresStatementTimeoutKey, 30*time.Second, 100*time.Millisecond, 10*time.Minute)
	if err != nil {
		return PostgreSQL{}, err
	}
	healthTimeout, err := durationValue(lookup, PostgresHealthTimeoutKey, 2*time.Second, 100*time.Millisecond, 30*time.Second)
	if err != nil {
		return PostgreSQL{}, err
	}
	minConnections, err := integerValue(lookup, PostgresMinConnsKey, 1, 0, 100)
	if err != nil {
		return PostgreSQL{}, err
	}
	maxConnections, err := integerValue(lookup, PostgresMaxConnsKey, 10, 1, 100)
	if err != nil {
		return PostgreSQL{}, err
	}
	if minConnections > maxConnections {
		return PostgreSQL{}, fieldError(PostgresMinConnsKey, "must not exceed "+PostgresMaxConnsKey)
	}
	maxLifetime, err := durationValue(lookup, PostgresMaxLifetimeKey, 30*time.Minute, time.Minute, 24*time.Hour)
	if err != nil {
		return PostgreSQL{}, err
	}
	maxIdleTime, err := durationValue(lookup, PostgresMaxIdleTimeKey, 5*time.Minute, 30*time.Second, time.Hour)
	if err != nil {
		return PostgreSQL{}, err
	}
	healthPeriod, err := durationValue(lookup, PostgresHealthPeriodKey, 30*time.Second, time.Second, 10*time.Minute)
	if err != nil {
		return PostgreSQL{}, err
	}
	slowQueryThreshold, err := durationValue(lookup, PostgresSlowQueryKey, 500*time.Millisecond, 10*time.Millisecond, time.Minute)
	if err != nil {
		return PostgreSQL{}, err
	}
	queryDebugValue, err := enumValue(lookup, PostgresQueryDebugKey, "false", map[string]struct{}{
		"true":  {},
		"false": {},
	})
	if err != nil {
		return PostgreSQL{}, err
	}

	return PostgreSQL{
		URL:                connectionURL,
		ConnectTimeout:     connectTimeout,
		AcquireTimeout:     acquireTimeout,
		StatementTimeout:   statementTimeout,
		HealthTimeout:      healthTimeout,
		MinConnections:     int32(minConnections),
		MaxConnections:     int32(maxConnections),
		MaxConnLifetime:    maxLifetime,
		MaxConnIdleTime:    maxIdleTime,
		HealthCheckPeriod:  healthPeriod,
		SlowQueryThreshold: slowQueryThreshold,
		QueryDebug:         queryDebugValue == "true",
	}, nil
}

// LoadRedis 加载可选 Redis client 配置；URL 为空时不启用也不解析其余设置。
// URL 的 query 被禁止，避免连接串中的隐式参数覆盖 typed 配置边界。
func LoadRedis(lookup Lookup) (Redis, error) {
	if lookup == nil {
		return Redis{}, fmt.Errorf("configuration lookup is required")
	}

	connectionURL := stringValue(lookup, RedisURLKey, "")
	if connectionURL == "" {
		return Redis{}, nil
	}
	if strings.ContainsAny(connectionURL, "\x00\r\n") {
		return Redis{}, fieldError(RedisURLKey, "must be a single-line redis or rediss URL")
	}
	parsedURL, err := url.Parse(connectionURL)
	if err != nil || (parsedURL.Scheme != "redis" && parsedURL.Scheme != "rediss") ||
		parsedURL.Host == "" || parsedURL.RawQuery != "" || parsedURL.Fragment != "" ||
		(parsedURL.Path != "" && parsedURL.Path != "/") {
		return Redis{}, fieldError(RedisURLKey, "must be a redis or rediss URL with a host and no database, query, or fragment")
	}

	dialTimeout, err := durationValue(lookup, RedisDialTimeoutKey, 5*time.Second, 100*time.Millisecond, time.Minute)
	if err != nil {
		return Redis{}, err
	}
	readTimeout, err := durationValue(lookup, RedisReadTimeoutKey, 3*time.Second, 100*time.Millisecond, time.Minute)
	if err != nil {
		return Redis{}, err
	}
	writeTimeout, err := durationValue(lookup, RedisWriteTimeoutKey, 3*time.Second, 100*time.Millisecond, time.Minute)
	if err != nil {
		return Redis{}, err
	}
	poolTimeout, err := durationValue(lookup, RedisPoolTimeoutKey, 2*time.Second, 100*time.Millisecond, time.Minute)
	if err != nil {
		return Redis{}, err
	}
	healthTimeout, err := durationValue(lookup, RedisHealthTimeoutKey, 2*time.Second, 100*time.Millisecond, 30*time.Second)
	if err != nil {
		return Redis{}, err
	}
	poolSize, err := integerValue(lookup, RedisPoolSizeKey, 10, 1, 1000)
	if err != nil {
		return Redis{}, err
	}
	minIdleConnections, err := integerValue(lookup, RedisMinIdleConnsKey, 1, 0, 1000)
	if err != nil {
		return Redis{}, err
	}
	if minIdleConnections > poolSize {
		return Redis{}, fieldError(RedisMinIdleConnsKey, "must not exceed "+RedisPoolSizeKey)
	}
	maxRetries, err := integerValue(lookup, RedisMaxRetriesKey, 2, 0, 5)
	if err != nil {
		return Redis{}, err
	}
	minRetryBackoff, err := durationValue(lookup, RedisMinRetryBackoffKey, 10*time.Millisecond, time.Millisecond, time.Second)
	if err != nil {
		return Redis{}, err
	}
	maxRetryBackoff, err := durationValue(lookup, RedisMaxRetryBackoffKey, 500*time.Millisecond, time.Millisecond, 5*time.Second)
	if err != nil {
		return Redis{}, err
	}
	if minRetryBackoff > maxRetryBackoff {
		return Redis{}, fieldError(RedisMinRetryBackoffKey, "must not exceed "+RedisMaxRetryBackoffKey)
	}
	maxIdleTime, err := durationValue(lookup, RedisMaxIdleTimeKey, 5*time.Minute, 30*time.Second, time.Hour)
	if err != nil {
		return Redis{}, err
	}
	maxLifetime, err := durationValue(lookup, RedisMaxLifetimeKey, 30*time.Minute, time.Minute, 24*time.Hour)
	if err != nil {
		return Redis{}, err
	}
	database, err := integerValue(lookup, RedisDatabaseKey, 0, 0, 255)
	if err != nil {
		return Redis{}, err
	}
	slowCommandThreshold, err := durationValue(lookup, RedisSlowCommandKey, 250*time.Millisecond, time.Millisecond, time.Minute)
	if err != nil {
		return Redis{}, err
	}

	return Redis{
		Enabled:              true,
		URL:                  connectionURL,
		DialTimeout:          dialTimeout,
		ReadTimeout:          readTimeout,
		WriteTimeout:         writeTimeout,
		PoolTimeout:          poolTimeout,
		HealthTimeout:        healthTimeout,
		PoolSize:             int(poolSize),
		MinIdleConnections:   int(minIdleConnections),
		MaxRetries:           int(maxRetries),
		MinRetryBackoff:      minRetryBackoff,
		MaxRetryBackoff:      maxRetryBackoff,
		MaxConnIdleTime:      maxIdleTime,
		MaxConnLifetime:      maxLifetime,
		Database:             int(database),
		SlowCommandThreshold: slowCommandThreshold,
	}, nil
}

func loadHTTP(lookup Lookup) (HTTP, error) {
	address := stringValue(lookup, HTTPAddressKey, "127.0.0.1:8080")
	if err := validateAddress(address); err != nil {
		return HTTP{}, fieldError(HTTPAddressKey, "must be a valid host:port address")
	}

	readHeaderTimeout, err := durationValue(lookup, HTTPReadHeaderKey, 5*time.Second, 100*time.Millisecond, time.Minute)
	if err != nil {
		return HTTP{}, err
	}
	readTimeout, err := durationValue(lookup, HTTPReadKey, 15*time.Second, 100*time.Millisecond, 5*time.Minute)
	if err != nil {
		return HTTP{}, err
	}
	writeTimeout, err := durationValue(lookup, HTTPWriteKey, 30*time.Second, 100*time.Millisecond, 5*time.Minute)
	if err != nil {
		return HTTP{}, err
	}
	idleTimeout, err := durationValue(lookup, HTTPIdleKey, 60*time.Second, time.Second, 10*time.Minute)
	if err != nil {
		return HTTP{}, err
	}
	maxBodyBytes, err := integerValue(lookup, HTTPMaxBodyBytesKey, 1024*1024, 1024, 10*1024*1024)
	if err != nil {
		return HTTP{}, err
	}
	corsAllowedOrigin := stringValue(lookup, HTTPCORSAllowedOriginKey, "")
	if corsAllowedOrigin != "" {
		parsedOrigin, parseErr := url.Parse(corsAllowedOrigin)
		if parseErr != nil || (parsedOrigin.Scheme != "http" && parsedOrigin.Scheme != "https") ||
			parsedOrigin.Host == "" || parsedOrigin.User != nil || parsedOrigin.Path != "" ||
			parsedOrigin.RawQuery != "" || parsedOrigin.Fragment != "" {
			return HTTP{}, fieldError(HTTPCORSAllowedOriginKey, "must be an http or https origin without credentials, path, query, or fragment")
		}
	}
	requestDebugValue, err := enumValue(lookup, HTTPRequestDebugKey, "false", map[string]struct{}{
		"true":  {},
		"false": {},
	})
	if err != nil {
		return HTTP{}, err
	}

	return HTTP{
		Address:           address,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxBodyBytes:      maxBodyBytes,
		CORSAllowedOrigin: corsAllowedOrigin,
		RequestDebug:      requestDebugValue == "true",
	}, nil
}

func stringValue(lookup Lookup, key, fallback string) string {
	value, ok := lookupValue(lookup, key)
	if !ok {
		return fallback
	}
	return strings.TrimSpace(value)
}

func enumValue(lookup Lookup, key, fallback string, allowed map[string]struct{}) (string, error) {
	value := strings.ToLower(stringValue(lookup, key, fallback))
	if _, ok := allowed[value]; !ok {
		return "", fieldError(key, "contains an unsupported value")
	}
	return value, nil
}

func durationValue(lookup Lookup, key string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	raw, ok := lookupValue(lookup, key)
	if !ok {
		return fallback, nil
	}

	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || value < minimum || value > maximum {
		// 不拼接 raw，避免 secret 或生产 endpoint 通过配置诊断进入日志。
		return 0, fieldError(key, fmt.Sprintf("must be between %s and %s", minimum, maximum))
	}
	return value, nil
}

func integerValue(lookup Lookup, key string, fallback, minimum, maximum int64) (int64, error) {
	raw, ok := lookupValue(lookup, key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32)
	if err != nil || value < minimum || value > maximum {
		return 0, fieldError(key, fmt.Sprintf("must be an integer between %d and %d", minimum, maximum))
	}
	return value, nil
}

func validateAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}

	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return fmt.Errorf("invalid port")
	}
	return nil
}

func fieldError(field, reason string) error {
	return fmt.Errorf("configuration %s %s", field, reason)
}
