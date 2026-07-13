// Package deploymeta holds pure rules for service deploy metadata.
// Keeping these free of I/O lets unit tests assert failed-deploy protection
// without a full Mini-Docker cluster.
package deploymeta

// PrepareCandidate records linkage for a new deploy attempt.
// previousSuccessfulID is the service's last known-good current_deploy_id
// (empty on first deploy). The candidate must not become current until success.
func PrepareCandidate(previousSuccessfulID, candidateID string) (previousDeployID string, currentDeployIDDuringAttempt string) {
	return previousSuccessfulID, previousSuccessfulID
}

// AfterSuccess returns the current_deploy_id that should be stored when a
// candidate deploy becomes the active healthy release.
func AfterSuccess(candidateID string) string {
	return candidateID
}

// AfterFailure restores current_deploy_id to the last successful deploy when a
// candidate fails. If there was no prior success, current_deploy_id is cleared
// so a failed first deploy does not masquerade as current.
func AfterFailure(previousSuccessfulID string) string {
	return previousSuccessfulID
}

// IsHealthyCurrent reports whether currentID is acceptable given that
// lastSuccessID is the last successful deploy and candidateID failed.
func IsHealthyCurrent(currentID, lastSuccessID, failedCandidateID string) bool {
	if currentID == failedCandidateID {
		return false
	}
	return currentID == lastSuccessID
}
