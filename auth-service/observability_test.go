package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func TestServeUntilSignalWaitsForInFlightRequest(t *testing.T) {
	addr := freeAddr(t)
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
		close(finished)
	})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- serveUntilSignal(ctx, srv) }()

	go func() {
		for i := 0; i < 100; i++ {
			resp, err := http.Get("http://" + addr)
			if err == nil {
				_ = resp.Body.Close()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	<-started
	cancel()
	select {
	case err := <-result:
		t.Fatalf("retornou antes da requisição terminar: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	<-finished
	if err := <-result; err != nil {
		t.Fatalf("encerramento normal deveria retornar nil: %v", err)
	}
}

func TestServeUntilSignalReturnsListenError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	srv := &http.Server{Addr: l.Addr().String()}
	if err := serveUntilSignal(context.Background(), srv); err == nil {
		t.Fatal("porta ocupada deveria retornar erro")
	}
}

func TestFlushTracingExportsPendingSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	// InMemoryExporter limpa seu conteúdo no Shutdown; por isso a cópia é feita antes.
	var exported tracetest.SpanStubs
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(time.Hour)))
	_, span := tp.Tracer("test").Start(context.Background(), "ultimo-span")
	span.End()

	if got := len(exporter.GetSpans()); got != 0 {
		t.Fatalf("span não deveria ter saído antes do flush, saíram %d", got)
	}
	flushTracing(func(ctx context.Context) error {
		if err := tp.ForceFlush(ctx); err != nil {
			return err
		}
		exported = exporter.GetSpans()
		return tp.Shutdown(ctx)
	})
	if len(exported) != 1 {
		t.Fatalf("esperava 1 span exportado, veio %d", len(exported))
	}
}

func TestNewHTTPHandlerFiltersHealthAndNamesByRoute(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	mux := http.NewServeMux()
	ok := func(http.ResponseWriter, *http.Request) {}
	mux.HandleFunc("/health", ok)
	mux.HandleFunc("/validate", ok)
	handler := newHTTPHandler(mux, "svc")

	for _, path := range []string{"/health", "/validate", "/wp-login.php"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	var names []string
	for _, s := range recorder.Ended() {
		names = append(names, s.Name())
	}
	want := []string{"GET /validate", "GET unmatched"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Fatalf("spans = %v, esperava %v", names, want)
	}
}

func TestMarkSpanErrorSetsErrorStatus(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	markSpanError(ctx, errors.New("boom"))
	span.End()

	got := recorder.Ended()[0]
	if got.Status().Code != codes.Error || got.Status().Description != "boom" {
		t.Fatalf("status = %+v", got.Status())
	}
	if len(got.Events()) != 1 {
		t.Fatalf("esperava o evento de exceção, veio %d", len(got.Events()))
	}
}
