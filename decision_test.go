package agentstatus

import (
	"testing"
	"time"
)

func ptr(s Status) *Status { return &s }

func TestDecide_Table(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		state      Status
		sig        Signal
		wantState  Status
		wantTrans  bool
		wantStatus Status
		wantPrev   Status
	}{
		// --- from zero state ---
		{
			name:       "zero+activity→working",
			state:      "",
			sig:        Signal{Activity: true},
			wantState:  StatusWorking,
			wantTrans:  true,
			wantStatus: StatusWorking,
			wantPrev:   "",
		},
		{
			name:       "zero+auth(starting)→starting",
			state:      "",
			sig:        Signal{Status: ptr(StatusStarting)},
			wantState:  StatusStarting,
			wantTrans:  true,
			wantStatus: StatusStarting,
			wantPrev:   "",
		},
		{
			name:      "zero+nothing→no change",
			state:     "",
			sig:       Signal{},
			wantState: "",
			wantTrans: false,
		},

		// --- duplicate suppression ---
		{
			name:      "working+activity→suppress",
			state:     StatusWorking,
			sig:       Signal{Activity: true},
			wantState: StatusWorking,
			wantTrans: false,
		},
		{
			name:      "working+auth(working)→suppress",
			state:     StatusWorking,
			sig:       Signal{Status: ptr(StatusWorking)},
			wantState: StatusWorking,
			wantTrans: false,
		},
		{
			name:      "idle+no activity→suppress",
			state:     StatusIdle,
			sig:       Signal{},
			wantState: StatusIdle,
			wantTrans: false,
		},

		// --- authoritative override beats activity inference ---
		{
			name:       "idle+activity+auth(awaiting)→awaiting",
			state:      StatusIdle,
			sig:        Signal{Activity: true, Status: ptr(StatusAwaitingInput)},
			wantState:  StatusAwaitingInput,
			wantTrans:  true,
			wantStatus: StatusAwaitingInput,
			wantPrev:   StatusIdle,
		},
		{
			name:       "working+activity+auth(idle)→idle",
			state:      StatusWorking,
			sig:        Signal{Activity: true, Status: ptr(StatusIdle)},
			wantState:  StatusIdle,
			wantTrans:  true,
			wantStatus: StatusIdle,
			wantPrev:   StatusWorking,
		},

		// --- every status transition both ways (representative subset
		// covers every distinct pair of non-equal statuses) ---
		{
			name:       "starting→working (activity)",
			state:      StatusStarting,
			sig:        Signal{Activity: true},
			wantState:  StatusWorking,
			wantTrans:  true,
			wantStatus: StatusWorking,
			wantPrev:   StatusStarting,
		},
		{
			name:       "working→idle",
			state:      StatusWorking,
			sig:        Signal{Status: ptr(StatusIdle)},
			wantState:  StatusIdle,
			wantTrans:  true,
			wantStatus: StatusIdle,
			wantPrev:   StatusWorking,
		},
		{
			name:       "idle→working (activity)",
			state:      StatusIdle,
			sig:        Signal{Activity: true},
			wantState:  StatusWorking,
			wantTrans:  true,
			wantStatus: StatusWorking,
			wantPrev:   StatusIdle,
		},
		{
			name:       "awaiting→working (activity)",
			state:      StatusAwaitingInput,
			sig:        Signal{Activity: true},
			wantState:  StatusWorking,
			wantTrans:  true,
			wantStatus: StatusWorking,
			wantPrev:   StatusAwaitingInput,
		},
		{
			name:       "working→error",
			state:      StatusWorking,
			sig:        Signal{Status: ptr(StatusError)},
			wantState:  StatusError,
			wantTrans:  true,
			wantStatus: StatusError,
			wantPrev:   StatusWorking,
		},
		{
			name:       "error→idle",
			state:      StatusError,
			sig:        Signal{Status: ptr(StatusIdle)},
			wantState:  StatusIdle,
			wantTrans:  true,
			wantStatus: StatusIdle,
			wantPrev:   StatusError,
		},
		{
			name:       "idle→ended",
			state:      StatusIdle,
			sig:        Signal{Status: ptr(StatusEnded)},
			wantState:  StatusEnded,
			wantTrans:  true,
			wantStatus: StatusEnded,
			wantPrev:   StatusIdle,
		},
		{
			name:       "ended→starting (new session reuse)",
			state:      StatusEnded,
			sig:        Signal{Status: ptr(StatusStarting)},
			wantState:  StatusStarting,
			wantTrans:  true,
			wantStatus: StatusStarting,
			wantPrev:   StatusEnded,
		},
		{
			name:       "working→awaiting",
			state:      StatusWorking,
			sig:        Signal{Status: ptr(StatusAwaitingInput)},
			wantState:  StatusAwaitingInput,
			wantTrans:  true,
			wantStatus: StatusAwaitingInput,
			wantPrev:   StatusWorking,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, trans := Decide(sessionState{Status: tc.state}, tc.sig)
			if got.Status != tc.wantState {
				t.Errorf("state: got %q, want %q", got.Status, tc.wantState)
			}
			if (trans != nil) != tc.wantTrans {
				t.Fatalf("trans present: got %v, want %v", trans != nil, tc.wantTrans)
			}
			if !tc.wantTrans {
				return
			}
			if trans.Status != tc.wantStatus {
				t.Errorf("trans.Status: got %q, want %q", trans.Status, tc.wantStatus)
			}
			if trans.PrevStatus != tc.wantPrev {
				t.Errorf("trans.PrevStatus: got %q, want %q", trans.PrevStatus, tc.wantPrev)
			}
		})
	}
}

func TestDecide_Deterministic(t *testing.T) {
	t.Parallel()
	st := sessionState{Status: StatusIdle}
	sig := Signal{Activity: true, At: time.Unix(1, 0)}

	s1, t1 := Decide(st, sig)
	s2, t2 := Decide(st, sig)

	if s1 != s2 {
		t.Fatalf("non-deterministic state: %+v vs %+v", s1, s2)
	}
	if (t1 == nil) != (t2 == nil) {
		t.Fatalf("non-deterministic transition presence")
	}
	if t1 != nil && *t1 != *t2 {
		t.Fatalf("non-deterministic transition content: %+v vs %+v", *t1, *t2)
	}
}

func TestDecide_NoInputMutation(t *testing.T) {
	t.Parallel()
	orig := StatusIdle
	sig := Signal{Status: &orig, Activity: true}
	_, _ = Decide(sessionState{Status: StatusWorking}, sig)
	if *sig.Status != StatusIdle {
		t.Fatalf("Decide mutated sig.Status: got %q", *sig.Status)
	}
	if !sig.Activity {
		t.Fatalf("Decide mutated sig.Activity")
	}
}
