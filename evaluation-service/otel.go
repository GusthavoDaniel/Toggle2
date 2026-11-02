package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// initTracing configura o OpenTelemetry (traces via OTLP/gRPC).
// Tudo é configurado por variáveis de ambiente padrão do OTel:
//
//	OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES,
//	OTEL_EXPORTER_OTLP_ENDPOINT
//
// Se OTEL_EXPORTER_OTLP_ENDPOINT não estiver definida, o tracing fica
// desligado (útil no docker-compose local) e a função de shutdown é no-op.
func initTracing(ctx context.Context) func(context.Context) error {
	// Propagação W3C (traceparent) é registrada sempre, para que o serviço
	// repasse o contexto mesmo sem exportar spans.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		log.Println("OTEL_EXPORTER_OTLP_ENDPOINT não definida: tracing desabilitado.")
		return func(context.Context) error { return nil }
	}

	// Sempre gRPC (porta 4317): este exporter lê o endpoint do ambiente, mas
	// ignora OTEL_EXPORTER_OTLP_PROTOCOL.
	exp, err := otlptracegrpc.New(ctx)
	if err != nil {
		log.Printf("Falha ao criar exporter OTLP (seguindo sem tracing): %v", err)
		return func(context.Context) error { return nil }
	}

	res, err := resource.New(ctx, resource.WithFromEnv(), resource.WithTelemetrySDK(), resource.WithHost())
	if err != nil {
		log.Printf("Falha ao criar resource OTel: %v", err)
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	log.Println("OpenTelemetry tracing habilitado.")
	return tp.Shutdown
}

// newHTTPHandler envolve o mux com o otelhttp. /health não gera span, e o nome
// do span usa o padrão registrado no mux (não o caminho cru), para que paths
// desconhecidos não criem nomes ilimitados no Datadog.
func newHTTPHandler(mux *http.ServeMux, operation string) http.Handler {
	return otelhttp.NewHandler(mux, operation,
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/health"
		}),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			_, pattern := mux.Handler(r)
			if pattern == "" {
				pattern = "unmatched"
			}
			return r.Method + " " + pattern
		}),
	)
}

// markSpanError registra o erro no span atual e o marca como falho.
// RecordError sozinho não muda o status: sem SetStatus o span aparece como OK.
func markSpanError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
