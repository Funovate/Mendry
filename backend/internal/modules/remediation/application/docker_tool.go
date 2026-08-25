package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

const (
	maxDockerLogLines        = 500
	maxDockerWindowExpansion = 15 * time.Minute
	maxDockerLogInterval     = 30 * time.Minute
)

func (g *ToolGateway) execDockerLogs(ctx context.Context, scope domain.EvidenceScope, source domain.SourceCapabilitySnapshot, params map[string]interface{}) (ToolResult, error) {
	if g.dockerPort == nil {
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: ToolDockerLogs, Message: "Docker evidence port is unavailable"}
	}
	if source.Kind != "ssh" || !source.Enabled || !source.Supported || source.SSHDeploymentKind != "docker" || strings.TrimSpace(source.SSHContainerName) == "" {
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: ToolDockerLogs, Message: "Docker deployment is unavailable"}
	}
	sinceText, _ := params["since"].(string)
	untilText, _ := params["until"].(string)
	since, err := parseDockerTime(sinceText)
	if err != nil {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolDockerLogs, Message: "since must be an RFC3339 timestamp"}
	}
	until, err := parseDockerTime(untilText)
	if err != nil {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolDockerLogs, Message: "until must be an RFC3339 timestamp"}
	}
	if !since.Before(until) {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolDockerLogs, Message: "since must be before until"}
	}
	if scope.TimeRange.Start.IsZero() || scope.TimeRange.End.IsZero() || !scope.TimeRange.Start.Before(scope.TimeRange.End) {
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: ToolDockerLogs, Message: "incident time range is unavailable"}
	}
	windowStart := scope.TimeRange.Start.Add(-maxDockerWindowExpansion)
	windowEnd := scope.TimeRange.End.Add(maxDockerWindowExpansion)
	if since.Before(windowStart) || until.After(windowEnd) || until.Sub(since) > maxDockerLogInterval {
		return ToolResult{}, &ToolRejection{Code: RejectBudget, Tool: ToolDockerLogs, Message: "Docker log window exceeds the incident bound"}
	}
	tailValue, ok := numericArg(params["tail"])
	if !ok || tailValue < 1 || tailValue > maxDockerLogLines || !isIntegerArgument(params["tail"]) {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolDockerLogs, Message: "tail must be an integer between 1 and 500"}
	}
	result, err := g.dockerPort.ReadDockerLogs(ctx, scope, domain.DockerLogQuery{
		Since: since, Until: until, Tail: int(tailValue), MaxBytes: g.maxReadBytes,
	})
	if err != nil {
		return ToolResult{}, fmt.Errorf("docker logs: %w", err)
	}
	return ToolResult{
		Tool:           ToolDockerLogs,
		Summary:        fmt.Sprintf("docker logs container=%s bytes=%d truncated=%t", result.Container.Name, result.BytesRetrieved, result.Truncated),
		BytesRetrieved: result.BytesRetrieved,
		Payload:        result,
	}, nil
}

func parseDockerTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func isIntegerArgument(value interface{}) bool {
	switch number := value.(type) {
	case int:
		return true
	case int64:
		return true
	case float64:
		return number == float64(int64(number))
	default:
		return false
	}
}
