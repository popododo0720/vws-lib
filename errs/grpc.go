package errs

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// internalMessage is all a client learns about an unexpected error.
const internalMessage = "internal error"

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

// GRPCStatus converts any error to the status a server returns. An error that is not an E,
// and an E with an Internal or Unspecified code, give "internal error": the cause stays in
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
			return st
		}
		return status.New(codes.Internal, internalMessage)
	}
	if e.Code == CodeInternal || e.Code == CodeUnspecified || GRPCCode(e.Code) == codes.Internal {
		return status.New(codes.Internal, internalMessage)
	}
	// An E made by FromGRPC from a peer's status keeps that status and its details.
	if st, ok := statusOf(e.Unwrap()); ok && st.Code() == GRPCCode(e.Code) && st.Message() == e.Message {
		return st
	}
	return status.New(GRPCCode(e.Code), e.Message)
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
