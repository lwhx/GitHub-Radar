package scheduler

import (
	"context"
	"errors"
	"testing"
)

func seedJob(t *testing.T, service *Service) int64 {
	t.Helper()
	if err := service.EnsureJob(context.Background()); err != nil {
		t.Fatalf("EnsureJob() error = %v", err)
	}
	jobs, err := service.Jobs(context.Background())
	if err != nil {
		t.Fatalf("Jobs() error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	return jobs[0].ID
}

func TestSetJobPolicyDisablesAndReenables(t *testing.T) {
	service, store := newTestService(t, fixedSearcher{})
	ctx := context.Background()
	jobID := seedJob(t, service)

	disabled := false
	updated, err := service.SetJobPolicy(ctx, jobID, &disabled, nil)
	if err != nil {
		t.Fatalf("SetJobPolicy() error = %v", err)
	}
	if updated.Enabled {
		t.Fatal("job should be disabled")
	}
	var enabled int
	if err := store.SQL().QueryRow(`SELECT enabled FROM collector_jobs WHERE id = ?`, jobID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 {
		t.Fatalf("database enabled = %d, want 0", enabled)
	}

	reenabled := true
	updated, err = service.SetJobPolicy(ctx, jobID, &reenabled, nil)
	if err != nil {
		t.Fatalf("SetJobPolicy() re-enable error = %v", err)
	}
	if !updated.Enabled {
		t.Fatal("job should be enabled again")
	}
	if updated.NextRunAt == nil {
		t.Fatal("next_run_at must be scheduled when the job is enabled")
	}
}

func TestSetJobPolicyUpdatesInterval(t *testing.T) {
	service, store := newTestService(t, fixedSearcher{})
	ctx := context.Background()
	jobID := seedJob(t, service)

	interval := 1800
	updated, err := service.SetJobPolicy(ctx, jobID, nil, &interval)
	if err != nil {
		t.Fatalf("SetJobPolicy() error = %v", err)
	}
	if updated.IntervalSecond != 1800 {
		t.Fatalf("interval = %d, want 1800", updated.IntervalSecond)
	}
	var stored int
	if err := store.SQL().QueryRow(`SELECT interval_seconds FROM collector_jobs WHERE id = ?`, jobID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1800 {
		t.Fatalf("database interval = %d, want 1800", stored)
	}
}

func TestSetJobPolicyRejectsInvalidInterval(t *testing.T) {
	service, _ := newTestService(t, fixedSearcher{})
	ctx := context.Background()
	jobID := seedJob(t, service)

	tooSmall := 5
	if _, err := service.SetJobPolicy(ctx, jobID, nil, &tooSmall); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("SetJobPolicy() error = %v, want ErrInvalidInterval", err)
	}
}

func TestSetJobPolicyReturnsErrJobMissing(t *testing.T) {
	service, _ := newTestService(t, fixedSearcher{})
	disabled := false
	if _, err := service.SetJobPolicy(context.Background(), 404, &disabled, nil); !errors.Is(err, ErrJobMissing) {
		t.Fatalf("SetJobPolicy() error = %v, want ErrJobMissing", err)
	}
}
