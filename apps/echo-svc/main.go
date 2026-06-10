// echo-svc: a minimal OpenTelemetry-instrumented HTTP service used to show a
// genuine, multi-span distributed trace behind the gateway.
//
// It extracts the W3C trace context Traefik injects (so its server span nests
// under Traefik's span), and — if DOWNSTREAM_URL is set — calls another instance
// with an instrumented client (producing a client span + a downstream server
// span). Two instances wired orders -> inventory give a 4-span trace across
// three services (traefik -> orders -> inventory).
package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func initTracer(ctx context.Context, svc, endpoint string) (func(context.Context) error, error) {
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(svc)))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

func main() {
	ctx := context.Background()
	svc := env("OTEL_SERVICE_NAME", "echo-svc")
	endpoint := env("OTEL_EXPORTER_OTLP_ENDPOINT", "lgtm.observability.svc.cluster.local:4317")
	listen := env("LISTEN_ADDR", ":8080")
	downstream := env("DOWNSTREAM_URL", "")

	shutdown, err := initTracer(ctx, svc, endpoint)
	if err != nil {
		log.Fatalf("tracer init: %v", err)
	}
	defer func() { _ = shutdown(ctx) }()

	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{
			"service":     svc,
			"path":        r.URL.Path,
			"user":        r.Header.Get("X-User-Name"),
			"user_groups": r.Header.Get("X-User-Groups"),
			"traceparent": r.Header.Get("Traceparent"),
		}
		if downstream != "" {
			req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, downstream, nil)
			resp, err := client.Do(req)
			if err != nil {
				out["downstream_error"] = err.Error()
			} else {
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				var ds any
				_ = json.Unmarshal(body, &ds)
				out["downstream"] = ds
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	mux := http.NewServeMux()
	// otelhttp wraps the handler -> creates the server span from the incoming
	// traceparent, making it a child of Traefik's span.
	mux.Handle("/", otelhttp.NewHandler(handler, svc+" handle"))

	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("%s listening on %s (otlp=%s, downstream=%q)", svc, listen, endpoint, downstream)
	log.Fatal(srv.ListenAndServe())
}
