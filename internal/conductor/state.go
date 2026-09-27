// Package conductor defines the durable domain model and state machine that
// Conductor uses to supervise work: Jobs, Runs, Leases, Sessions and
// Branches. The model is intentionally free of any GitHub or Claude Code
// specific concepts so that other subsystems (execution, observation,
// scheduling, operations) can depend on it without depending on each other.
package conductor

import (
	"errors"
	"fmt"
)

// State is a lifecycle stage shared by the vocabulary of Jobs and Runs.
// Job and Run each have their own transition table (see jobTransitions and
// runTransitions) because the two entities do not always allow the same
// moves, even though they draw from the same set of names.
type State string

const (
	// StateReady means the entity has been defined but has no active
	// executor yet (no granted lease for a Job; no session for a Run).
	StateReady State = "READY"
	// StateActive means work is currently being executed under a live
	// lease/session.
	StateActive State = "ACTIVE"
	// StateWaiting means execution has paused for an external event that is
	// expected in the normal course of work (e.g. review, CI).
	StateWaiting State = "WAITING"
	// StateBlocked means execution cannot proceed without an intervention
	// (human or automated) to resolve a blocker.
	StateBlocked State = "BLOCKED"
	// StateStalled means no forward progress has been observed for longer
	// than expected and the run/job needs attention.
	StateStalled State = "STALLED"
	// StateDone is a terminal success state.
	StateDone State = "DONE"
	// StateFailed is a terminal failure state.
	StateFailed State = "FAILED"
	// StateSuperseded is a terminal state meaning a newer run/job made this
	// one obsolete. Superseded entities are never deleted; they remain
	// traceable in the persistence layer.
	StateSuperseded State = "SUPERSEDED"
)

// Valid reports whether s is one of the known states.
func (s State) Valid() bool {
	switch s {
	case StateReady, StateActive, StateWaiting, StateBlocked, StateStalled,
		StateDone, StateFailed, StateSuperseded:
		return true
	default:
		return false
	}
}

// Terminal reports whether s is a terminal state: once reached, no further
// transitions are allowed.
func (s State) Terminal() bool {
	switch s {
	case StateDone, StateFailed, StateSuperseded:
		return true
	default:
		return false
	}
}

// ErrInvalidTransition is returned when a requested state transition is not
// permitted by the entity's transition table.
var ErrInvalidTransition = errors.New("conductor: invalid state transition")

// transitionTable maps a "from" state to the set of "to" states reachable
// directly from it.
type transitionTable map[State]map[State]bool

func newTransitionTable(edges map[State][]State) transitionTable {
	t := make(transitionTable, len(edges))
	for from, tos := range edges {
		set := make(map[State]bool, len(tos))
		for _, to := range tos {
			set[to] = true
		}
		t[from] = set
	}
	return t
}

func (t transitionTable) allowed(from, to State) bool {
	if from == to {
		return false
	}
	return t[from][to]
}

// jobTransitions is the transition graph for Job.State. A Job mirrors the
// progress of whichever Run currently holds its lease, plus terminal
// outcomes that end the Job regardless of run progress (e.g. the tracked
// issue was closed as a duplicate, superseding the whole Job).
var jobTransitions = newTransitionTable(map[State][]State{
	StateReady:   {StateActive, StateSuperseded},
	StateActive:  {StateWaiting, StateBlocked, StateStalled, StateDone, StateFailed, StateSuperseded},
	StateWaiting: {StateActive, StateBlocked, StateStalled, StateDone, StateFailed, StateSuperseded},
	StateBlocked: {StateActive, StateWaiting, StateStalled, StateDone, StateFailed, StateSuperseded},
	StateStalled: {StateActive, StateWaiting, StateBlocked, StateDone, StateFailed, StateSuperseded},
	// StateDone, StateFailed, StateSuperseded are terminal: no outgoing edges.
})

// runTransitions is the transition graph for Run.State. A Run begins READY
// (created, not yet leased/sessioned), becomes ACTIVE once a lease and
// session are attached, moves among ACTIVE/WAITING/BLOCKED/STALLED as
// execution progress is observed, and ends in exactly one terminal state.
var runTransitions = newTransitionTable(map[State][]State{
	StateReady:   {StateActive, StateSuperseded},
	StateActive:  {StateWaiting, StateBlocked, StateStalled, StateDone, StateFailed, StateSuperseded},
	StateWaiting: {StateActive, StateBlocked, StateStalled, StateDone, StateFailed, StateSuperseded},
	StateBlocked: {StateActive, StateWaiting, StateStalled, StateDone, StateFailed, StateSuperseded},
	StateStalled: {StateActive, StateWaiting, StateBlocked, StateDone, StateFailed, StateSuperseded},
	// StateDone, StateFailed, StateSuperseded are terminal: no outgoing edges.
})

// TransitionJob validates a Job state transition from -> to, returning
// ErrInvalidTransition if it is not allowed.
func TransitionJob(from, to State) error {
	return checkTransition(jobTransitions, "job", from, to)
}

// TransitionRun validates a Run state transition from -> to, returning
// ErrInvalidTransition if it is not allowed.
func TransitionRun(from, to State) error {
	return checkTransition(runTransitions, "run", from, to)
}

func checkTransition(t transitionTable, kind string, from, to State) error {
	if !from.Valid() {
		return fmt.Errorf("conductor: invalid %s state %q: %w", kind, from, ErrInvalidTransition)
	}
	if !to.Valid() {
		return fmt.Errorf("conductor: invalid %s state %q: %w", kind, to, ErrInvalidTransition)
	}
	if !t.allowed(from, to) {
		return fmt.Errorf("conductor: %s %s -> %s: %w", kind, from, to, ErrInvalidTransition)
	}
	return nil
}
