package main

import (
	"bytes"
	"chat-system/internal/logging"
	"chat-system/internal/telemetry"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
)

type Message struct {
	ID                string    `json:"id"`
	AuthorID          string    `json:"author_id"`
	DestinationUserID string    `json:"destination_user_id"`
	Message           string    `json:"message"`
	CreatedAt         time.Time `json:"created_at"`
}

type Handler struct {
	chatServiceUrl     string
	logger             *logging.Logger
	connections        sync.Map
	redisClient        *redis.Client
	tracer             trace.Tracer
	webSocketServerUrl string
}

func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to upgrade connection", "error", err)

		return
	}
	defer conn.Close()

	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		h.logger.ErrorContext(ctx, "user_id is required")
		return
	}

	if err := h.redisClient.Set(ctx, userID, h.webSocketServerUrl, 0).Err(); err != nil {
		h.logger.ErrorContext(ctx, "failed to register user in Redis", "error", err)
		return
	}
	h.connections.Store(userID, conn)

	h.logger.InfoContext(ctx, "user connected", "user_id", userID)

	client := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		h.chatServiceUrl+"/users/"+userID+"/messages",
		nil,
	)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to create request to chat service", "error", err, "user_id", userID)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to send message to chat service", "error", err, "user_id", userID)
		return
	}
	defer resp.Body.Close()

	var msgs []Message
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		h.logger.ErrorContext(ctx, "failed to decode messages from chat service", "error", err, "user_id", userID)
		return
	}

	for _, msg := range msgs {
		if err := conn.WriteJSON(msg); err != nil {
			h.logger.ErrorContext(ctx, "failed to send message to user", "error", err, "user_id", userID)
			return
		}
	}

	defer func() {
		h.connections.Delete(userID)
		h.redisClient.Del(ctx, userID)

		h.logger.InfoContext(ctx, "user disconnected", "user_id", userID)
	}()

	for {
		select {
		case <-ctx.Done():
			h.logger.InfoContext(ctx, "context canceled, closing connection", "user_id", userID)
			return
		default:
			ctx, span := h.tracer.Start(ctx, "ReadMessage")
			defer span.End()

			_, msg, err := conn.ReadMessage()
			if err != nil {
				h.logger.ErrorContext(ctx, "error reading message from user", "error", err, "user_id", userID)
				return
			}

			h.logger.InfoContext(ctx, "received message from user", "user_id", userID, "message", string(msg))

			client := &http.Client{
				Transport: otelhttp.NewTransport(http.DefaultTransport),
			}

			req, err := http.NewRequestWithContext(
				ctx,
				http.MethodPost,
				h.chatServiceUrl+"/messages",
				bytes.NewReader(msg),
			)
			if err != nil {
				h.logger.ErrorContext(ctx, "failed to create request to chat service", "error", err, "user_id", userID)
				return
			}

			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				h.logger.ErrorContext(ctx, "failed to send message to chat service", "error", err, "user_id", userID)
				return
			}
			defer resp.Body.Close()

			h.logger.InfoContext(ctx, "message sent to chat service", "user_id", userID, "response_status_code", resp.StatusCode)
		}
	}
}

func (h *Handler) HandleSendMessage(w http.ResponseWriter, r *http.Request) {
	_, span := h.tracer.Start(r.Context(), "SendMessage")
	defer span.End()

	var msg Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	span.SetAttributes(attribute.String("message.id", msg.ID))

	conn, exists := h.connections.Load(msg.DestinationUserID)
	if !exists {
		http.Error(w, "User not connected", http.StatusNotFound)
		return
	}

	wsConn, ok := conn.(*websocket.Conn)
	if !ok {
		http.Error(w, "Invalid WebSocket connection", http.StatusInternalServerError)
		return
	}

	if err := wsConn.WriteJSON(msg); err != nil {
		http.Error(w, "Failed to send message", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
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

	chatServiceUrl := os.Getenv("CHAT_SERVICE_URL")
	if chatServiceUrl == "" {
		logger.Fatal("environment variable is not set", "var", "CHAT_SERVICE_URL")
	}

	webSocketServerUrl := os.Getenv("WEBSOCKET_SERVER_URL")
	if webSocketServerUrl == "" {
		logger.Fatal("environment variable is not set", "var", "WEBSOCKET_SERVER_URL")
	}

	otlpEndpoint := os.Getenv("OTLP_ENDPOINT")
	if otlpEndpoint == "" {
		logger.Fatal("environment variable is not set", "var", "OTLP_ENDPOINT")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.InitTracer(ctx, otlpEndpoint, "websocket-server")
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

	handler := &Handler{
		chatServiceUrl: chatServiceUrl,
		logger:         logger,
		redisClient: redis.NewClient(&redis.Options{
			Addr: redisUrl,
		}),
		tracer:             otel.Tracer("websocket-server"),
		webSocketServerUrl: webSocketServerUrl,
	}

	router := http.NewServeMux()
	router.HandleFunc("GET /ws", handler.HandleWebSocket)
	router.HandleFunc("POST /send", handler.HandleSendMessage)

	logger.Info("starting WebSocket server", "port", 8080)
	if err := http.ListenAndServe(":8080", otelhttp.NewHandler(
		router,
		"websocket-server",
		otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
			return r.Method + " " + r.Pattern
		}),
	)); err != nil {
		logger.Fatal("could not start server", "error", err)
	}
}
