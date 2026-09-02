package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

const (
	maxDockerLogLines        = 2000
	maxDockerPatternBytes    = 256
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
	pattern := ""
	if rawPattern, ok := params["pattern"]; ok {
		var patternOK bool
		pattern, patternOK = rawPattern.(string)
		if !patternOK {
			return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolDockerLogs, Message: "pattern must be a string"}
		}
		if err := validateDockerPattern(pattern); err != nil {
			return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolDockerLogs, Message: err.Error()}
		}
	}
	contextBefore, err := dockerContextArgument(params, "context_before")
	if err != nil {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolDockerLogs, Message: err.Error()}
	}
	contextAfter, err := dockerContextArgument(params, "context_after")
	if err != nil {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolDockerLogs, Message: err.Error()}
	}
	tailValue, ok := numericArg(params["tail"])
	if !ok || tailValue < 1 || tailValue > maxDockerLogLines || !isIntegerArgument(params["tail"]) {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolDockerLogs, Message: "tail must be an integer between 1 and 2000"}
	}
	result, err := g.dockerPort.ReadDockerLogs(ctx, scope, domain.DockerLogQuery{
		Since: since, Until: until, Tail: int(tailValue), MaxBytes: g.maxReadBytes,
		Pattern: pattern, ContextBefore: contextBefore, ContextAfter: contextAfter,
	})
	if err != nil {
		return ToolResult{}, fmt.Errorf("docker logs: %w", err)
	}
	returnedLines := countDockerOutputLines(result.Stdout)
	return ToolResult{
		Tool: ToolDockerLogs,
		Summary: fmt.Sprintf("docker logs container=%s bytes=%d truncated=%t coverage_limited=%t refinement_required=%t window_lines=%d returned_lines=%d filtered=%d",
			result.Container.Name, result.BytesRetrieved, result.Truncated, result.CoverageLimited,
			result.RefinementRequired, result.WindowLines, returnedLines, result.FilteredLines),
		BytesRetrieved:     result.BytesRetrieved,
		RefinementRequired: result.RefinementRequired,
		RefinementReason:   result.CoverageReason,
		Payload:            result,
	}, nil
}

func validateDockerPattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("pattern must not be blank")
	}
	if len(pattern) > maxDockerPatternBytes {
		return fmt.Errorf("pattern exceeds 256 bytes")
	}
	for index := 0; index < len(pattern); index++ {
		value := pattern[index]
		if (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') {
			continue
		}
		switch value {
		case ' ', '.', '_', '-', '|', '(', ')', '*', '?':
			continue
		default:
			return fmt.Errorf("pattern contains unsupported characters")
		}
	}
	return nil
}

func dockerContextArgument(params map[string]interface{}, name string) (int, error) {
	value, ok := params[name]
	if !ok {
		return 0, nil
	}
	number, numeric := numericArg(value)
	if !numeric || !isIntegerArgument(value) || number < 0 || number > 100 {
		return 0, fmt.Errorf("%s must be an integer between 0 and 100", name)
	}
	return int(number), nil
}

func countDockerOutputLines(output string) int64 {
	if output == "" {
		return 0
	}
	count := int64(strings.Count(output, "\n"))
	if !strings.HasSuffix(output, "\n") {
		count++
	}
	return count
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
