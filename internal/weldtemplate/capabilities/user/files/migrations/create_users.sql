-- +goose Up
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    roles         text[] NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_username_lowercase CHECK (username = lower(username)),
    CONSTRAINT users_username_not_blank CHECK (username <> ''),
    CONSTRAINT users_password_hash_not_blank CHECK (password_hash <> '')
);

-- +goose Down
DROP TABLE users;
