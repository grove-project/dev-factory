package conductor

import (
	"errors"
	"fmt"
	"time"
)

// ID types are distinct string types (rather than a shared "string" alias)
// so the compiler catches accidental mixing, e.g. passing a RunID where a
// JobID is expected.
type (
	JobID     string
	RunID     string
	LeaseID   string
	SessionID string
)

// ErrInvalidEntity is returned by Validate methods when required fields are
// missing or inconsistent.
var ErrInvalidEntity = errors.New("conductor: invalid entity")

// Affinity captures workstream/context affinity metadata: which workstream a
// Job belongs to, and an opaque hint used to keep related Jobs with the same
// executing agent/context across runs.
type Affinity struct {
	// Workstream groups related Jobs for reporting and scheduling, e.g.
	// "Core Runtime".
	Workstream string
	// ContextKey is an opaque grouping hint (e.g. "keep with the same agent
	// for the Core Runtime sequence"). Two Jobs sharing a ContextKey should
	// prefer the same execution context when one is available.
	ContextKey string
}

// Job is the desired unit of work Conductor supervises. It normally
// corresponds to a GitHub issue, but the model only stores an opaque
// SourceRef so this package has no GitHub dependency.
//
// Invariant: a Job survives the failure of any Session working on it. A
// Session failure only ever affects the Run/Session/Lease that used it; the
// Job itself is never deleted and its State only changes through an
// explicit, validated transition (see TransitionJob).
type Job struct {
	ID JobID
	// SourceRef is an opaque external reference, e.g.
	// "github:grove-project/dev-factory#2". Conductor never parses it.
	SourceRef string
	Title     string
	Affinity  Affinity
	State     State
	// CurrentRunID is the Run currently holding this Job's active lease, if
	// any. Empty when the Job has no active executor.
	CurrentRunID RunID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Validate checks that required Job fields are populated.
func (j Job) Validate() error {
	if j.ID == "" {
		return fmt.Errorf("%w: job: ID is required", ErrInvalidEntity)
	}
	if j.SourceRef == "" {
		return fmt.Errorf("%w: job %s: SourceRef is required", ErrInvalidEntity, j.ID)
	}
	if !j.State.Valid() {
		return fmt.Errorf("%w: job %s: state %q is not valid", ErrInvalidEntity, j.ID, j.State)
	}
	return nil
}

// Run is one execution attempt for a Job. Runs are never deleted, even once
// superseded or failed, so history stays traceable.
//
// Invariant: every Run has a unique ID and a unique Branch (enforced by the
// Store, see RegisterBranch).
type Run struct {
	ID    RunID
	JobID JobID
	// Attempt is a 1-based sequence number among the Runs for JobID.
	Attempt int
	// Branch is the name of the isolated Git branch this Run's output is
	// durably recorded on. Required and globally unique.
	Branch string
	State  State
	// SessionID is the disposable external agent execution resource
	// currently attached to this Run, if any. Empty when no session is
	// attached (e.g. before dispatch, or after the session closed).
	SessionID SessionID
	// SupersededBy names the Run that made this one obsolete. Only set when
	// State == StateSuperseded.
	SupersededBy RunID
	// Reason is a free-form note explaining a terminal or blocked state
	// (e.g. a failure message or a description of what a run is blocked on).
	Reason    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks that required Run fields are populated.
func (r Run) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("%w: run: ID is required", ErrInvalidEntity)
	}
	if r.JobID == "" {
		return fmt.Errorf("%w: run %s: JobID is required", ErrInvalidEntity, r.ID)
	}
	if r.Branch == "" {
		return fmt.Errorf("%w: run %s: Branch is required", ErrInvalidEntity, r.ID)
	}
	if r.Attempt < 1 {
		return fmt.Errorf("%w: run %s: Attempt must be >= 1", ErrInvalidEntity, r.ID)
	}
	if !r.State.Valid() {
		return fmt.Errorf("%w: run %s: state %q is not valid", ErrInvalidEntity, r.ID, r.State)
	}
	if r.State == StateSuperseded && r.SupersededBy == "" {
		return fmt.Errorf("%w: run %s: SupersededBy is required when State is SUPERSEDED", ErrInvalidEntity, r.ID)
	}
	return nil
}

// Lease grants a single Run permission to be the active executor for a Job.
//
// Invariant: at most one Lease is active (ReleasedAt == nil) per Job at any
// time. The Store is responsible for enforcing this atomically; see
// Store.AcquireLease.
type Lease struct {
	ID         LeaseID
	JobID      JobID
	RunID      RunID
	AcquiredAt time.Time
	// ReleasedAt is nil while the lease is active.
	ReleasedAt *time.Time
}

// Active reports whether the lease has not been released.
func (l Lease) Active() bool {
	return l.ReleasedAt == nil
}

// Validate checks that required Lease fields are populated.
func (l Lease) Validate() error {
	if l.ID == "" {
		return fmt.Errorf("%w: lease: ID is required", ErrInvalidEntity)
	}
	if l.JobID == "" {
		return fmt.Errorf("%w: lease %s: JobID is required", ErrInvalidEntity, l.ID)
	}
	if l.RunID == "" {
		return fmt.Errorf("%w: lease %s: RunID is required", ErrInvalidEntity, l.ID)
	}
	return nil
}

// Session is a disposable external agent execution resource (e.g. a Claude
// Code cloud run) attached to a Run.
//
// Invariant: Session != Job. A Session's identity, lifecycle and failures
// are entirely separate from the Job it works on; losing a Session never
// implies losing the Job (see Job's invariant above). Conductor only learns
// about Session outcomes through explicit Run transitions.
type Session struct {
	ID    SessionID
	RunID RunID
	// ExternalRef is an opaque handle to the underlying execution resource,
	// e.g. a Claude Code cloud session ID. Conductor never interprets it.
	ExternalRef string
	OpenedAt    time.Time
	// ClosedAt is nil while the session is open.
	ClosedAt *time.Time
}

// Open reports whether the session has not been closed.
func (s Session) Open() bool {
	return s.ClosedAt == nil
}

// Validate checks that required Session fields are populated.
func (s Session) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("%w: session: ID is required", ErrInvalidEntity)
	}
	if s.RunID == "" {
		return fmt.Errorf("%w: session %s: RunID is required", ErrInvalidEntity, s.ID)
	}
	return nil
}

// Branch is the isolated durable output location for a single Run. Branch
// names are globally unique across all Jobs and Runs.
type Branch struct {
	Name      string
	JobID     JobID
	RunID     RunID
	CreatedAt time.Time
}

// Validate checks that required Branch fields are populated.
func (b Branch) Validate() error {
	if b.Name == "" {
		return fmt.Errorf("%w: branch: Name is required", ErrInvalidEntity)
	}
	if b.JobID == "" {
		return fmt.Errorf("%w: branch %s: JobID is required", ErrInvalidEntity, b.Name)
	}
	if b.RunID == "" {
		return fmt.Errorf("%w: branch %s: RunID is required", ErrInvalidEntity, b.Name)
	}
	return nil
}
