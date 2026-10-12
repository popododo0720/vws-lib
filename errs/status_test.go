package errs_test

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/popododo0720/vws-lib/errs"
)

// existsStatus is what a handler returns for a duplicate: AlreadyExists with an ErrorInfo.
func existsStatus(t *testing.T) *status.Status {
	t.Helper()
	st, err := status.New(codes.AlreadyExists, "subnet sub-1 already exists").
		WithDetails(&errdetails.ErrorInfo{Reason: "ALREADY_EXISTS", Domain: "network.vws", Metadata: map[string]string{"id": "sub-1"}})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func errorInfo(st *status.Status) *errdetails.ErrorInfo {
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	return nil
}

// vws-lib#13: a status, bare or wrapped, keeps its code, its own message and its details.
func TestStatusPassesThrough(t *testing.T) {
	for name, err := range map[string]error{
		"bare":     existsStatus(t).Err(),
		"wrapped":  fmt.Errorf("create subnet: %w", existsStatus(t).Err()),
		"reported": errs.Reported(fmt.Errorf("create subnet: %w", existsStatus(t).Err())),
	} {
		if got := errs.CodeOf(err); got != errs.CodeAlreadyExists {
			t.Errorf("%s: CodeOf = %v, want AlreadyExists", name, got)
		}
		st := errs.GRPCStatus(err)
		if st.Code() != codes.AlreadyExists || st.Message() != "subnet sub-1 already exists" {
			t.Errorf("%s: status %v %q", name, st.Code(), st.Message())
		}
		if strings.Contains(st.Message(), "rpc error") {
			t.Errorf("%s: message double-wrapped: %q", name, st.Message())
		}
		if info := errorInfo(st); info == nil || info.GetReason() != "ALREADY_EXISTS" || info.GetMetadata()["id"] != "sub-1" {
			t.Errorf("%s: ErrorInfo lost: %v", name, st.Details())
		}
	}
}

// A peer's status turned into an E by FromGRPC and returned again keeps its details.
func TestFromGRPCRoundTripKeepsDetails(t *testing.T) {
	st := errs.GRPCStatus(errs.FromGRPC(existsStatus(t).Err()))
	if st.Code() != codes.AlreadyExists || errorInfo(st) == nil {
		t.Errorf("round trip: %v %q %v", st.Code(), st.Message(), st.Details())
	}
}

// Internal and Unknown statuses still hide their text.
func TestInternalStatusStaysHidden(t *testing.T) {
	for _, c := range []codes.Code{codes.Internal, codes.Unknown} {
		st := errs.GRPCStatus(fmt.Errorf("x: %w", status.Error(c, "dial 10.0.0.7: password=hunter2")))
		if st.Code() != codes.Internal || st.Message() != "internal error" {
			t.Errorf("%v: %v %q", c, st.Code(), st.Message())
		}
	}
	if got := errs.CodeOf(status.Error(codes.Unknown, "x")); got != errs.CodeUnspecified {
		t.Errorf("Unknown status CodeOf = %v", got)
	}
}
