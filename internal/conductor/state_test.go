package conductor

import (
	"errors"
	"testing"
)

func allStates() []State {
	return []State{
		StateReady, StateActive, StateWaiting, StateBlocked, StateStalled,
		StateDone, StateFailed, StateSuperseded,
	}
}

func TestRunTransitions(t *testing.T) {
	valid := map[State]map[State]bool{
		StateReady:      {StateActive: true, StateSuperseded: true},
		StateActive:     {StateWaiting: true, StateBlocked: true, StateStalled: true, StateDone: true, StateFailed: true, StateSuperseded: true},
		StateWaiting:    {StateActive: true, StateBlocked: true, StateStalled: true, StateDone: true, StateFailed: true, StateSuperseded: true},
		StateBlocked:    {StateActive: true, StateWaiting: true, StateStalled: true, StateDone: true, StateFailed: true, StateSuperseded: true},
		StateStalled:    {StateActive: true, StateWaiting: true, StateBlocked: true, StateDone: true, StateFailed: true, StateSuperseded: true},
		StateDone:       {},
		StateFailed:     {},
		StateSuperseded: {},
	}

	for _, from := range allStates() {
		for _, to := range allStates() {
			wantOK := valid[from][to]
			err := TransitionRun(from, to)
			if wantOK && err != nil {
				t.Errorf("TransitionRun(%s, %s) = %v, want nil", from, to, err)
			}
			if !wantOK && err == nil {
				t.Errorf("TransitionRun(%s, %s) = nil, want error", from, to)
			}
			if err != nil && !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("TransitionRun(%s, %s) error %v does not wrap ErrInvalidTransition", from, to, err)
			}
		}
	}
}

func TestJobTransitions(t *testing.T) {
	valid := map[State]map[State]bool{
		StateReady:      {StateActive: true, StateSuperseded: true},
		StateActive:     {StateWaiting: true, StateBlocked: true, StateStalled: true, StateDone: true, StateFailed: true, StateSuperseded: true},
		StateWaiting:    {StateActive: true, StateBlocked: true, StateStalled: true, StateDone: true, StateFailed: true, StateSuperseded: true},
		StateBlocked:    {StateActive: true, StateWaiting: true, StateStalled: true, StateDone: true, StateFailed: true, StateSuperseded: true},
		StateStalled:    {StateActive: true, StateWaiting: true, StateBlocked: true, StateDone: true, StateFailed: true, StateSuperseded: true},
		StateDone:       {},
		StateFailed:     {},
		StateSuperseded: {},
	}

	for _, from := range allStates() {
		for _, to := range allStates() {
			wantOK := valid[from][to]
			err := TransitionJob(from, to)
			if wantOK && err != nil {
				t.Errorf("TransitionJob(%s, %s) = %v, want nil", from, to, err)
			}
			if !wantOK && err == nil {
				t.Errorf("TransitionJob(%s, %s) = nil, want error", from, to)
			}
		}
	}
}

func TestTransition_SameStateRejected(t *testing.T) {
	for _, s := range allStates() {
		if err := TransitionRun(s, s); err == nil {
			t.Errorf("TransitionRun(%s, %s) = nil, want error (no-op transitions aren't transitions)", s, s)
		}
	}
}

func TestTransition_UnknownStateRejected(t *testing.T) {
	bogus := State("NOT_A_REAL_STATE")
	if err := TransitionRun(bogus, StateActive); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("TransitionRun(bogus, ACTIVE) = %v, want ErrInvalidTransition", err)
	}
	if err := TransitionRun(StateReady, bogus); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("TransitionRun(READY, bogus) = %v, want ErrInvalidTransition", err)
	}
}

func TestTerminalStatesHaveNoOutgoingEdges(t *testing.T) {
	for _, terminal := range []State{StateDone, StateFailed, StateSuperseded} {
		if !terminal.Terminal() {
			t.Errorf("%s.Terminal() = false, want true", terminal)
		}
		for _, to := range allStates() {
			if err := TransitionRun(terminal, to); err == nil {
				t.Errorf("TransitionRun(%s, %s) = nil, want error: terminal states must not transition", terminal, to)
			}
			if err := TransitionJob(terminal, to); err == nil {
				t.Errorf("TransitionJob(%s, %s) = nil, want error: terminal states must not transition", terminal, to)
			}
		}
	}
}

func TestNonTerminalStatesAreNotTerminal(t *testing.T) {
	for _, s := range []State{StateReady, StateActive, StateWaiting, StateBlocked, StateStalled} {
		if s.Terminal() {
			t.Errorf("%s.Terminal() = true, want false", s)
		}
	}
}
