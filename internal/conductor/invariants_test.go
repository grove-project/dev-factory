package conductor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func mustCreateJob(t *testing.T, s *MemStore, id JobID) Job {
	t.Helper()
	job := Job{
		ID:        id,
		SourceRef: "github:grove-project/dev-factory#2",
		Title:     "Define Conductor job/run state model",
		Affinity:  Affinity{Workstream: "Core Runtime", ContextKey: "core-runtime"},
		State:     StateReady,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("CreateJob() = %v, want nil", err)
	}
	return job
}

func mustCreateRun(t *testing.T, s *MemStore, jobID JobID, runID RunID, attempt int, branch string) Run {
	t.Helper()
	run := Run{
		ID:        runID,
		JobID:     jobID,
		Attempt:   attempt,
		Branch:    branch,
		State:     StateReady,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun() = %v, want nil", err)
	}
	if err := s.RegisterBranch(context.Background(), Branch{Name: branch, JobID: jobID, RunID: runID, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("RegisterBranch() = %v, want nil", err)
	}
	return run
}

// TestInvariant_AtMostOneActiveLeasePerJob verifies that a second lease
// cannot be acquired for a job while another lease is still active, but
// that a new lease can be acquired once the prior one is released.
func TestInvariant_AtMostOneActiveLeasePerJob(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	job := mustCreateJob(t, s, "job-1")
	run1 := mustCreateRun(t, s, job.ID, "run-1", 1, "run/job-1/1")

	lease1 := Lease{ID: "lease-1", JobID: job.ID, RunID: run1.ID, AcquiredAt: time.Now()}
	if err := s.AcquireLease(ctx, lease1); err != nil {
		t.Fatalf("AcquireLease(lease1) = %v, want nil", err)
	}

	run2 := mustCreateRun(t, s, job.ID, "run-2", 2, "run/job-1/2")
	lease2 := Lease{ID: "lease-2", JobID: job.ID, RunID: run2.ID, AcquiredAt: time.Now()}
	err := s.AcquireLease(ctx, lease2)
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("AcquireLease(lease2) while lease1 active = %v, want ErrLeaseHeld", err)
	}

	if err := s.ReleaseLease(ctx, lease1.ID, time.Now()); err != nil {
		t.Fatalf("ReleaseLease(lease1) = %v, want nil", err)
	}

	if err := s.AcquireLease(ctx, lease2); err != nil {
		t.Fatalf("AcquireLease(lease2) after lease1 released = %v, want nil", err)
	}

	active, ok, err := s.GetActiveLease(ctx, job.ID)
	if err != nil || !ok || active.ID != lease2.ID {
		t.Fatalf("GetActiveLease() = (%+v, %v, %v), want (lease2, true, nil)", active, ok, err)
	}
}

// TestInvariant_JobSurvivesSessionFailure verifies that closing (failing) a
// Session never mutates or deletes the Job it worked on; the Job's state
// only changes through an explicit TransitionJob-validated update.
func TestInvariant_JobSurvivesSessionFailure(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	job := mustCreateJob(t, s, "job-1")
	run := mustCreateRun(t, s, job.ID, "run-1", 1, "run/job-1/1")

	session := Session{ID: "session-1", RunID: run.ID, ExternalRef: "claude-cloud:abc123", OpenedAt: time.Now()}
	if err := s.OpenSession(ctx, session); err != nil {
		t.Fatalf("OpenSession() = %v, want nil", err)
	}

	// Simulate the session crashing/failing.
	if err := s.CloseSession(ctx, session.ID, time.Now()); err != nil {
		t.Fatalf("CloseSession() = %v, want nil", err)
	}

	gotJob, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() after session failure = %v, want nil error", err)
	}
	if gotJob.State != StateReady {
		t.Fatalf("Job.State after session failure = %s, want unchanged %s", gotJob.State, StateReady)
	}

	closedSession, err := s.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetSession() = %v, want nil", err)
	}
	if closedSession.Open() {
		t.Fatalf("Session.Open() = true after CloseSession, want false")
	}

	// The Run also survives untouched; only an explicit transition changes it.
	gotRun, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun() after session failure = %v, want nil", err)
	}
	if gotRun.State != StateReady {
		t.Fatalf("Run.State after session failure = %s, want unchanged %s", gotRun.State, StateReady)
	}
}

// TestInvariant_SupersededRunsRemainTraceable verifies that superseding a
// run never deletes it: it stays listed among the job's runs with a
// SupersededBy pointer to the replacement.
func TestInvariant_SupersededRunsRemainTraceable(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	job := mustCreateJob(t, s, "job-1")
	run1 := mustCreateRun(t, s, job.ID, "run-1", 1, "run/job-1/1")
	run2 := mustCreateRun(t, s, job.ID, "run-2", 2, "run/job-1/2")

	if err := TransitionRun(run1.State, StateActive); err != nil {
		t.Fatalf("TransitionRun(READY, ACTIVE) = %v, want nil", err)
	}
	run1.State = StateActive
	if err := s.UpdateRun(ctx, run1); err != nil {
		t.Fatalf("UpdateRun(run1 -> ACTIVE) = %v, want nil", err)
	}

	if err := TransitionRun(run1.State, StateSuperseded); err != nil {
		t.Fatalf("TransitionRun(ACTIVE, SUPERSEDED) = %v, want nil", err)
	}
	run1.State = StateSuperseded
	run1.SupersededBy = run2.ID
	if err := s.UpdateRun(ctx, run1); err != nil {
		t.Fatalf("UpdateRun(run1 -> SUPERSEDED) = %v, want nil", err)
	}

	runs, err := s.ListRunsByJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("ListRunsByJob() = %v, want nil", err)
	}
	if len(runs) != 2 {
		t.Fatalf("ListRunsByJob() returned %d runs, want 2 (superseded run must remain listed)", len(runs))
	}

	got, err := s.GetRun(ctx, run1.ID)
	if err != nil {
		t.Fatalf("GetRun(run1) after superseding = %v, want nil", err)
	}
	if got.State != StateSuperseded {
		t.Fatalf("run1.State = %s, want SUPERSEDED", got.State)
	}
	if got.SupersededBy != run2.ID {
		t.Fatalf("run1.SupersededBy = %s, want %s", got.SupersededBy, run2.ID)
	}
}

// TestInvariant_UniqueBranchPerRun verifies that Run.Validate rejects a
// missing branch, and that the store rejects registering the same branch
// name twice.
func TestInvariant_UniqueBranchPerRun(t *testing.T) {
	if err := (Run{ID: "r1", JobID: "j1", Attempt: 1, State: StateReady}).Validate(); !errors.Is(err, ErrInvalidEntity) {
		t.Fatalf("Run without Branch: Validate() = %v, want ErrInvalidEntity", err)
	}

	ctx := context.Background()
	s := NewMemStore()
	job := mustCreateJob(t, s, "job-1")
	_ = mustCreateRun(t, s, job.ID, "run-1", 1, "shared-branch")

	run2 := Run{ID: "run-2", JobID: job.ID, Attempt: 2, Branch: "shared-branch", State: StateReady, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := s.CreateRun(ctx, run2); err != nil {
		t.Fatalf("CreateRun(run2) = %v, want nil", err)
	}
	err := s.RegisterBranch(ctx, Branch{Name: "shared-branch", JobID: job.ID, RunID: run2.ID, CreatedAt: time.Now()})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("RegisterBranch(duplicate name) = %v, want ErrAlreadyExists", err)
	}
}

// TestInvariant_EveryRunHasUniqueIdentity verifies that creating two runs
// with the same ID fails.
func TestInvariant_EveryRunHasUniqueIdentity(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	job := mustCreateJob(t, s, "job-1")
	_ = mustCreateRun(t, s, job.ID, "run-1", 1, "run/job-1/1")

	dup := Run{ID: "run-1", JobID: job.ID, Attempt: 2, Branch: "run/job-1/2", State: StateReady, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := s.CreateRun(ctx, dup); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("CreateRun(duplicate ID) = %v, want ErrAlreadyExists", err)
	}
}

// TestSessionDistinctFromJob is a light compile-time-flavored check that a
// Session's fields never expose Job identity: a Session only ever knows
// about a RunID, never a JobID, so "Session != Job" is structural.
func TestSessionDistinctFromJob(t *testing.T) {
	session := Session{ID: "s1", RunID: "run-1", ExternalRef: "ext", OpenedAt: time.Now()}
	if err := session.Validate(); err != nil {
		t.Fatalf("Session.Validate() = %v, want nil", err)
	}
	// If Session ever grows a JobID field this test (and the type) should be
	// revisited; today the type simply has no such field to assert on.
}
