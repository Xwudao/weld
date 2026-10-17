-- name: CreateUser :one
INSERT INTO users (username, password_hash, roles)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: SetUserRoles :one
UPDATE users
SET roles = $2, updated_at = now()
WHERE id = $1
RETURNING *;
