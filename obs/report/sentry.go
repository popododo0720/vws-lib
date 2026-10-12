package report

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/getsentry/sentry-go"
	sentryotel "github.com/getsentry/sentry-go/otel"
)

// SentryOptions turns on Sentry. DSN empty means sentry-go is never initialized.
type SentryOptions struct {
	DSN         string
	Environment string
	Release     string
	SampleRate  float64
	Service     string
}

// InitSentry starts sentry-go and returns the Capture function for Sink and a flush function.
// With an empty DSN it returns nil, nil, nil: nothing is initialized.
func InitSentry(o SentryOptions) (capture func(context.Context, error, map[string]string), flush func(time.Duration), err error) {
	if o.DSN == "" {
		return nil, nil, nil
	}
	// SendDefaultPII is left at its default, false. BeforeSend removes the rest.
	rate := o.SampleRate
	if rate <= 0 {
		rate = 1
	}
	err = sentry.Init(sentry.ClientOptions{
		Dsn:         o.DSN,
		Environment: o.Environment,
		Release:     o.Release,
		SampleRate:  rate,
		BeforeSend:  scrubEvent,
		Integrations: func(in []sentry.Integration) []sentry.Integration {
			return append(in, sentryotel.NewOtelIntegration())
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("sentry init: %w", err)
	}
	service := o.Service
	capture = func(_ context.Context, err error, tags map[string]string) {
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("service", service)
			for k, v := range tags {
				scope.SetTag(k, v)
			}
			sentry.CaptureException(err)
		})
	}
	flush = func(d time.Duration) { sentry.Flush(d) }
	return capture, flush, nil
}

var (
	accountID   = regexp.MustCompile(`\b\d{12}\b`)
	sigV4       = regexp.MustCompile(`(?i)(Signature=)[0-9a-f]{16,}`)
	credential  = regexp.MustCompile(`(?i)(Credential=)[^,\s]+`)
	bearer      = regexp.MustCompile(`(?i)(Bearer\s+)\S+`)
	secretPairs = regexp.MustCompile(`(?i)((?:password|passwd|secret|token|api[_-]?key)\s*[=:]\s*)\S+`)
)

// scrub removes what must never reach Sentry from a free-text string: twelve-digit account
// ids, SigV4 signatures and credentials, bearer tokens, and key=value secrets.
func scrub(s string) string {
	s = sigV4.ReplaceAllString(s, "${1}[redacted]")
	s = credential.ReplaceAllString(s, "${1}[redacted]")
	s = bearer.ReplaceAllString(s, "${1}[redacted]")
	s = secretPairs.ReplaceAllString(s, "${1}[redacted]")
	return accountID.ReplaceAllString(s, "[account-id]")
}

// scrubEvent is BeforeSend. SendDefaultPII is already false; this also drops the request body,
// headers, cookies, query string and user, and scrubs every string that can carry a secret.
func scrubEvent(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if e.Request != nil {
		e.Request.Data = ""
		e.Request.Headers = nil
		e.Request.Cookies = ""
		e.Request.QueryString = ""
		e.Request.Env = nil
	}
	e.User = sentry.User{}
	e.Message = scrub(e.Message)
	for i := range e.Exception {
		e.Exception[i].Value = scrub(e.Exception[i].Value)
	}
	for k, v := range e.Tags {
		e.Tags[k] = scrub(v)
	}
	for _, b := range e.Breadcrumbs {
		b.Message = scrub(b.Message)
		b.Data = nil
	}
	return e
}
