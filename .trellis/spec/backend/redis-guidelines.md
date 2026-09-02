# Redis Guidelines

> Established API session-store lifecycle, observability, and isolation contracts.

---

## Scenario: Disposable Operational State

### 1. Scope / Trigger

Use this contract when changing the API Redis client or implementing the bounded
session adapter.

- Redis uses `go-redis/v9` and one API-owned standalone client.
- `internal/platform/redis` owns connection construction, pool bounds, safe
  instrumentation, health checks, metrics, and close behavior.
- Redis stores disposable authenticated sessions only in the current MVP. It is not
  a queue, cache layer, durable job log, audit record, or source of business truth.
- A consuming feature owns its narrow port. Application/domain packages do not
  import go-redis types, and the platform package does not invent generic cache
  or session repositories.

### 2. Signatures

```go
func config.LoadRedis(config.Lookup) (config.Redis, error)
func redis.Open(context.Context, redis.ClientOptions) (*redis.Client, error)
func (*redis.Client) Health(context.Context) error
func (*redis.Client) Close(context.Context) error
```

`redis.Client` embeds the go-redis command surface for outbound adapters only.
Feature constructors receive the narrow dependency they need and map
`redis.Nil` or infrastructure failures into their application error taxonomy.

### 3. Contracts

#### Configuration And Enablement

| Key | Default | Constraint |
|---|---|---|
| `FIXTHE_REDIS_URL` | none | API-required `redis`/`rediss` URL; host required, no DB path/query/fragment |
| `FIXTHE_REDIS_DIAL_TIMEOUT` | `5s` | 100 ms through 1 minute |
| `FIXTHE_REDIS_READ_TIMEOUT` | `3s` | 100 ms through 1 minute |
| `FIXTHE_REDIS_WRITE_TIMEOUT` | `3s` | 100 ms through 1 minute |
| `FIXTHE_REDIS_POOL_TIMEOUT` | `2s` | 100 ms through 1 minute |
| `FIXTHE_REDIS_HEALTH_TIMEOUT` | `2s` | 100 ms through 30 seconds |
| `FIXTHE_REDIS_POOL_SIZE` | `10` | 1 through 1000; hard active-connection cap |
| `FIXTHE_REDIS_MIN_IDLE_CONNS` | `1` | 0 through pool size |
| `FIXTHE_REDIS_MAX_RETRIES` | `2` | 0 through 5; zero disables retries |
| `FIXTHE_REDIS_MIN_RETRY_BACKOFF` | `10ms` | 1 ms through 1 second, not above max |
| `FIXTHE_REDIS_MAX_RETRY_BACKOFF` | `500ms` | 1 ms through 5 seconds, not below min |
| `FIXTHE_REDIS_MAX_CONN_IDLE_TIME` | `5m` | 30 seconds through 1 hour |
| `FIXTHE_REDIS_MAX_CONN_LIFETIME` | `30m` | 1 minute through 24 hours |
| `FIXTHE_REDIS_DB` | `0` | 0 through 255; URL cannot override it |
| `FIXTHE_REDIS_SLOW_COMMAND_THRESHOLD` | `250ms` | 1 ms through 1 minute |

`LoadRedis` can represent a disabled client in isolation, but `LoadAPI` rejects that
state because login sessions require Redis. API startup requires `PING`, readiness
adds the stable dependency name `redis`, and shutdown closes Redis before PostgreSQL
and local telemetry. Migrate does not load Redis. Liveness is unaffected by failure.

The connection pool sets `PoolSize`, `MaxActiveConns`, and
`MaxConcurrentDials` to the same typed limit. Context deadlines are enabled.
Because go-redis uses `-1` to disable retries, typed `MaxRetries=0` is mapped to
`-1`; leaving the library value at zero would silently enable its default
retry count. `rediss` enforces TLS 1.2 or newer.

go-redis maintenance notifications are explicitly disabled for this standalone
contract. Automatic Redis Enterprise endpoint handoff and its library-level
logger are not part of the application's injected logging or trust boundary.

#### Command Observability

The Hook calls only `Cmder.Name()` and validates it as a low-cardinality protocol
command. It never calls `Args()`, `String()`, or records error messages. Single
commands produce `redis.command.completed`; a pipeline is one `pipeline`
operation with `command_count` rather than one event per entry.

Logs/spans/metrics include command name, duration, outcome, stable error class,
and trace correlation. They exclude keys, values, TTL arguments, connection URL,
credentials, server messages, and returned values. Pool metrics expose only
aggregate total/idle connections, pending requests, and wait counts.

Redis `safeError` values use their constructor to capture an `errtrace.Trace` at
parse, startup health, ping, or close failure wrapping points. The trace is returned
without lower-layer logging and is available only to the final private diagnostic
boundary. Direct `safeError` struct literals are forbidden because they omit the
origin trace.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Empty Redis URL passed to `LoadAPI` | Fail startup naming `FIXTHE_REDIS_URL` |
| Invalid URL/typed setting | Fail configuration naming only the key and rule |
| URL contains DB/query/fragment | Reject; typed fields remain sole owners |
| Startup `PING` fails | Close partial client/metrics and fail startup safely |
| Enabled Redis later fails | Readiness returns 503 naming only `redis`; liveness stays 200 |
| Command returns `redis.Nil` | Classify `not_found`; feature adapter maps semantics |
| Network timeout/unavailable | Emit safe class without server or endpoint text |
| Redis error reaches an unknown HTTP 500 | Private log uses the captured safe-error stack |
| Invalid command name | Use `unknown`, never untrusted/raw formatted command |
| Root context already canceled during shutdown | Use a fresh bounded close context |
| Integration URL/prefix missing or unsafe | Fail before writing/deleting any key |

### 5. Good/Base/Bad Cases

- Good: a session adapter receives the client, owns a `SessionStore` port, uses
  bounded TTLs and namespaced keys, and returns application values/errors.
- Base: migrate runs with PostgreSQL only and does not load Redis configuration.
- Bad: code uses Redis lists as a queue, application code returns
  `*redis.StringCmd`, logs `cmd.String()`, relies on unbounded library pool
  defaults, or integration cleanup calls `FLUSHDB`.

### 6. Tests Required

- Configuration: isolated disabled value, API-required URL, every override/bound,
  URL restrictions, min/pool and retry-backoff relations, and secret-safe diagnostics.
- Options: hard pool cap, DB override ownership, context timeouts, TLS minimum,
  zero-retry mapping, client name, and disabled maintenance notifications.
- Hook: success/error/slow levels, safe command/pipeline name, trace correlation,
  latency/outcome metrics, and absence of key/value/error-message text.
- Lifecycle: startup PING, partial-startup cleanup, readiness degradation and
  recovery, captured safe-error call sites, idempotent bounded close,
  Redis-before-PostgreSQL-before-telemetry ordering.
- Integration: explicit URL and test prefix checks, random owned-key set/get,
  exact-key cleanup, and no scan/flush operation.
- Quality: `go vet ./...`, `go test ./...`, `go test -race ./...`, and
  `go build ./cmd/...`.

### 7. Wrong vs Correct

#### Wrong

```go
// Leaks the key/value and returns a concrete Redis type into application code.
logger.Info("redis", "command", cmd.String())
func (s *Service) Session(ctx context.Context) *redis.StringCmd {
    return globalClient.Get(ctx, s.sessionKey)
}
```

#### Correct

```go
type SessionStore interface {
    Find(context.Context, SessionID) (Session, error)
}

// Adapter owns key construction and maps redis.Nil without exposing go-redis.
func (s Store) Find(ctx context.Context, id SessionID) (Session, error) {
    value, err := s.client.Get(ctx, s.key(id)).Result()
    return s.decode(value, err)
}
```

## Common Mistakes

- Do not use Redis for durable business state, audit history, or job transport.
- Do not log command arguments, formatted commands, URLs, credentials, server
  error text, keys, or values.
- Do not let URL query/path parameters override typed configuration.
- Do not interpret go-redis `MaxRetries=0` as disabled; map it to `-1`.
- Do not enable maintenance notifications without a separately reviewed
  endpoint-handoff and safe-library-logging contract.
- Do not skip integration tests when isolation values are missing or delete
  anything beyond the exact random key owned by the test.
