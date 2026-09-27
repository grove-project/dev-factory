package conductor

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemStore is a simple, thread-safe in-memory Store implementation. It is
// intended for tests and for early bring-up of subsystems that consume the
// conductor model before a durable backing store exists.
type MemStore struct {
	mu sync.Mutex

	jobs     map[JobID]Job
	runs     map[RunID]Run
	leases   map[LeaseID]Lease
	sessions map[SessionID]Session
	branches map[string]Branch
}

// NewMemStore returns an empty MemStore ready for use.
func NewMemStore() *MemStore {
	return &MemStore{
		jobs:     make(map[JobID]Job),
		runs:     make(map[RunID]Run),
		leases:   make(map[LeaseID]Lease),
		sessions: make(map[SessionID]Session),
		branches: make(map[string]Branch),
	}
}

var _ Store = (*MemStore)(nil)

func (m *MemStore) CreateJob(_ context.Context, job Job) error {
	if err := job.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[job.ID]; ok {
		return fmt.Errorf("job %s: %w", job.ID, ErrAlreadyExists)
	}
	m.jobs[job.ID] = job
	return nil
}

func (m *MemStore) GetJob(_ context.Context, id JobID) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("job %s: %w", id, ErrNotFound)
	}
	return job, nil
}

func (m *MemStore) UpdateJob(_ context.Context, job Job) error {
	if err := job.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[job.ID]; !ok {
		return fmt.Errorf("job %s: %w", job.ID, ErrNotFound)
	}
	m.jobs[job.ID] = job
	return nil
}

func (m *MemStore) CreateRun(_ context.Context, run Run) error {
	if err := run.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[run.ID]; ok {
		return fmt.Errorf("run %s: %w", run.ID, ErrAlreadyExists)
	}
	if _, ok := m.jobs[run.JobID]; !ok {
		return fmt.Errorf("run %s: job %s: %w", run.ID, run.JobID, ErrNotFound)
	}
	m.runs[run.ID] = run
	return nil
}

func (m *MemStore) GetRun(_ context.Context, id RunID) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[id]
	if !ok {
		return Run{}, fmt.Errorf("run %s: %w", id, ErrNotFound)
	}
	return run, nil
}

func (m *MemStore) ListRunsByJob(_ context.Context, jobID JobID) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var runs []Run
	for _, r := range m.runs {
		if r.JobID == jobID {
			runs = append(runs, r)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Attempt < runs[j].Attempt })
	return runs, nil
}

func (m *MemStore) UpdateRun(_ context.Context, run Run) error {
	if err := run.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[run.ID]; !ok {
		return fmt.Errorf("run %s: %w", run.ID, ErrNotFound)
	}
	m.runs[run.ID] = run
	return nil
}

// AcquireLease enforces the "at most one active lease per job" invariant:
// it fails with ErrLeaseHeld if any other lease for lease.JobID is still
// active (ReleasedAt == nil).
func (m *MemStore) AcquireLease(_ context.Context, lease Lease) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.leases[lease.ID]; ok {
		return fmt.Errorf("lease %s: %w", lease.ID, ErrAlreadyExists)
	}
	for _, existing := range m.leases {
		if existing.JobID == lease.JobID && existing.Active() {
			return fmt.Errorf("job %s: %w", lease.JobID, ErrLeaseHeld)
		}
	}
	m.leases[lease.ID] = lease
	return nil
}

func (m *MemStore) ReleaseLease(_ context.Context, id LeaseID, releasedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.leases[id]
	if !ok {
		return fmt.Errorf("lease %s: %w", id, ErrNotFound)
	}
	if !lease.Active() {
		return fmt.Errorf("lease %s: already released", id)
	}
	lease.ReleasedAt = &releasedAt
	m.leases[id] = lease
	return nil
}

func (m *MemStore) GetActiveLease(_ context.Context, jobID JobID) (Lease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, lease := range m.leases {
		if lease.JobID == jobID && lease.Active() {
			return lease, true, nil
		}
	}
	return Lease{}, false, nil
}

func (m *MemStore) OpenSession(_ context.Context, session Session) error {
	if err := session.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[session.ID]; ok {
		return fmt.Errorf("session %s: %w", session.ID, ErrAlreadyExists)
	}
	m.sessions[session.ID] = session
	return nil
}

// CloseSession marks a session closed. It never touches the owning Job or
// Run: a Session's failure/closure must be translated into a Run/Lease
// transition explicitly by the caller, preserving "a job survives session
// failure".
func (m *MemStore) CloseSession(_ context.Context, id SessionID, closedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return fmt.Errorf("session %s: %w", id, ErrNotFound)
	}
	if !session.Open() {
		return fmt.Errorf("session %s: already closed", id)
	}
	session.ClosedAt = &closedAt
	m.sessions[id] = session
	return nil
}

func (m *MemStore) GetSession(_ context.Context, id SessionID) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return Session{}, fmt.Errorf("session %s: %w", id, ErrNotFound)
	}
	return session, nil
}

// RegisterBranch enforces globally unique branch names across all Jobs and
// Runs, matching Git's own constraint that branch names are unique within a
// repository.
func (m *MemStore) RegisterBranch(_ context.Context, branch Branch) error {
	if err := branch.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.branches[branch.Name]; ok {
		return fmt.Errorf("branch %s: %w", branch.Name, ErrAlreadyExists)
	}
	m.branches[branch.Name] = branch
	return nil
}

func (m *MemStore) GetBranch(_ context.Context, name string) (Branch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	branch, ok := m.branches[name]
	if !ok {
		return Branch{}, fmt.Errorf("branch %s: %w", name, ErrNotFound)
	}
	return branch, nil
}
