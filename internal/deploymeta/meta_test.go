package deploymeta

import "testing"

func TestPrepareCandidateKeepsPriorCurrent(t *testing.T) {
	prev, current := PrepareCandidate("success-1", "candidate-2")
	if prev != "success-1" {
		t.Fatalf("previous_deploy_id: want success-1, got %q", prev)
	}
	if current != "success-1" {
		t.Fatalf("current during attempt: want success-1, got %q", current)
	}
}

func TestPrepareCandidateFirstDeploy(t *testing.T) {
	prev, current := PrepareCandidate("", "candidate-1")
	if prev != "" {
		t.Fatalf("previous: want empty, got %q", prev)
	}
	if current != "" {
		t.Fatalf("current during first attempt: want empty, got %q", current)
	}
}

func TestAfterSuccessAndFailure(t *testing.T) {
	if got := AfterSuccess("deploy-b"); got != "deploy-b" {
		t.Fatalf("AfterSuccess: got %q", got)
	}
	if got := AfterFailure("deploy-a"); got != "deploy-a" {
		t.Fatalf("AfterFailure: got %q", got)
	}
	if got := AfterFailure(""); got != "" {
		t.Fatalf("AfterFailure empty prior: got %q", got)
	}
}

func TestIsHealthyCurrent(t *testing.T) {
	if IsHealthyCurrent("failed-2", "success-1", "failed-2") {
		t.Fatal("current pointing at failed candidate must be unhealthy")
	}
	if !IsHealthyCurrent("success-1", "success-1", "failed-2") {
		t.Fatal("current pointing at last success must be healthy")
	}
}

// Simulated store sequence: success then failure must leave current on success.
func TestSimulatedSuccessThenFailureSequence(t *testing.T) {
	current := ""
	// first success
	candidate1 := "d1"
	prev, current := PrepareCandidate(current, candidate1)
	if prev != "" || current != "" {
		t.Fatalf("first prepare: prev=%q current=%q", prev, current)
	}
	current = AfterSuccess(candidate1)
	if current != "d1" {
		t.Fatalf("after first success: %q", current)
	}

	// second attempt fails
	candidate2 := "d2"
	prev, current = PrepareCandidate(current, candidate2)
	if prev != "d1" {
		t.Fatalf("second previous: want d1, got %q", prev)
	}
	if current != "d1" {
		t.Fatalf("during second attempt current must stay d1, got %q", current)
	}
	current = AfterFailure(prev)
	if current != "d1" {
		t.Fatalf("after failure current must be d1, got %q", current)
	}
	if !IsHealthyCurrent(current, "d1", "d2") {
		t.Fatal("post-failure state not healthy")
	}
}
