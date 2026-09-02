-- name: CreateUser :one
INSERT INTO users (id, username, password_hash, role)
VALUES ($1, $2, $3, $4)
RETURNING id, username, password_hash, role, enabled, version, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, username, password_hash, role, enabled, version, created_at, updated_at
FROM users
WHERE id = $1;

-- name: GetUserByUsername :one
SELECT id, username, password_hash, role, enabled, version, created_at, updated_at
FROM users
WHERE username = $1;
