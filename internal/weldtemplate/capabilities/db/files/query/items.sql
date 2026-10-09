-- name: CreateItem :one
INSERT INTO items (id, name, quantity, status)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetItem :one
SELECT * FROM items WHERE id = $1;

-- name: ListItems :many
SELECT * FROM items ORDER BY created_at, id LIMIT $1;
