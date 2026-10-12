package errs

import (
	"errors"
	"fmt"
)

// E is the error every layer returns. Message is for the caller. The cause is for logs and
// never goes on the wire.
type E struct {
	Code    Code
	Kind    Kind
	ID      string
	Field   string
	Message string
	cause   error
}

// Option sets an optional field of an E.
type Option func(*E)

// WithKind names the resource kind the error is about.
func WithKind(k Kind) Option { return func(e *E) { e.Kind = k } }

// WithID names the resource id the error is about.
func WithID(id string) Option { return func(e *E) { e.ID = id } }

// WithField names the request field the error is about.
func WithField(f string) Option { return func(e *E) { e.Field = f } }

// WithCause keeps an internal error for logs. It is returned by Unwrap, not by Message.
func WithCause(err error) Option { return func(e *E) { e.cause = err } }

// New returns an E with a code and a caller-visible message.
func New(code Code, message string, opts ...Option) *E {
	e := &E{Code: code, Message: message}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Wrap is New with a cause.
func Wrap(code Code, cause error, message string, opts ...Option) *E {
	return New(code, message, append([]Option{WithCause(cause)}, opts...)...)
}

// Internal wraps an unexpected error. The caller sees "internal error", the log sees the cause.
func Internal(cause error) *E {
	return Wrap(CodeInternal, cause, "internal error")
}

func (e *E) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%v: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%v: %s", e.Code, e.Message)
}

// Unwrap returns the internal cause.
func (e *E) Unwrap() error { return e.cause }

// Is lets errors.Is(err, errs.Sentinel(code)) match on the code alone.
func (e *E) Is(target error) bool {
	t, ok := target.(*E) //nolint:errorlint // target is the sentinel itself, not a wrapped chain
	if !ok || t.Message != "" || t.Kind != KindUnspecified || t.ID != "" || t.Field != "" {
		return false
	}
	return t.Code == e.Code
}

// Sentinel returns a value for errors.Is that matches any E with this code.
func Sentinel(code Code) error { return &E{Code: code} }

// CodeOf returns the code of the first E in the chain, or CodeUnspecified.
func CodeOf(err error) Code {
	var e *E
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeUnspecified
}

// KindOf returns the kind of the first E in the chain, or KindUnspecified.
func KindOf(err error) Kind {
	var e *E
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindUnspecified
}

// reported marks an error that report.Error already recorded.
type reported struct{ err error }

func (r *reported) Error() string { return r.err.Error() }
func (r *reported) Unwrap() error { return r.err }

// Reported marks err as already reported so an outer layer does not report it again.
// Reported(nil) is nil.
func Reported(err error) error {
	if err == nil || IsReported(err) {
		return err
	}
	return &reported{err: err}
}

// IsReported reports whether err or an error it wraps was marked by Reported.
func IsReported(err error) bool {
	var r *reported
	return errors.As(err, &r)
}
