package main

import (
	"bytes"
	"chat-system/internal/logging"
	"chat-system/internal/telemetry"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-redis/redis/v8"
	_ "github.com/lib/pq" // PostgreSQL driver
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	KafkaTopic         = "chat-messages"
	KafkaConsumerGroup = "my-message-router"
)

type Message struct {
	ID                string    `json:"id"`
	AuthorID          string    `json:"author_id"`
	DestinationUserID string    `json:"destination_user_id"`
	Message           string    `json:"message"`
	CreatedAt         time.Time `json:"created_at"`
}

type Handler struct {
	db          *sql.DB
	logger      *logging.Logger
	redisClient *redis.Client
	tracer      trace.Tracer
}

func (h *Handler) Setup(sarama.ConsumerGroupSession) error {
	return nil
}

func (h *Handler) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}

func (h *Handler) ConsumeClaim(
	session sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
) error {
	for {
		select {
		case <-session.Context().Done():
			return session.Context().Err()

		case kafkaMsg, ok := <-claim.Messages():
			if !ok {
				return nil
			}

			if err := h.processMessage(session.Context(), kafkaMsg); err != nil {
				h.logger.ErrorContext(session.Context(), "failed to process message", "error", err, "topic", kafkaMsg.Topic, "partition", kafkaMsg.Partition, "offset", kafkaMsg.Offset, "key", string(kafkaMsg.Key))
			}

			session.MarkMessage(kafkaMsg, "")
		}
	}
}

func (h *Handler) processMessage(
	ctx context.Context,
	kafkaMsg *sarama.ConsumerMessage,
) error {
	carrier := propagation.MapCarrier{}
	for _, header := range kafkaMsg.Headers {
		carrier[string(header.Key)] = string(header.Value)
	}

	ctx, span := h.tracer.Start(
		otel.GetTextMapPropagator().Extract(
			ctx,
			carrier,
		),
		"Kafka Consume",
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", kafkaMsg.Topic),
			attribute.Int64("messaging.kafka.partition", int64(kafkaMsg.Partition)),
			attribute.Int64("messaging.kafka.offset", kafkaMsg.Offset),
		),
	)
	defer span.End()

	var msg Message
	if err := json.Unmarshal(kafkaMsg.Value, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %w", err)
	}

	span.SetAttributes(attribute.String("message.id", msg.ID))

	err := h.db.QueryRowContext(
		ctx,
		"INSERT INTO messages(id, author_id, destination_user_id, message) VALUES ($1, $2, $3, $4) RETURNING created_at",
		msg.ID, msg.AuthorID, msg.DestinationUserID, msg.Message).Scan(&msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("Failed to insert message into PostgreSQL: %w", err)
	}

	h.logger.InfoContext(ctx, "message inserted into PostgreSQL", "message_id", msg.ID, "author_id", msg.AuthorID, "destination_user_id", msg.DestinationUserID)

	var wsServerUrl string
	if wsServerUrl, err = h.redisClient.Get(ctx, msg.DestinationUserID).Result(); err != nil {
		return fmt.Errorf("Failed to get WS server from the Redis: %w", err)
	}

	client := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(msg); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		wsServerUrl+"/send",
		&body,
	)
	if err != nil {
		return fmt.Errorf("Failed to create request to the WS server: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Failed to send message to the WS server: %w", err)
	}
	defer resp.Body.Close()

	h.logger.InfoContext(ctx, "message sent back to the websocket server", "message_id", msg.ID, "ws_server", wsServerUrl, "response_status_code", resp.StatusCode)

	return nil
}

func main() {
	logger := logging.New(logging.NewTraceHandler(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}),
	))

	redisUrl := os.Getenv("REDIS_URL")
	if redisUrl == "" {
		logger.Fatal("environment variable is not set", "var", "REDIS_URL")
	}

	kafkaBroker := os.Getenv("KAFKA_BROKER")
	if kafkaBroker == "" {
		logger.Fatal("environment variable is not set", "var", "KAFKA_BROKER")
	}

	postgresDSN := os.Getenv("POSTGRES_DSN")
	if postgresDSN == "" {
		logger.Fatal("environment variable is not set", "var", "POSTGRES_DSN")
	}

	otlpEndpoint := os.Getenv("OTLP_ENDPOINT")
	if otlpEndpoint == "" {
		logger.Fatal("environment variable is not set", "var", "OTLP_ENDPOINT")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.InitTracer(ctx, otlpEndpoint, "message-router")
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

	handler := &Handler{
		db:     db,
		logger: logger,
		redisClient: redis.NewClient(&redis.Options{
			Addr: redisUrl,
		}),
		tracer: otel.Tracer("message-router"),
	}

	kafkaConsumerGroup, err := initKafkaConsumerGroup(kafkaBroker)
	if err != nil {
		logger.Fatal("failed to initialize Kafka consumer", "error", err)
	}
	defer kafkaConsumerGroup.Close()

	for ctx.Err() == nil {
		if err := kafkaConsumerGroup.Consume(ctx, []string{KafkaTopic}, handler); err != nil {
			logger.Error("failed to consume Kafka messages", "error", err)
		}
	}
}

func initKafkaConsumerGroup(broker string) (sarama.ConsumerGroup, error) {
	config := sarama.NewConfig()
	config.Version = sarama.V4_0_0_0
	config.Consumer.Offsets.Initial = sarama.OffsetNewest

	consumer, err := sarama.NewConsumerGroup([]string{broker}, KafkaConsumerGroup, config)
	if err != nil {
		return nil, err
	}
	return consumer, nil
}
