package telemetry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestSetupDisabledIsNoop(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if Enabled() {
		t.Fatal("enabled without endpoint")
	}
	shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func params(kind trace.SpanKind, parent context.Context) sdktrace.SamplingParameters {
	var tid trace.TraceID
	tid[0] = 1
	return sdktrace.SamplingParameters{ParentContext: parent, TraceID: tid, Name: "x", Kind: kind}
}

func parentCtx() context.Context {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), sc)
}

func TestSamplerDropsOrphanClientSpans(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
	s := sampler()
	if !strings.HasPrefix(s.Description(), "DropOrphanClients{") {
		t.Errorf("description = %q", s.Description())
	}
	cases := []struct {
		name   string
		kind   trace.SpanKind
		parent context.Context
		want   sdktrace.SamplingDecision
	}{
		{"root client (redis poll)", trace.SpanKindClient, context.Background(), sdktrace.Drop},
		{"client under a request", trace.SpanKindClient, parentCtx(), sdktrace.RecordAndSample},
		{"root server", trace.SpanKindServer, context.Background(), sdktrace.RecordAndSample},
	}
	for _, c := range cases {
		if got := s.ShouldSample(params(c.kind, c.parent)).Decision; got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSamplerRatio(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0")
	if got := sampler().ShouldSample(params(trace.SpanKindServer, context.Background())).Decision; got != sdktrace.Drop {
		t.Errorf("ratio 0 root = %v", got)
	}
	// A sampled parent still wins.
	if got := sampler().ShouldSample(params(trace.SpanKindServer, parentCtx())).Decision; got != sdktrace.RecordAndSample {
		t.Errorf("ratio 0 child = %v", got)
	}
}

func TestTraceCoreSwapsContextForIDs(t *testing.T) {
	obs, logs := observer.New(zap.InfoLevel)
	log := zap.New(traceCore{obs})

	ctx := parentCtx()
	log.Info("with span", zap.Any("ctx", ctx), zap.Int("n", 1))
	log.With(zap.Any("ctx", context.Background())).Info("no span")
	log.Debug("filtered")

	all := logs.All()
	if len(all) != 2 {
		t.Fatalf("entries = %d, want 2", len(all))
	}
	f := all[0].ContextMap()
	sc := trace.SpanContextFromContext(ctx)
	if f["trace_id"] != sc.TraceID().String() || f["span_id"] != sc.SpanID().String() || f["n"] != int64(1) {
		t.Errorf("fields = %v", f)
	}
	if _, ok := f["ctx"]; ok {
		t.Error("raw ctx field kept")
	}
	if f := all[1].ContextMap(); len(f) != 0 {
		t.Errorf("no-span fields = %v", f)
	}
}

func TestNewLogger(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	l := NewLogger()
	if !l.Core().Enabled(zapcore.InfoLevel) || l.Core().Enabled(zapcore.DebugLevel) {
		t.Error("logger level is not info")
	}
}

func TestEndRecordsError(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	_, span := tp.Tracer("t").Start(context.Background(), "op")
	End(span, errors.New("boom"))
	_, ok := tp.Tracer("t").Start(context.Background(), "ok")
	End(ok, nil)

	spans := rec.Ended()
	if len(spans) != 2 {
		t.Fatalf("spans = %d", len(spans))
	}
	if spans[0].Status().Description != "boom" || len(spans[0].Events()) != 1 {
		t.Errorf("error span = %+v", spans[0].Status())
	}
	if spans[1].Status().Description != "" {
		t.Errorf("ok span status = %+v", spans[1].Status())
	}
}

func TestSetupEnabledExportsToCollector(t *testing.T) {
	var mu sync.Mutex
	paths := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		paths[r.URL.Path]++
		mu.Unlock()
	}))
	defer srv.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", srv.URL)
	t.Setenv("OTEL_SERVICE_NAME", "telemetry-test")

	origTransport := http.DefaultTransport
	origTracer, origMeter, origProp := otel.GetTracerProvider(), otel.GetMeterProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		http.DefaultTransport = origTransport
		otel.SetTracerProvider(origTracer)
		otel.SetMeterProvider(origMeter)
		otel.SetTextMapPropagator(origProp)
	})

	if !Enabled() {
		t.Fatal("not enabled")
	}
	shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if http.DefaultTransport == origTransport {
		t.Error("default transport not instrumented")
	}

	// A span, a metric and a log record all flush on shutdown.
	ctx, span := otel.Tracer("t").Start(context.Background(), "op")
	span.End()
	_, s2 := Start(ctx, "inner")
	End(s2, errors.New("x"))
	l := NewLogger() // OTel enabled: tee with the OTLP core
	l.Info("hello", zap.Any("ctx", ctx))
	_ = l.Sync()
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if paths["/v1/traces"] == 0 {
		t.Errorf("no trace export, got %v", paths)
	}
}

func TestSetupBadResourceAttributes(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "no-equals-sign")
	if _, err := Setup(context.Background()); err == nil {
		t.Skip("this SDK version tolerates malformed OTEL_RESOURCE_ATTRIBUTES")
	}
}

func TestTraceCoreCheckSkipsDisabledLevels(t *testing.T) {
	obs, _ := observer.New(zap.InfoLevel)
	c := traceCore{obs}
	if ce := c.Check(zapcore.Entry{Level: zapcore.DebugLevel}, nil); ce != nil {
		t.Error("debug entry was checked in")
	}
	if ce := c.Check(zapcore.Entry{Level: zapcore.InfoLevel}, nil); ce == nil {
		t.Error("info entry was dropped")
	}
}
