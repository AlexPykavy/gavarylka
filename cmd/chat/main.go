package main

import (
	"bytes"
	"chat-system/internal/logging"
	"chat-system/internal/telemetry"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	_ "github.com/lib/pq" // PostgreSQL driver
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type CreateMessageRequest struct {
	ID                string `json:"id"`
	AuthorID          string `json:"author_id"`
	DestinationUserID string `json:"destination_user_id"`
	Message           string `json:"message"`
}

type Message struct {
	ID                string    `json:"id"`
	AuthorID          string    `json:"author_id"`
	DestinationUserID string    `json:"destination_user_id"`
	Message           string    `json:"message"`
	CreatedAt         time.Time `json:"created_at"`
}

const (
	KafkaTopic = "chat-messages"
)

type Handler struct {
	db            *sql.DB
	kafkaProducer sarama.SyncProducer
	logger        *logging.Logger
}

// TODO: play with other ways to return a list of messages
// For instance, some HTTP stream or gRPC stream
// Give a try to application/x-ndjson
func (h *Handler) HandleGetUserMessages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID := r.PathValue("user_id")

	rows, err := h.db.QueryContext(
		ctx,
		"SELECT id, author_id, destination_user_id, message, created_at FROM messages WHERE destination_user_id = $1 ORDER BY created_at ASC",
		userID)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to fetch messages from the database", "error", err, "user_id", userID)
		http.Error(w, "Failed to fetch messages", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message

		if err := rows.Scan(
			&msg.ID,
			&msg.AuthorID,
			&msg.DestinationUserID,
			&msg.Message,
			&msg.CreatedAt,
		); err != nil {
			h.logger.ErrorContext(ctx, "failed to scan message from the database", "error", err, "user_id", userID)
			http.Error(w, "Failed to fetch messages", http.StatusInternalServerError)
			return
		}

		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(ctx, "error occurred during rows iteration", "error", err, "user_id", userID)
		http.Error(w, "Failed to fetch messages", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

func (h *Handler) HandleSendMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	span := trace.SpanFromContext(ctx)

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	var msg CreateMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	span.SetAttributes(attribute.String("message.id", msg.ID))

	var buf bytes.Buffer
	err := json.NewEncoder(&buf).Encode(msg)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to encode message to JSON", "error", err, "message_id", msg.ID)

		http.Error(w, "Failed to encode message", http.StatusInternalServerError)
		return
	}

	kafkaMsg := &sarama.ProducerMessage{
		Topic: KafkaTopic,
		Key:   sarama.StringEncoder(msg.DestinationUserID),
		Value: sarama.ByteEncoder(buf.Bytes()),
	}
	for key, value := range carrier {
		kafkaMsg.Headers = append(kafkaMsg.Headers, sarama.RecordHeader{
			Key:   []byte(key),
			Value: []byte(value),
		})
	}

	partition, offset, err := h.kafkaProducer.SendMessage(kafkaMsg)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to send message to Kafka", "error", err, "message_id", msg.ID, "topic", KafkaTopic, "partition", partition, "offset", offset)

		http.Error(w, "Failed to send message to Kafka", http.StatusInternalServerError)
		return
	}

	h.logger.InfoContext(ctx, "message successfully sent to Kafka", "message_id", msg.ID, "topic", KafkaTopic, "partition", partition, "offset", offset)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Message sent successfully"))
}

func main() {
	logger := logging.New(logging.NewTraceHandler(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}),
	))

	postgresDSN := os.Getenv("POSTGRES_DSN")
	if postgresDSN == "" {
		logger.Fatal("environment variable is not set", "var", "POSTGRES_DSN")
	}

	kafkaBroker := os.Getenv("KAFKA_BROKER")
	if kafkaBroker == "" {
		logger.Fatal("environment variable is not set", "var", "KAFKA_BROKER")
	}

	otlpEndpoint := os.Getenv("OTLP_ENDPOINT")
	if otlpEndpoint == "" {
		logger.Fatal("environment variable is not set", "var", "OTLP_ENDPOINT")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.InitTracer(ctx, otlpEndpoint, "chat-service")
	if err != nil {
		logger.Fatal("failed to initialize OpenTelemetry tracer", "error", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := shutdown(shutdownCtx); err != nil {
			logger.Error("error occurred during OTel shutdown", "error", err)
		}
	}()

	db, err := sql.Open("postgres", postgresDSN)
	if err != nil {
		logger.Fatal("failed to connect to the database", "error", err)
	}
	defer db.Close()

	kafkaProducer, err := initKafkaProducer(kafkaBroker)
	if err != nil {
		logger.Fatal("failed to initialize Kafka producer", "error", err)
	}
	defer kafkaProducer.Close()

	handler := &Handler{
		kafkaProducer: kafkaProducer,
		logger:        logger,
		db:            db,
	}

	router := http.NewServeMux()
	router.HandleFunc("GET /users/{user_id}/messages", handler.HandleGetUserMessages)
	router.HandleFunc("POST /messages", handler.HandleSendMessage)

	logger.Info("starting Chat service", "port", 8080)
	if err := http.ListenAndServe(":8080", otelhttp.NewHandler(
		router,
		"chat-service",
		otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
			return r.Method + " " + r.Pattern
		}),
	)); err != nil {
		logger.Fatal("could not start server", "error", err)
	}
}

func initKafkaProducer(broker string) (sarama.SyncProducer, error) {
	config := sarama.NewConfig()
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Retry.Max = 5
	config.Producer.Return.Successes = true

	producer, err := sarama.NewSyncProducer([]string{broker}, config)
	if err != nil {
		return nil, err
	}
	return producer, nil
}
