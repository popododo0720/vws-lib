package errs

import (
	"errors"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// errorDomain is the ErrorInfo.domain every VWS status carries.
	errorDomain = "vws.dev"
	// internalMessage is all a client learns about an unexpected error.
	internalMessage = "internal error"
)

var grpcCodes = map[Code]codes.Code{
	CodeUnspecified:         codes.Internal,
	CodeInvalidArgument:     codes.InvalidArgument,
	CodeNotFound:            codes.NotFound,
	CodeAlreadyExists:       codes.AlreadyExists,
	CodeConflict:            codes.FailedPrecondition,
	CodeIdempotencyMismatch: codes.Aborted,
	CodeUnauthenticated:     codes.Unauthenticated,
	CodePermissionDenied:    codes.PermissionDenied,
	CodeResourceExhausted:   codes.ResourceExhausted,
	CodePayloadTooLarge:     codes.InvalidArgument,
	CodeUnavailable:         codes.Unavailable,
	CodeUnimplemented:       codes.Unimplemented,
	CodeInternal:            codes.Internal,
}

// GRPCCode returns the gRPC code for a Code. It is the table console-api's errorDetail uses.
// An unknown Code is Internal.
func GRPCCode(c Code) codes.Code {
	if g, ok := grpcCodes[c]; ok {
		return g
	}
	return codes.Internal
}

// errorReasons is the ErrorInfo.reason for a Code, matching vws-proto docs/errors.md. That
// contract has no reason for CodeUnimplemented and forbids ERROR_REASON_UNSPECIFIED, so the
// gRPC code name is used there.
var errorReasons = map[Code]string{
	CodeUnspecified:         "RECONCILIATION_FAILED",
	CodeInvalidArgument:     "INVALID_SPEC",
	CodeNotFound:            "RESOURCE_NOT_FOUND",
	CodeAlreadyExists:       "ALREADY_EXISTS",
	CodeConflict:            "DEPENDENCY_NOT_READY",
	CodeIdempotencyMismatch: "IDEMPOTENCY_CONFLICT",
	CodeUnauthenticated:     "INVALID_CREDENTIALS",
	CodePermissionDenied:    "ACCESS_DENIED",
	CodeResourceExhausted:   "QUOTA_EXCEEDED",
	CodePayloadTooLarge:     "INVALID_SPEC",
	CodeUnavailable:         "RECONCILIATION_FAILED",
	CodeUnimplemented:       "UNIMPLEMENTED",
	CodeInternal:            "RECONCILIATION_FAILED",
}

// reasonFor returns the ErrorInfo.reason for a Code. An unknown Code is Internal.
func reasonFor(c Code) string {
	if r, ok := errorReasons[c]; ok {
		return r
	}
	return errorReasons[CodeInternal]
}

// withErrorInfo returns st with exactly one ErrorInfo. A status from a peer that already
// carries one keeps its own details (vws-lib#13).
func withErrorInfo(st *status.Status, c Code) *status.Status {
	for _, d := range st.Details() {
		if _, ok := d.(*errdetails.ErrorInfo); ok {
			return st
		}
	}
	out, err := st.WithDetails(&errdetails.ErrorInfo{Domain: errorDomain, Reason: reasonFor(c)})
	if err != nil {
		return st
	}
	return out
}

// GRPCStatus converts any error to the status a server returns. Every non-OK status carries one
// google.rpc.ErrorInfo with domain vws.dev and the reason for its Code. An error that is not an
// E, and an E with an Internal or Unspecified code, give "internal error": the cause stays in
// the log. A nil error gives a nil status.
func GRPCStatus(err error) *status.Status {
	if err == nil {
		return nil
	}
	var e *E
	if !errors.As(err, &e) {
		// A status in the chain passes through as it is: its own message (not the wrapped
		// "rpc error: ... desc = rpc error: ..." text) and its details (vws-lib#13).
		if st, ok := statusOf(err); ok && st.Code() != codes.Unknown && st.Code() != codes.Internal {
			return withErrorInfo(st, codeFromGRPC(st.Code()))
		}
		return withErrorInfo(status.New(codes.Internal, internalMessage), CodeInternal)
	}
	if e.Code == CodeInternal || e.Code == CodeUnspecified || GRPCCode(e.Code) == codes.Internal {
		return withErrorInfo(status.New(codes.Internal, internalMessage), e.Code)
	}
	// An E made by FromGRPC from a peer's status keeps that status and its details.
	if st, ok := statusOf(e.Unwrap()); ok && st.Code() == GRPCCode(e.Code) && st.Message() == e.Message {
		return withErrorInfo(st, e.Code)
	}
	return withErrorInfo(status.New(GRPCCode(e.Code), e.Message), e.Code)
}

// statusOf returns the gRPC status of the first error in the chain that carries one
// (a status.Error, possibly wrapped). A nil chain or an OK status gives false.
func statusOf(err error) (*status.Status, bool) {
	if err == nil {
		return nil, false
	}
	var g interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &g) {
		return nil, false
	}
	st := g.GRPCStatus()
	if st == nil || st.Code() == codes.OK {
		return nil, false
	}
	return st, true
}

// FromGRPC converts a client-side error back to an E. A status that carries no code maps
// to Internal, and the original error stays as the cause.
func FromGRPC(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return Internal(err)
	}
	msg := st.Message()
	if msg == "" {
		msg = "backend request failed"
	}
	return Wrap(codeFromGRPC(st.Code()), err, msg)
}

func codeFromGRPC(c codes.Code) Code {
	switch c {
	case codes.OK:
		return CodeInternal
	case codes.InvalidArgument, codes.OutOfRange:
		return CodeInvalidArgument
	case codes.NotFound:
		return CodeNotFound
	case codes.AlreadyExists:
		return CodeAlreadyExists
	case codes.FailedPrecondition:
		return CodeConflict
	case codes.Aborted:
		return CodeIdempotencyMismatch
	case codes.Unauthenticated:
		return CodeUnauthenticated
	case codes.PermissionDenied:
		return CodePermissionDenied
	case codes.ResourceExhausted:
		return CodeResourceExhausted
	case codes.Unavailable, codes.DeadlineExceeded:
		return CodeUnavailable
	case codes.Unimplemented:
		return CodeUnimplemented
	default:
		return CodeInternal
	}
}
