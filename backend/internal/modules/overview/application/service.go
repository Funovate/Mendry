package application

import (
	"context"
	"errors"
	"strings"
	"time"
	_ "time/tzdata" // Keep IANA zones available in minimal production images.

	authdomain "mendry/backend/internal/modules/auth/domain"
	projectdomain "mendry/backend/internal/modules/projects/domain"
)

var ErrInvalidInput = errors.New("invalid overview input")

type Task struct {
	RunID          string     `json:"runId"`
	IncidentID     string     `json:"incidentId"`
	Title          string     `json:"title"`
	State          string     `json:"state"`
	AttemptNumber  int        `json:"attemptNumber"`
	Generation     int64      `json:"generation"`
	StartedAt      time.Time  `json:"startedAt"`
	EndedAt        *time.Time `json:"endedAt"`
	StateEnteredAt *time.Time `json:"stateEnteredAt"`
	TokensIn       int64      `json:"tokensIn"`
	TokensOut      int64      `json:"tokensOut"`
	Model          string     `json:"model"`
	UsageRecorded  bool       `json:"usageRecorded"`
	Retryable      bool       `json:"retryable"`
	Latest         bool       `json:"latest"`
}

type Counts struct {
	Processed       int64 `json:"processed"`
	TodayProcessed  int64 `json:"todayProcessed"`
	ActiveIncidents int64 `json:"activeIncidents"`
	ActiveTasks     int64 `json:"activeTasks"`
	Successful      int64 `json:"successful"`
	Failed          int64 `json:"failed"`
	BudgetExhausted int64 `json:"budgetExhausted"`
	Recovered       int64 `json:"recovered"`
	Waiting         int64 `json:"waiting"`
}
type Tokens struct {
	Input           int64 `json:"input"`
	Output          int64 `json:"output"`
	TodayInput      int64 `json:"todayInput"`
	TodayOutput     int64 `json:"todayOutput"`
	UnrecordedTasks int64 `json:"unrecordedTasks"`
}
type Stage struct {
	State string `json:"state"`
	Count int64  `json:"count"`
	Tasks []Task `json:"tasks"`
}
type Bucket struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Input   int64     `json:"input"`
	Output  int64     `json:"output"`
	Covered bool      `json:"covered"`
	Partial bool      `json:"partial"`
}
type Failure struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}
type Attention struct {
	SampleCount     int64     `json:"sampleCount"`
	MedianSeconds   *float64  `json:"medianSeconds"`
	P95Seconds      *float64  `json:"p95Seconds"`
	BudgetExhausted int64     `json:"budgetExhausted"`
	Failures        []Failure `json:"failures"`
	Longest         []Task    `json:"longest"`
	Since           time.Time `json:"since"`
}
type Snapshot struct {
	GeneratedAt         time.Time `json:"generatedAt"`
	Timezone            string    `json:"timezone"`
	Range               string    `json:"range"`
	CollectionStartedAt time.Time `json:"collectionStartedAt"`
	Counts              Counts    `json:"counts"`
	Tokens              Tokens    `json:"tokens"`
	Stages              []Stage   `json:"stages"`
	Trend               []Bucket  `json:"trend"`
	Attention           Attention `json:"attention"`
}
type TaskPage struct {
	Items []Task `json:"items"`
	Total int64  `json:"total"`
}

// Window carries UTC instants; calendar boundaries are computed in the requested zone.
type Window struct {
	Now            time.Time
	Today          time.Time
	Start          time.Time
	AttentionStart time.Time
	Timezone       string
	Range          string
	BucketStarts   []time.Time
	BucketEnds     []time.Time
}

func NewWindow(now time.Time, timezone, period string) (Window, error) {
	if timezone == "" {
		timezone = "UTC"
	}
	if timezone == "Local" || len(timezone) > 100 {
		return Window{}, ErrInvalidInput
	}
	zone, err := time.LoadLocation(timezone)
	if err != nil {
		return Window{}, ErrInvalidInput
	}
	if period == "" {
		period = "7d"
	}
	days := 7
	switch period {
	case "today":
		days = 1
	case "7d":
	case "30d":
		days = 30
	default:
		return Window{}, ErrInvalidInput
	}
	local := now.In(zone)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	start := today.AddDate(0, 0, 1-days)
	w := Window{Now: now.UTC(), Today: today.UTC(), Start: start.UTC(), Timezone: timezone, Range: period,
		AttentionStart: today.AddDate(0, 0, 1-max(days, 7)).UTC(), BucketStarts: []time.Time{}, BucketEnds: []time.Time{}}
	end := today.AddDate(0, 0, 1)
	for at := start; at.Before(end); {
		next := at.AddDate(0, 0, 1)
		if period == "today" {
			next = at.Add(time.Hour)
		}
		if next.After(end) {
			next = end
		}
		// Future hours are not observations and must not be rendered as zero usage.
		if at.After(now) {
			break
		}
		w.BucketStarts = append(w.BucketStarts, at.UTC())
		w.BucketEnds = append(w.BucketEnds, next.UTC())
		at = next
	}
	return w, nil
}

type TaskFilter struct {
	State    string
	Scope    string
	Sort     string
	Page     int
	PageSize int
}

func (f TaskFilter) Validate() error {
	if f.Page < 1 || f.Page > 100000 || f.PageSize < 1 || f.PageSize > 100 {
		return ErrInvalidInput
	}
	if f.Sort != "recent" && f.Sort != "tokens" {
		return ErrInvalidInput
	}
	if f.Scope != "all" && f.Scope != "flow" && f.Scope != "attention" && f.Scope != "failed" && f.Scope != "budget" {
		return ErrInvalidInput
	}
	// Unknown future states remain filterable, but accept only bounded identifiers.
	if len(f.State) > 80 || strings.Trim(f.State, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" {
		return ErrInvalidInput
	}
	return nil
}

type Repository interface {
	Snapshot(context.Context, string, Window) (Snapshot, error)
	Tasks(context.Context, string, TaskFilter) (TaskPage, error)
}
type Projects interface {
	GetProject(context.Context, authdomain.User, string) (projectdomain.Project, error)
}
type Service struct {
	repository Repository
	projects   Projects
}

func NewService(repository Repository, projects Projects) (*Service, error) {
	if repository == nil || projects == nil {
		return nil, errors.New("overview dependencies are required")
	}
	return &Service{repository: repository, projects: projects}, nil
}
func (s *Service) GetSnapshot(ctx context.Context, user authdomain.User, key string, w Window) (Snapshot, error) {
	project, err := s.projects.GetProject(ctx, user, key)
	if err != nil {
		return Snapshot{}, err
	}
	return s.repository.Snapshot(ctx, project.ID, w)
}
func (s *Service) GetTasks(ctx context.Context, user authdomain.User, key string, f TaskFilter) (TaskPage, error) {
	if err := f.Validate(); err != nil {
		return TaskPage{}, err
	}
	project, err := s.projects.GetProject(ctx, user, key)
	if err != nil {
		return TaskPage{}, err
	}
	return s.repository.Tasks(ctx, project.ID, f)
}
