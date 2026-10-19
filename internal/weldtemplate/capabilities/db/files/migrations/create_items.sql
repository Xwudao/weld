-- +goose Up
CREATE TABLE items (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    quantity   integer NOT NULL CHECK (quantity > 0),
    status     text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE items;
