CREATE TABLE messages (
    id UUID PRIMARY KEY,
    author_id VARCHAR(255) NOT NULL,
    destination_user_id VARCHAR(255) NOT NULL,
    message TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);