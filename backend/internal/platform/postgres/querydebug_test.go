package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestInterpolateQueryRendersExecutableSQL(t *testing.T) {
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	statement := `-- name: GetProjectAccess :one
SELECT p.id
FROM projects AS p
WHERE p.project_key = $3
  AND ($1::boolean OR pm.user_id = $2)
  AND extra = $10`
	got := interpolateQuery(statement, []any{
		true,
		pgtype.UUID{Bytes: userID, Valid: true},
		"demo-key",
		nil, nil, nil, nil, nil, nil,
		"token-10",
	})
	want := `-- name: GetProjectAccess :one
SELECT p.id
FROM projects AS p
WHERE p.project_key = 'demo-key'
  AND (TRUE::boolean OR pm.user_id = '11111111-1111-1111-1111-111111111111')
  AND extra = 'token-10'`
	if got != want {
		t.Fatalf("interpolateQuery() = %q, want %q", got, want)
	}
}

func TestFormatQueryArgCoversCommonDriverValues(t *testing.T) {
	service := "service"
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		arg  any
		want string
	}{
		{name: "nil", arg: nil, want: "NULL"},
		{name: "nil string pointer", arg: (*string)(nil), want: "NULL"},
		{name: "string pointer", arg: &service, want: "'service'"},
		{name: "nil time pointer", arg: (*time.Time)(nil), want: "NULL"},
		{name: "time pointer", arg: &now, want: "'2026-08-18T12:00:00Z'"},
		{name: "false", arg: false, want: "FALSE"},
		{name: "int", arg: 42, want: "42"},
		{name: "quoted string", arg: "O'Reilly", want: "'O''Reilly'"},
		{name: "bytes", arg: []byte{0xde, 0xad}, want: `'\xdead'::bytea`},
		{name: "time", arg: time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC), want: "'2026-08-18T12:00:00Z'"},
		{name: "invalid uuid", arg: pgtype.UUID{}, want: "NULL"},
		{name: "invalid text", arg: pgtype.Text{}, want: "NULL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatQueryArg(test.arg); got != test.want {
				t.Fatalf("formatQueryArg(%v) = %q, want %q", test.arg, got, test.want)
			}
		})
	}
}
