package errs

import "testing"

// DO NOT MERGE. Throwaway test for the CI-failure marker live proof (popododo0720/vws-deploy#412).
// It fails on purpose so that the ci workflow is red; each proof head changes the head number below.
func TestCIFailProofDeliberateFailure(t *testing.T) {
	t.Fatal("deliberate failure for the vws-deploy#412 live proof, head 1 of 5")
}
