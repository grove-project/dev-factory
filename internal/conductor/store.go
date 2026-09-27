package conductor

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a lookup fails to find the requested entity.
var ErrNotFound = errors.New("conductor: not found")

// ErrAlreadyExists is returned when creating an entity whose ID/key already
// exists.
var ErrAlreadyExists = errors.New("conductor: already exists")

// ErrLeaseHeld is returned by AcquireLease when the target Job already has
// an active lease (the "at most one active lease per job" invariant).
var ErrLeaseHeld = errors.New("conductor: job already has an active lease")

// Store is the persistence boundary for the Conductor domain model. It
// knows nothing about GitHub or Claude Code; implementations may be backed
// by anything from an in-memory map (see MemStore) to a relational
// database. Callers outside this package should depend only on this
// interface, not on any particular implementation.
//
// Store implementations are responsible for enforcing the invariants that
// span multiple entities:
//   - AcquireLease enforces at most one active lease per Job.
//   - RegisterBranch enforces globally unique branch names.
//   - Runs and Leases are never deleted, so history remains traceable even
//     once superseded or released.
type Store interface {
	// CreateJob persists a new Job. Returns ErrAlreadyExists if job.ID is
	// already in use, or an error wrapping ErrInvalidEntity if job fails
	// validation.
	CreateJob(ctx context.Context, job Job) error
	// GetJob returns the Job with the given ID, or ErrNotFound.
	GetJob(ctx context.Context, id JobID) (Job, error)
	// UpdateJob persists a full replacement of an existing Job. Callers must
	// validate the state transition themselves (see TransitionJob) before
	// calling UpdateJob; the Store only enforces field-level validity.
	UpdateJob(ctx context.Context, job Job) error

	// CreateRun persists a new Run. Returns ErrAlreadyExists if run.ID is
	// already in use.
	CreateRun(ctx context.Context, run Run) error
	// GetRun returns the Run with the given ID, or ErrNotFound.
	GetRun(ctx context.Context, id RunID) (Run, error)
	// ListRunsByJob returns every Run ever created for jobID, including
	// superseded and failed ones, ordered by Attempt ascending.
	ListRunsByJob(ctx context.Context, jobID JobID) ([]Run, error)
	// UpdateRun persists a full replacement of an existing Run. Callers must
	// validate the state transition themselves (see TransitionRun) before
	// calling UpdateRun.
	UpdateRun(ctx context.Context, run Run) error

	// AcquireLease creates a new active Lease for lease.JobID. Returns
	// ErrLeaseHeld if the Job already has an active (unreleased) lease.
	AcquireLease(ctx context.Context, lease Lease) error
	// ReleaseLease marks the given Lease released at releasedAt. Returns
	// ErrNotFound if it does not exist, or an error if it is already
	// released.
	ReleaseLease(ctx context.Context, id LeaseID, releasedAt time.Time) error
	// GetActiveLease returns the currently active lease for jobID, if any.
	// The bool return is false (with a zero Lease and nil error) when the
	// Job has no active lease.
	GetActiveLease(ctx context.Context, jobID JobID) (Lease, bool, error)

	// OpenSession persists a new, open Session.
	OpenSession(ctx context.Context, session Session) error
	// CloseSession marks the given Session closed at closedAt. Closing a
	// Session never modifies the Job or Run it was attached to.
	CloseSession(ctx context.Context, id SessionID, closedAt time.Time) error
	// GetSession returns the Session with the given ID, or ErrNotFound.
	GetSession(ctx context.Context, id SessionID) (Session, error)

	// RegisterBranch reserves a Branch name for a Run. Returns
	// ErrAlreadyExists if the name is already registered by any Job/Run.
	RegisterBranch(ctx context.Context, branch Branch) error
	// GetBranch returns the Branch with the given name, or ErrNotFound.
	GetBranch(ctx context.Context, name string) (Branch, error)
}
