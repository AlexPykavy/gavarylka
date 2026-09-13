# Gavarylka

This project is a simple chat system that utilizes various technologies to provide real-time messaging capabilities. It includes a WebSocket server for real-time communication, Redis for user mapping, an HTTP chat service for message handling, Kafka for message queuing, and PostgreSQL for message storage.

## Technologies Used

- **Go**: The programming language used for the application.
- **WebSocket**: For real-time communication between clients and the server.
- **Redis**: For mapping user IDs to WebSocket connections.
- **Kafka**: For message queuing and handling.
- **PostgreSQL**: For persistent message storage.

## Useful commands

```
make ws
> {"id":"9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6a","author_id":"1","destination_user_id":"1","message":"Hello!"}
```

```
make psql SQL="SELECT * FROM messages;"
```
