package errs_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/popododo0720/vws-lib/errs"
)

func TestEveryCodeHasAName(t *testing.T) {
	for _, c := range errs.Codes() {
		if c.String() == "" {
			t.Errorf("code %d has no name", int(c))
		}
	}
	if errs.Code(99).String() != "Unspecified" {
		t.Errorf("an unknown code must read as Unspecified")
	}
}

// Every Code has a gRPC code, and the table is the one console-api's errorDetail uses.
func TestGRPCTableIsCompleteAndMatchesConsoleAPI(t *testing.T) {
	want := map[errs.Code]codes.Code{
		errs.CodeInvalidArgument:     codes.InvalidArgument,
		errs.CodePayloadTooLarge:     codes.InvalidArgument,
		errs.CodeNotFound:            codes.NotFound,
		errs.CodeAlreadyExists:       codes.AlreadyExists,
		errs.CodeConflict:            codes.FailedPrecondition,
		errs.CodeIdempotencyMismatch: codes.Aborted,
		errs.CodeUnauthenticated:     codes.Unauthenticated,
		errs.CodePermissionDenied:    codes.PermissionDenied,
		errs.CodeResourceExhausted:   codes.ResourceExhausted,
		errs.CodeUnavailable:         codes.Unavailable,
		errs.CodeUnimplemented:       codes.Unimplemented,
		errs.CodeInternal:            codes.Internal,
		errs.CodeUnspecified:         codes.Internal,
	}
	for _, c := range errs.Codes() {
		got := errs.GRPCCode(c)
		if w, ok := want[c]; !ok || got != w {
			t.Errorf("GRPCCode(%v) = %v, want %v (listed: %v)", c, got, w, ok)
		}
	}
	if errs.GRPCCode(errs.Code(99)) != codes.Internal {
		t.Errorf("an unknown code must map to Internal")
	}
}

func TestOnlyUnavailableIsRetryable(t *testing.T) {
	for _, c := range errs.Codes() {
		if c.Retryable() != (c == errs.CodeUnavailable) {
			t.Errorf("Retryable(%v) = %v", c, c.Retryable())
		}
	}
}

func TestGRPCStatusHidesInternalDetail(t *testing.T) {
	secret := errors.New("dial tcp 10.0.0.7:5432: password=hunter2")
	for name, err := range map[string]error{
		"internal E":    errs.Internal(secret),
		"plain error":   secret,
		"wrapped plain": fmt.Errorf("op: %w", secret),
		"unspecified":   errs.New(errs.CodeUnspecified, "x "+secret.Error()),
	} {
		st := errs.GRPCStatus(err)
		if st.Code() != codes.Internal || st.Message() != "internal error" {
			t.Errorf("%s: got %v %q", name, st.Code(), st.Message())
		}
	}
}

func TestGRPCStatusKeepsCallerMessage(t *testing.T) {
	err := fmt.Errorf("handler: %w", errs.Wrap(errs.CodeNotFound, errors.New("row 7 missing"), "volume vol-1 not found"))
	st := errs.GRPCStatus(err)
	if st.Code() != codes.NotFound || st.Message() != "volume vol-1 not found" {
		t.Errorf("got %v %q", st.Code(), st.Message())
	}
	if errs.GRPCStatus(nil) != nil {
		t.Errorf("a nil error must give a nil status")
	}
}

func TestGRPCStatusPassesThroughAServerStatus(t *testing.T) {
	st := errs.GRPCStatus(status.Error(codes.PermissionDenied, "no"))
	if st.Code() != codes.PermissionDenied || st.Message() != "no" {
		t.Errorf("got %v %q", st.Code(), st.Message())
	}
}

func TestFromGRPCRoundTrip(t *testing.T) {
	for _, c := range errs.Codes() {
		if c == errs.CodePayloadTooLarge || c == errs.CodeUnspecified {
			continue // many-to-one on the wire
		}
		back := errs.CodeOf(errs.FromGRPC(errs.GRPCStatus(errs.New(c, "m")).Err()))
		if back != c {
			t.Errorf("%v came back as %v", c, back)
		}
	}
	if errs.FromGRPC(nil) != nil {
		t.Errorf("FromGRPC(nil) must be nil")
	}
	plain := errors.New("not a status")
	got := errs.FromGRPC(plain)
	if errs.CodeOf(got) != errs.CodeInternal || !errors.Is(got, plain) {
		t.Errorf("a non-status error must become Internal and keep its cause: %v", got)
	}
	if errs.CodeOf(errs.FromGRPC(status.Error(codes.DeadlineExceeded, ""))) != errs.CodeUnavailable {
		t.Errorf("a deadline must read as Unavailable")
	}
}

func TestErrorChain(t *testing.T) {
	cause := errors.New("disk full")
	err := fmt.Errorf("create: %w", errs.Wrap(errs.CodeResourceExhausted, cause, "no space",
		errs.WithKind(errs.KindVolume), errs.WithID("vol-1"), errs.WithField("size")))
	if !errors.Is(err, errs.Sentinel(errs.CodeResourceExhausted)) {
		t.Errorf("errors.Is must match on the code")
	}
	if errors.Is(err, errs.Sentinel(errs.CodeNotFound)) {
		t.Errorf("errors.Is must not match another code")
	}
	if !errors.Is(err, cause) {
		t.Errorf("the cause must stay reachable")
	}
	if errs.CodeOf(err) != errs.CodeResourceExhausted || errs.KindOf(err) != errs.KindVolume {
		t.Errorf("CodeOf/KindOf: %v %v", errs.CodeOf(err), errs.KindOf(err))
	}
	if errs.CodeOf(errors.New("x")) != errs.CodeUnspecified || errs.KindOf(nil) != errs.KindUnspecified {
		t.Errorf("a non-E must be Unspecified")
	}
	if errs.KindVolume.String() != "volume" || errs.KindSecurityGroup.String() != "securityGroup" || errs.Kind(99).String() != "" {
		t.Errorf("kind names")
	}
}

func TestReportedMarker(t *testing.T) {
	if errs.Reported(nil) != nil {
		t.Errorf("Reported(nil) must be nil")
	}
	base := errs.New(errs.CodeConflict, "busy")
	if errs.IsReported(base) {
		t.Errorf("a fresh error is not reported")
	}
	r := errs.Reported(base)
	if !errs.IsReported(r) || !errs.IsReported(fmt.Errorf("outer: %w", r)) {
		t.Errorf("the marker must survive wrapping")
	}
	if !errors.Is(errs.Reported(r), r) || errs.Reported(r).Error() != r.Error() {
		t.Errorf("marking twice must not nest")
	}
	if errs.CodeOf(r) != errs.CodeConflict || !errors.Is(r, errs.Sentinel(errs.CodeConflict)) {
		t.Errorf("the marker must not hide the code")
	}
}

func TestAWSTable(t *testing.T) {
	table := errs.AWSTable{
		Default: map[errs.Code]errs.AWSEntry{errs.CodeNotFound: {HTTP: 404, Code: "NotFound"}},
		ByKind: map[errs.Code]map[errs.Kind]errs.AWSEntry{
			errs.CodeNotFound: {errs.KindInstance: {HTTP: 400, Code: "InvalidInstanceID.NotFound"}},
		},
	}
	if e, ok := table.Lookup(errs.CodeNotFound, errs.KindInstance); !ok || e.Code != "InvalidInstanceID.NotFound" {
		t.Errorf("kind override: %v %v", e, ok)
	}
	if e, ok := table.Lookup(errs.CodeNotFound, errs.KindVpc); !ok || e.Code != "NotFound" {
		t.Errorf("default: %v %v", e, ok)
	}
	if e, ok := table.Lookup(errs.CodeConflict, errs.KindVpc); ok || e != errs.InternalFailure {
		t.Errorf("a missing combination must give InternalFailure and found=false: %v %v", e, ok)
	}
	if len(table.Missing()) != len(errs.Codes())-1 {
		t.Errorf("Missing must list every code without a default, got %v", table.Missing())
	}
	full := errs.AWSTable{Default: map[errs.Code]errs.AWSEntry{}}
	for _, c := range errs.Codes() {
		full.Default[c] = errs.AWSEntry{HTTP: 500, Code: "X"}
	}
	if len(full.Missing()) != 0 {
		t.Errorf("a complete table must have no missing codes")
	}
}
