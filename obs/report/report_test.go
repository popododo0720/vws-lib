package report_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/popododo0720/vws-lib/errs"
	"github.com/popododo0720/vws-lib/obs/report"
)

type counts struct {
	mu       sync.Mutex
	errors   []string
	panics   int
	captured []map[string]string
}

func install(t *testing.T, withCapture bool) (*counts, *bytes.Buffer) {
	t.Helper()
	c := &counts{}
	var buf bytes.Buffer
	s := report.Sink{
		Logger:     slog.New(slog.NewJSONHandler(&buf, nil)),
		CountError: func(_ context.Context, code string) { c.mu.Lock(); c.errors = append(c.errors, code); c.mu.Unlock() },
		CountPanic: func(context.Context) { c.mu.Lock(); c.panics++; c.mu.Unlock() },
	}
	if withCapture {
		s.Capture = func(_ context.Context, _ error, tags map[string]string) {
			c.mu.Lock()
			c.captured = append(c.captured, tags)
			c.mu.Unlock()
		}
	}
	report.Configure(s)
	t.Cleanup(func() { report.Configure(report.Sink{}) })
	return c, &buf
}

func TestErrorDoesFourThingsOnce(t *testing.T) {
	c, buf := install(t, true)
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	ctx, span := tp.Tracer("t").Start(t.Context(), "op")

	err := errs.New(errs.CodeNotFound, "volume gone")
	first := report.Error(ctx, err, slog.String("request_id", "req-9"), slog.String("resource_id", "vol-1"))
	if !errs.IsReported(first) || !errors.Is(first, err) {
		t.Fatalf("Error must return the error marked reported: %v", first)
	}
	second := report.Error(ctx, first)
	if second != first { //nolint:errorlint // identity is the point: nothing was wrapped again
		t.Errorf("a reported error must pass through unchanged")
	}
	span.End()

	if len(c.errors) != 1 || c.errors[0] != "NotFound" {
		t.Errorf("counter: %v", c.errors)
	}
	if strings.Count(buf.String(), "error reported") != 1 || !strings.Contains(buf.String(), `"err_code":"NotFound"`) {
		t.Errorf("log: %s", buf.String())
	}
	if len(c.captured) != 1 {
		t.Fatalf("sentry captures: %d", len(c.captured))
	}
	tags := c.captured[0]
	if tags["request_id"] != "req-9" || tags["resource_id"] != "vol-1" || tags["trace_id"] != span.SpanContext().TraceID().String() {
		t.Errorf("tags: %v", tags)
	}
	ended := rec.Ended()
	if len(ended) != 1 || len(ended[0].Events()) != 1 || ended[0].Status().Code.String() != "Error" {
		t.Errorf("span must have one recorded error and Error status: %+v", ended)
	}
	if report.Error(ctx, nil) != nil {
		t.Errorf("Error(nil) must be nil")
	}
}

func TestPanicBecomesInternalWithoutDetail(t *testing.T) {
	c, buf := install(t, false)
	err := report.Panic(t.Context(), "boom password=hunter2")
	if errs.CodeOf(err) != errs.CodeInternal || !errs.IsReported(err) {
		t.Fatalf("Panic must return a reported Internal error: %v", err)
	}
	if st := errs.GRPCStatus(err); st.Message() != "internal error" {
		t.Errorf("the client must see only %q, got %q", "internal error", st.Message())
	}
	if c.panics != 1 || len(c.errors) != 1 {
		t.Errorf("panic counted %d, errors counted %d", c.panics, len(c.errors))
	}
	if !strings.Contains(buf.String(), "stack") {
		t.Errorf("the stack belongs in the log")
	}
}

func TestNoSentryWithoutDSN(t *testing.T) {
	capture, flush, err := report.InitSentry(report.SentryOptions{})
	if err != nil || capture != nil || flush != nil {
		t.Errorf("an empty DSN must initialize nothing: %v %v %v", capture != nil, flush != nil, err)
	}
	if _, _, err := report.InitSentry(report.SentryOptions{DSN: "not a dsn"}); err == nil {
		t.Errorf("a malformed DSN must be an error")
	}
}

// The scrubber is checked against a fake Sentry that records the raw envelope.
func TestSentryEventIsScrubbed(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rd io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				return
			}
			rd = zr
		}
		b, err := io.ReadAll(rd)
		if err != nil {
			return
		}
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	capture, flush, err := report.InitSentry(report.SentryOptions{
		DSN: "http://publickey@" + host + "/1", Environment: "test", Release: "v1", Service: "compute",
	})
	if err != nil || capture == nil {
		t.Fatalf("InitSentry: %v", err)
	}
	cause := errors.New("call failed for account 123456789012: Authorization: Bearer abc.def.ghi " +
		"Credential=AKIAEXAMPLE/20260101/ap-northeast-2/ec2/aws4_request, Signature=0123456789abcdef0123456789abcdef0123456789abcdef password=hunter2")
	capture(t.Context(), errs.Internal(cause), map[string]string{"request_id": "req-1", "resource_id": "vol-1"})
	flush(2 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	all := strings.Join(bodies, "\n")
	if all == "" {
		t.Fatal("the fake Sentry received nothing")
	}
	for _, secret := range []string{"123456789012", "abc.def.ghi", "AKIAEXAMPLE", "0123456789abcdef0123456789abcdef", "hunter2"} {
		if strings.Contains(all, secret) {
			t.Errorf("the event still contains %q", secret)
		}
	}
	for _, want := range []string{"[account-id]", "[redacted]", "req-1", "vol-1", `"service":"compute"`, "environment"} {
		if !strings.Contains(all, want) {
			t.Errorf("the event lacks %q:\n%s", want, all)
		}
	}
}
