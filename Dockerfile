FROM golang:1.23.6 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

ARG PROJECT
COPY ./internal ./internal
COPY ./cmd/$PROJECT ./cmd/$PROJECT
RUN CGO_ENABLED=0 GOOS=linux go build -o main ./cmd/$PROJECT


FROM alpine:latest

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /home/appuser/
COPY --from=builder /app/main .
RUN chown -R appuser:appgroup /home/appuser

USER appuser
EXPOSE 8080
CMD ["/home/appuser/main"]