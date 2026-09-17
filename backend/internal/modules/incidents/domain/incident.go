package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Status 是事故生命周期的稳定状态值。
type Status string

const (
	StatusOpen      Status = "Open"
	StatusRecovered Status = "Recovered"
	StatusClosed    Status = "Closed"
)

// Priority 是事故当前的稳定优先级值。
type Priority string

const (
	PriorityInfo Priority = "Info"
	PriorityP2   Priority = "P2"
	PriorityP1   Priority = "P1"
)

const (
	incidentIDPrefix = "INC-"
	// InitialLifecycleGeneration 是新建事故的生命周期代数。不得持久化 0。
	InitialLifecycleGeneration int64 = 1
)

// Incident 是 application 层使用的事故聚合，不暴露数据库生成类型。
type Incident struct {
	InternalID          string
	ProjectID           string
	EnvironmentID       string
	SourceID            string
	Number              int64
	Title               string
	Fingerprint         string
	Status              Status
	Priority            Priority
	Source              string
	FirstSeen           time.Time
	LastSeen            time.Time
	OccurrenceCount     int64
	HostCount           int64
	Muted               bool
	NotificationSummary string
	LifecycleGeneration int64
	DeployedCommit      string
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// NextLifecycleGeneration 根据状态转换计算下一生命周期代数。
// 仅 Recovered → Open（recovery→reopen）递增；其余转换保持当前值。
// 当前值小于 1 时先按 1 处理，因此 reopen 结果至少为 2，且不会持久化 0。
func NextLifecycleGeneration(from, to Status, current int64) int64 {
	generation := max(current, InitialLifecycleGeneration)
	if from == StatusRecovered && to == StatusOpen {
		return generation + 1
	}
	return generation
}

// ParseStatus 拒绝 API 输入或数据库行中的未知生命周期状态。
func ParseStatus(value string) (Status, error) {
	status := Status(value)
	switch status {
	case StatusOpen, StatusRecovered, StatusClosed:
		return status, nil
	default:
		return "", fmt.Errorf("unknown incident status")
	}
}

// ParsePriority 拒绝 API 输入或数据库行中的未知事故优先级。
func ParsePriority(value string) (Priority, error) {
	priority := Priority(value)
	switch priority {
	case PriorityInfo, PriorityP2, PriorityP1:
		return priority, nil
	default:
		return "", fmt.Errorf("unknown incident priority")
	}
}

// FormatID 将数据库事故编号转换为稳定的外部标识。
func FormatID(number int64) (string, error) {
	if number <= 0 {
		return "", fmt.Errorf("incident number must be positive")
	}
	return incidentIDPrefix + strconv.FormatInt(number, 10), nil
}

// ParseID 将严格的外部标识解析为数据库事故编号。
func ParseID(value string) (int64, error) {
	if !strings.HasPrefix(value, incidentIDPrefix) {
		return 0, fmt.Errorf("incident ID must use INC-<number>")
	}
	numberText := strings.TrimPrefix(value, incidentIDPrefix)
	if numberText == "" || (len(numberText) > 1 && numberText[0] == '0') {
		return 0, fmt.Errorf("incident ID must use a canonical positive number")
	}
	for _, character := range numberText {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("incident ID must use a canonical positive number")
		}
	}
	number, err := strconv.ParseInt(numberText, 10, 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("incident ID must use a canonical positive number")
	}
	return number, nil
}

// NormalizeText 去除边界空白并按 PostgreSQL char_length 语义验证文本长度。
func NormalizeText(value, field string, maximum int) (string, error) {
	normalized := strings.TrimSpace(value)
	length := utf8.RuneCountInString(normalized)
	if length < 1 || length > maximum {
		return "", fmt.Errorf("%s must contain between 1 and %d characters", field, maximum)
	}
	return normalized, nil
}

// Validate 检查跨 application 和 persistence 边界共享的事故不变量。
// Number、Version 和持久化时间在新建前允许为零，repository 映射已存储行时
// 会额外要求这些数据库生成字段为正且非零。
func (incident Incident) Validate() error {
	if incident.InternalID == "" || incident.ProjectID == "" || incident.EnvironmentID == "" || incident.SourceID == "" {
		return fmt.Errorf("incident ownership IDs are required")
	}
	if incident.Number < 0 || incident.Version < 0 {
		return fmt.Errorf("incident database values cannot be negative")
	}
	if incident.LifecycleGeneration < 0 {
		return fmt.Errorf("incident lifecycle_generation cannot be negative")
	}
	if _, err := NormalizeText(incident.Title, "incident title", 240); err != nil {
		return err
	}
	if _, err := NormalizeText(incident.Fingerprint, "incident fingerprint", 255); err != nil {
		return err
	}
	if _, err := ParseStatus(string(incident.Status)); err != nil {
		return err
	}
	if _, err := ParsePriority(string(incident.Priority)); err != nil {
		return err
	}
	if _, err := NormalizeText(incident.Source, "incident source", 128); err != nil {
		return err
	}
	if incident.FirstSeen.IsZero() || incident.LastSeen.IsZero() || incident.LastSeen.Before(incident.FirstSeen) {
		return fmt.Errorf("incident seen times must be present and ordered")
	}
	if incident.OccurrenceCount <= 0 || incident.HostCount <= 0 || incident.HostCount > incident.OccurrenceCount {
		return fmt.Errorf("incident counts must be positive and ordered")
	}
	if _, err := NormalizeText(incident.NotificationSummary, "incident notification summary", 160); err != nil {
		return err
	}
	if (!incident.CreatedAt.IsZero() || !incident.UpdatedAt.IsZero()) &&
		(incident.CreatedAt.IsZero() || incident.UpdatedAt.IsZero() || incident.UpdatedAt.Before(incident.CreatedAt)) {
		return fmt.Errorf("incident persistence times must be present and ordered")
	}
	return nil
}
