// Package observability 统一构造结构化日志并维护稳定的 event、field 和 telemetry 契约。
package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"fixthe/backend/internal/platform/buildinfo"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "fixthe/backend"

// TelemetryOptions 声明一个进程实例的本地 OpenTelemetry 身份。
type TelemetryOptions struct {
	Service     string
	Environment string
	Build       buildinfo.Info
}

// Telemetry 持有实例级 trace、metric 和 W3C propagation 依赖。
// 该类型不修改 OpenTelemetry global provider，避免同进程测试或多实例相互污染。
type Telemetry struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
	propagator     propagation.TextMapPropagator
}

// NewTelemetry 创建仅进程内使用的 provider 和 W3C propagator，不连接远程 exporter。
func NewTelemetry(ctx context.Context, options TelemetryOptions) (*Telemetry, error) {
	_ = ctx
	if options.Service == "" {
		return nil, fmt.Errorf("telemetry service is required")
	}
	if options.Environment == "" {
		return nil, fmt.Errorf("telemetry environment is required")
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(options.Service),
		semconv.ServiceVersion(options.Build.Version),
		semconv.DeploymentEnvironmentNameKey.String(options.Environment),
	)

	traceOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(boundedParentSampler{
			root:   sdktrace.TraceIDRatioBased(0.1),
			parent: sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1)),
		}),
	}
	metricOptions := []sdkmetric.Option{sdkmetric.WithResource(res)}

	return &Telemetry{
		tracerProvider: sdktrace.NewTracerProvider(traceOptions...),
		meterProvider:  sdkmetric.NewMeterProvider(metricOptions...),
		propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	}, nil
}

// boundedParentSampler 继承进程内 parent 的采样决策，但对 remote parent 重新应用
// 本地 ratio。这样可以继续远端 trace ID，同时避免未受信任入口通过 sampled bit
// 强制本服务产生不受控的 telemetry。
type boundedParentSampler struct {
	root   sdktrace.Sampler
	parent sdktrace.Sampler
}

func (s boundedParentSampler) ShouldSample(parameters sdktrace.SamplingParameters) sdktrace.SamplingResult {
	parent := trace.SpanContextFromContext(parameters.ParentContext)
	if parent.IsRemote() {
		return s.root.ShouldSample(parameters)
	}
	return s.parent.ShouldSample(parameters)
}

func (boundedParentSampler) Description() string {
	return "BoundedParentBasedTraceIDRatio"
}

// Tracer 返回绑定当前进程 provider 的 tracer，不使用 global provider。
func (t *Telemetry) Tracer(name string) trace.Tracer {
	if name == "" {
		name = instrumentationName
	}
	return t.tracerProvider.Tracer(name)
}

// MeterProvider 返回当前进程实例的 metric provider。
func (t *Telemetry) MeterProvider() metric.MeterProvider {
	return t.meterProvider
}

// HTTPHandler 为入站请求提取 W3C context，并创建 server span 和 HTTP metrics。
func (t *Telemetry) HTTPHandler(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, "http.server.request",
		otelhttp.WithTracerProvider(t.tracerProvider),
		otelhttp.WithMeterProvider(t.meterProvider),
		otelhttp.WithPropagators(t.propagator),
		otelhttp.WithSpanNameFormatter(func(operation string, request *http.Request) string {
			if request.Pattern != "" {
				return request.Pattern
			}
			return operation
		}),
	)
}

// Shutdown 关闭进程内 metric 和 trace provider。
// 调用方必须提供独立的有界 context，不能复用已经取消的进程根 context。
func (t *Telemetry) Shutdown(ctx context.Context) error {
	return errors.Join(
		t.meterProvider.ForceFlush(ctx),
		t.tracerProvider.ForceFlush(ctx),
		t.meterProvider.Shutdown(ctx),
		t.tracerProvider.Shutdown(ctx),
	)
}
