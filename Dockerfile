FROM golang:1.23.6 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

ARG PROJECT
COPY ./internal ./internal
COPY ./cmd/$PROJECT ./cmd/$PROJECT
RUN CGO_ENABLED=0 GOOS=linux go build -o $PROJECT ./cmd/$PROJECT


FROM alpine:latest

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /home/appuser/
ARG PROJECT
COPY --from=builder /app/$PROJECT .
RUN chown -R appuser:appgroup /home/appuser

USER appuser
EXPOSE 8080
ENV PROJECT=$PROJECT
CMD ["/bin/sh", "-ec", "exec /home/appuser/$PROJECT"]
