package redis

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sync"
	"time"

	"mendry/backend/internal/platform/config"
	"mendry/backend/internal/platform/observability"

	redisclient "github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var clientNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// ClientOptions 声明 Redis 的已验证配置和观测依赖。
type ClientOptions struct {
	Configuration config.Redis
	Application   string
	Logger        *slog.Logger
	Tracer        trace.Tracer
	MeterProvider metric.MeterProvider
}

// Client 包装进程独占的 go-redis client，并统一 startup health、观测和关闭行为。
// 嵌入的 command API 只应由 outbound adapter 使用，不得穿透到 application/domain。
type Client struct {
	*redisclient.Client
	healthTimeout time.Duration
	logger        *slog.Logger
	metrics       *poolMetrics
	closeOnce     sync.Once
	closeDone     chan struct{}
	closeError    error
}

// Open 创建 bounded Redis client，并在返回前完成一次 PING。
// parse、connect 和 server failure 均使用固定安全错误，禁止泄漏 URL 或 Redis message。
func Open(ctx context.Context, options ClientOptions) (*Client, error) {
	redisOptions, err := buildClientOptions(options.Configuration)
	if err != nil {
		return nil, err
	}
	if options.Logger == nil || options.Tracer == nil || options.MeterProvider == nil {
		return nil, fmt.Errorf("Redis observability dependencies are required")
	}
	if !clientNamePattern.MatchString(options.Application) {
		return nil, fmt.Errorf("Redis client name is invalid")
	}
	redisOptions.ClientName = options.Application
	meter := options.MeterProvider.Meter("mendry/backend/redis")
	hook, err := NewCommandHook(options.Logger, options.Tracer, meter, options.Configuration.SlowCommandThreshold)
	if err != nil {
		return nil, err
	}
	rawClient := redisclient.NewClient(redisOptions)
	rawClient.AddHook(hook)
	metrics, err := newPoolMetrics(meter, rawClient)
	if err != nil {
		_ = rawClient.Close()
		return nil, err
	}
	client := &Client{
		Client:        rawClient,
		healthTimeout: options.Configuration.HealthTimeout,
		logger:        options.Logger,
		metrics:       metrics,
		closeDone:     make(chan struct{}),
	}
	if err := client.Health(ctx); err != nil {
		// startup 失败仍要保留注销 metrics 与关闭 client 的失败原因，便于调用方
		// 使用 errors.Is/As 诊断；safeError 对外仍只暴露固定文本。
		cleanupError := errors.Join(metrics.close(), rawClient.Close())
		return nil, newSafeError("verify Redis startup health", errors.Join(err, cleanupError))
	}
	client.logState(context.WithoutCancel(ctx), "ready")
	return client, nil
}

func buildClientOptions(configuration config.Redis) (*redisclient.Options, error) {
	if !configuration.Enabled || configuration.URL == "" || configuration.DialTimeout <= 0 ||
		configuration.DialTimeout > time.Minute ||
		configuration.ReadTimeout <= 0 || configuration.WriteTimeout <= 0 || configuration.PoolTimeout <= 0 ||
		configuration.ReadTimeout > time.Minute || configuration.WriteTimeout > time.Minute ||
		configuration.PoolTimeout > time.Minute || configuration.HealthTimeout <= 0 ||
		configuration.HealthTimeout > 30*time.Second || configuration.PoolSize <= 0 || configuration.PoolSize > 1000 ||
		configuration.MinIdleConnections < 0 ||
		configuration.MinIdleConnections > configuration.PoolSize || configuration.MaxRetries < 0 ||
		configuration.MaxRetries > 5 || configuration.MinRetryBackoff <= 0 ||
		configuration.MinRetryBackoff > time.Second || configuration.MaxRetryBackoff < configuration.MinRetryBackoff ||
		configuration.MaxRetryBackoff > 5*time.Second || configuration.MaxConnIdleTime < 30*time.Second ||
		configuration.MaxConnIdleTime > time.Hour || configuration.MaxConnLifetime < time.Minute ||
		configuration.MaxConnLifetime > 24*time.Hour || configuration.Database < 0 || configuration.Database > 255 ||
		configuration.SlowCommandThreshold < time.Millisecond || configuration.SlowCommandThreshold > time.Minute {
		return nil, fmt.Errorf("Redis configuration is invalid")
	}
	parsedURL, err := url.Parse(configuration.URL)
	if err != nil || (parsedURL.Scheme != "redis" && parsedURL.Scheme != "rediss") || parsedURL.Host == "" ||
		parsedURL.RawQuery != "" || parsedURL.Fragment != "" || (parsedURL.Path != "" && parsedURL.Path != "/") {
		return nil, newSafeError("parse Redis configuration", err)
	}

	redisOptions, err := redisclient.ParseURL(configuration.URL)
	if err != nil {
		return nil, newSafeError("parse Redis configuration", err)
	}
	redisOptions.DB = configuration.Database
	redisOptions.DialTimeout = configuration.DialTimeout
	redisOptions.ReadTimeout = configuration.ReadTimeout
	redisOptions.WriteTimeout = configuration.WriteTimeout
	redisOptions.PoolTimeout = configuration.PoolTimeout
	redisOptions.PoolSize = configuration.PoolSize
	redisOptions.MaxActiveConns = configuration.PoolSize
	redisOptions.MaxConcurrentDials = configuration.PoolSize
	redisOptions.MinIdleConns = configuration.MinIdleConnections
	redisOptions.MaxIdleConns = configuration.PoolSize
	redisOptions.ConnMaxIdleTime = configuration.MaxConnIdleTime
	redisOptions.ConnMaxLifetime = configuration.MaxConnLifetime
	redisOptions.MinRetryBackoff = configuration.MinRetryBackoff
	redisOptions.MaxRetryBackoff = configuration.MaxRetryBackoff
	redisOptions.ContextTimeoutEnabled = true
	// 当前 contract 是 standalone disposable-state client，不接管 Redis Enterprise
	// maintenance handoff。显式关闭可避免隐式 endpoint 切换及库级 logger 泄漏地址。
	redisOptions.MaintNotificationsConfig = &maintnotifications.Config{Mode: maintnotifications.ModeDisabled}
	// go-redis 使用 -1 而不是 0 表示禁用 retry；typed 配置的 0 保持直观语义。
	redisOptions.MaxRetries = configuration.MaxRetries
	if configuration.MaxRetries == 0 {
		redisOptions.MaxRetries = -1
	}
	if redisOptions.TLSConfig != nil {
		redisOptions.TLSConfig.MinVersion = tls.VersionTLS12
	}
	return redisOptions, nil
}

// Health 在独立 health timeout 内执行 PING，不读取或写入任何业务 key。
func (c *Client) Health(ctx context.Context) error {
	healthContext, cancel := boundedContext(ctx, c.healthTimeout)
	defer cancel()
	if err := c.Ping(healthContext).Err(); err != nil {
		return newSafeError("ping Redis", err)
	}
	return nil
}

// Close 在 caller 的 shutdown stage deadline 内关闭 client 并注销 pool metrics。
// go-redis Close 没有 context；超时只允许进程继续后续清理，后台关闭仍会完成。
func (c *Client) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		logContext := context.WithoutCancel(ctx)
		go func() {
			c.closeError = c.Client.Close()
			metricError := c.metrics.close()
			c.closeError = errors.Join(c.closeError, metricError)
			// PoolStats 在 go-redis 关闭后仍提供最终 counters。日志放在 closeOnce
			// 内，确保重复或并发 Close 只产生一个 closed lifecycle event。
			c.logState(logContext, "closed")
			close(c.closeDone)
		}()
	})
	select {
	case <-c.closeDone:
		if c.closeError != nil {
			return newSafeError("close Redis client", c.closeError)
		}
		return nil
	case <-ctx.Done():
		return newSafeError("close Redis client", ctx.Err())
	}
}

func (c *Client) logState(ctx context.Context, health string) {
	stats := c.PoolStats()
	observability.Log(ctx, c.logger, slog.LevelInfo, observability.EventRedisPoolState, "pool state changed",
		slog.String(observability.FieldComponent, "redis"),
		slog.String(observability.FieldHealth, health),
		slog.Int64(observability.FieldPoolIdle, int64(stats.IdleConns)),
		slog.Int64(observability.FieldPoolTotal, int64(stats.TotalConns)),
	)
}

func boundedContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}
