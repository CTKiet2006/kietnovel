package model

import "testing"

func TestOperationTransitions(t *testing.T) {
	tests := []struct {
		from OperationState
		to   OperationState
		want bool
	}{
		{OperationQueued, OperationRunning, true},
		{OperationRunning, OperationQueued, true},
		{OperationRunning, OperationSucceeded, true},
		{OperationSucceeded, OperationRunning, false},
		{OperationCancelled, OperationQueued, false},
		{OperationPaused, OperationSucceeded, false},
		// 只保留有生产者的边（D64）：待审批被新推导取代转 stale；排队与暂停不会失效。
		{OperationAwaitingApproval, OperationStale, true},
		{OperationAwaitingApproval, OperationQueued, false},
		{OperationQueued, OperationStale, false},
		{OperationPaused, OperationStale, false},
	}
	for _, tt := range tests {
		if got := CanTransitionOperation(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransitionOperation(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}
