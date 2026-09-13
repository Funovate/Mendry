package application

import (
	"context"
	"errors"
	authdomain "mendry/backend/internal/modules/auth/domain"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"testing"
	"time"
)

func TestWindowCalendarBoundaries(t *testing.T) {
	tests := []struct {
		name, now, zone, period, start string
		buckets                        int
	}{
		{"Shanghai midnight", "2026-09-12T16:01:00Z", "Asia/Shanghai", "today", "2026-09-12T16:00:00Z", 1},
		{"spring DST", "2026-03-09T03:59:00Z", "America/New_York", "today", "2026-03-08T05:00:00Z", 23},
		{"fall DST", "2026-11-02T04:59:00Z", "America/New_York", "today", "2026-11-01T04:00:00Z", 25},
		{"seven calendar days", "2026-03-10T15:00:00Z", "America/New_York", "7d", "2026-03-04T05:00:00Z", 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, tt.now)
			w, err := NewWindow(now, tt.zone, tt.period)
			if err != nil {
				t.Fatal(err)
			}
			if w.Start.Format(time.RFC3339) != tt.start || len(w.BucketStarts) != tt.buckets {
				t.Fatalf("window: %#v", w)
			}
			for i := range w.BucketStarts {
				if !w.BucketEnds[i].After(w.BucketStarts[i]) {
					t.Fatal("empty bucket")
				}
				if i > 0 && !w.BucketStarts[i].Equal(w.BucketEnds[i-1]) {
					t.Fatal("gap or overlap")
				}
			}
		})
	}
	for _, zone := range []string{"Local", "Not/AZone", "../../etc/passwd"} {
		if _, err := NewWindow(time.Now(), zone, "7d"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("accepted %s", zone)
		}
	}
	if _, err := NewWindow(time.Now(), "UTC", "year"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("accepted invalid period")
	}
}

func TestTaskFilterValidation(t *testing.T) {
	valid := TaskFilter{State: "future_phase", Scope: "flow", Sort: "tokens", Page: 1, PageSize: 20}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*TaskFilter){
		func(f *TaskFilter) { f.Page = 0 }, func(f *TaskFilter) { f.PageSize = 101 },
		func(f *TaskFilter) { f.Sort = "sql" }, func(f *TaskFilter) { f.Scope = "secret" },
		func(f *TaskFilter) { f.State = "x';--" },
	} {
		f := valid
		change(&f)
		if !errors.Is(f.Validate(), ErrInvalidInput) {
			t.Fatalf("accepted %#v", f)
		}
	}
}

type testProjects struct{ err error }

func (p testProjects) GetProject(context.Context, authdomain.User, string) (projectdomain.Project, error) {
	return projectdomain.Project{ID: "authorized-project"}, p.err
}

type testRepository struct {
	project string
	calls   int
}

func (r *testRepository) Snapshot(_ context.Context, project string, _ Window) (Snapshot, error) {
	r.project = project
	r.calls++
	return Snapshot{}, nil
}
func (r *testRepository) Tasks(_ context.Context, project string, _ TaskFilter) (TaskPage, error) {
	r.project = project
	r.calls++
	return TaskPage{}, nil
}
func TestServiceResolvesProjectBeforeReading(t *testing.T) {
	denied := errors.New("denied")
	repo := &testRepository{}
	service, _ := NewService(repo, testProjects{err: denied})
	if _, err := service.GetSnapshot(context.Background(), authdomain.User{}, "unauthorized", Window{}); !errors.Is(err, denied) || repo.calls != 0 {
		t.Fatal("read before authorization")
	}
	service, _ = NewService(repo, testProjects{})
	_, err := service.GetSnapshot(context.Background(), authdomain.User{}, "url-key", Window{})
	if err != nil || repo.project != "authorized-project" {
		t.Fatal("did not scope by resolved ID")
	}
}
