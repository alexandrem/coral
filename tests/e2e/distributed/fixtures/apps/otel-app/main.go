// #nosec
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	tracer trace.Tracer
	meter  metric.Meter

	// Metrics.
	requestCounter  metric.Int64Counter
	requestDuration metric.Float64Histogram
)

func main() {
	// Setup OpenTelemetry.
	ctx := context.Background()

	// Get OTLP endpoint from environment or use default.
	otlpEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otlpEndpoint == "" {
		otlpEndpoint = "localhost:4317"
	}

	shutdown, err := initOTel(ctx, otlpEndpoint)
	if err != nil {
		log.Fatalf("Failed to initialize OpenTelemetry: %v", err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()

	// Create tracer and meter.
	tracer = otel.Tracer("otel-app")
	meter = otel.Meter("otel-app")

	// Initialize metrics.
	requestCounter, err = meter.Int64Counter(
		"http.server.requests",
		metric.WithDescription("Total number of HTTP requests"),
		metric.WithUnit("{requests}"),
	)
	if err != nil {
		log.Fatalf("Failed to create counter: %v", err)
	}

	requestDuration, err = meter.Float64Histogram(
		"http.server.duration",
		metric.WithDescription("HTTP request duration"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		log.Fatalf("Failed to create histogram: %v", err)
	}

	// Get HTTP port from environment or use default.
	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "8080"
	}

	// Setup HTTP server.
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/api/users", handleUsers)
	mux.HandleFunc("/api/products", handleProducts)
	mux.HandleFunc("/api/checkout", handleCheckout)
	mux.HandleFunc("/health", handleHealth)

	server := &http.Server{
		Addr:    ":" + httpPort,
		Handler: mux,
	}

	// Start server in goroutine.
	go func() {
		log.Printf("Starting server on %s (OTLP endpoint: %s)", server.Addr, otlpEndpoint)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
}

// initOTel initializes the OpenTelemetry SDK.
func initOTel(ctx context.Context, endpoint string) (shutdown func(context.Context) error, err error) {
	// Create resource with service information.
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("otel-app"),
			semconv.ServiceVersionKey.String("1.0.0"),
			attribute.String("environment", "test"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create OTLP trace exporter.
	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithTLSCredentials(insecure.NewCredentials()),
		otlptracegrpc.WithDialOption(grpc.WithBlock()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	// Create trace provider.
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tracerProvider)

	// Create meter provider.
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)

	// Return shutdown function.
	shutdown = func(ctx context.Context) error {
		if err := tracerProvider.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown tracer provider: %w", err)
		}
		if err := meterProvider.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown meter provider: %w", err)
		}
		return nil
	}

	return shutdown, nil
}

// instrumentHandler wraps an HTTP handler with OpenTelemetry instrumentation.
func instrumentHandler(name string, handler func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create span.
		ctx, span := tracer.Start(r.Context(), name,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPMethodKey.String(r.Method),
				semconv.HTTPTargetKey.String(r.URL.Path),
				semconv.HTTPRouteKey.String(r.URL.Path),
			),
		)
		defer span.End()

		// Create response writer wrapper to capture status code.
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Call actual handler.
		handler(rw, r.WithContext(ctx))

		// Record metrics.
		duration := float64(time.Since(start).Milliseconds())
		attrs := []attribute.KeyValue{
			attribute.String("http.method", r.Method),
			attribute.String("http.route", r.URL.Path),
			attribute.Int("http.status_code", rw.statusCode),
		}

		requestCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
		requestDuration.Record(ctx, duration, metric.WithAttributes(attrs...))

		// Add status code to span.
		span.SetAttributes(semconv.HTTPStatusCodeKey.Int(rw.statusCode))

		// Mark span as error if status code indicates an error.
		if rw.statusCode >= 400 {
			span.SetAttributes(attribute.Bool("error", true))
		}
	}
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// HTTP Handlers.

func handleRoot(w http.ResponseWriter, r *http.Request) {
	instrumentHandler("GET /", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		_, span := tracer.Start(ctx, "process-root")
		defer span.End()

		time.Sleep(time.Duration(rand.Intn(50)) * time.Millisecond)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status": "ok", "service": "otel-app"}`)
	})(w, r)
}

func handleUsers(w http.ResponseWriter, r *http.Request) {
	instrumentHandler("GET /api/users", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Simulate database query.
		_, span := tracer.Start(ctx, "database.query",
			trace.WithAttributes(
				attribute.String("db.system", "postgresql"),
				attribute.String("db.operation", "SELECT"),
			),
		)
		time.Sleep(time.Duration(20+rand.Intn(30)) * time.Millisecond)
		span.End()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"users": [{"id": 1, "name": "Alice"}, {"id": 2, "name": "Bob"}]}`)
	})(w, r)
}

func handleProducts(w http.ResponseWriter, r *http.Request) {
	instrumentHandler("GET /api/products", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Simulate cache lookup.
		_, cacheSpan := tracer.Start(ctx, "cache.get",
			trace.WithAttributes(attribute.String("cache.key", "products")),
		)
		time.Sleep(time.Duration(5+rand.Intn(10)) * time.Millisecond)
		cacheSpan.End()

		// Simulate database query.
		_, dbSpan := tracer.Start(ctx, "database.query",
			trace.WithAttributes(
				attribute.String("db.system", "postgresql"),
				attribute.String("db.operation", "SELECT"),
			),
		)
		time.Sleep(time.Duration(30+rand.Intn(40)) * time.Millisecond)
		dbSpan.End()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"products": [{"id": 1, "name": "Widget"}, {"id": 2, "name": "Gadget"}]}`)
	})(w, r)
}

func handleCheckout(w http.ResponseWriter, r *http.Request) {
	instrumentHandler("POST /api/checkout", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Simulate various checkout steps with varying latency.
		steps := []struct {
			name     string
			minMs    int
			maxMs    int
			errorPct int
		}{
			{"validate-cart", 10, 30, 5},
			{"check-inventory", 20, 100, 10},
			{"process-payment", 50, 500, 15},
			{"update-inventory", 20, 80, 5},
			{"send-confirmation", 30, 100, 5},
		}

		for _, step := range steps {
			_, stepSpan := tracer.Start(ctx, step.name)

			// Simulate latency.
			latency := time.Duration(step.minMs+rand.Intn(step.maxMs-step.minMs)) * time.Millisecond
			time.Sleep(latency)

			// Randomly inject errors.
			if rand.Intn(100) < step.errorPct {
				stepSpan.SetAttributes(attribute.Bool("error", true))
				stepSpan.End()
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, `{"error": "Checkout failed at step: %s"}`, step.name)
				return
			}

			stepSpan.End()
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status": "success", "order_id": "%d"}`, rand.Intn(10000))
	})(w, r)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status": "healthy"}`)
}
