package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"mendry/backend/internal/modules/notifications/domain"
)

type Database interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Enqueuer joins the caller-owned PostgreSQL transaction and never sends network requests.
type Enqueuer interface {
	Enqueue(context.Context, pgx.Tx, domain.Event) error
}

type Store struct {
	db        Database
	publicURL string
}

func NewStore(db Database, publicURL string) *Store {
	return &Store{db: db, publicURL: strings.TrimRight(publicURL, "/")}
}

const channelColumns = `id::text, project_id::text, name, platform, enabled, ciphertext, nonce, created_at, updated_at`

func scanChannel(row pgx.Row) (domain.Channel, error) {
	var c domain.Channel
	err := row.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Platform, &c.Enabled, &c.Ciphertext, &c.Nonce, &c.CreatedAt, &c.UpdatedAt)
	c.HasCredentials = len(c.Ciphertext) > 0
	if errors.Is(err, pgx.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return c, err
}
func (s *Store) ListChannels(ctx context.Context, projectID string) ([]domain.Channel, error) {
	rows, err := s.db.Query(ctx, `SELECT `+channelColumns+` FROM notification_channels WHERE project_id=$1 ORDER BY created_at,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Channel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *Store) GetChannel(ctx context.Context, projectID, id string) (domain.Channel, error) {
	return scanChannel(s.db.QueryRow(ctx, `SELECT `+channelColumns+` FROM notification_channels WHERE project_id=$1 AND id=$2`, projectID, id))
}
func (s *Store) SaveChannel(ctx context.Context, c domain.Channel, create bool) (domain.Channel, error) {
	if create {
		return scanChannel(s.db.QueryRow(ctx, `INSERT INTO notification_channels(id,project_id,name,platform,enabled,ciphertext,nonce) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+channelColumns, c.ID, c.ProjectID, c.Name, c.Platform, c.Enabled, c.Ciphertext, c.Nonce))
	}
	var saved domain.Channel
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		saved, err = scanChannel(tx.QueryRow(ctx, `UPDATE notification_channels SET name=$3,enabled=$4,ciphertext=$5,nonce=$6,updated_at=now() WHERE project_id=$1 AND id=$2 RETURNING `+channelColumns, c.ProjectID, c.ID, c.Name, c.Enabled, c.Ciphertext, c.Nonce))
		if err != nil || saved.Enabled {
			return err
		}
		// A separate READ COMMITTED statement sees jobs committed while the channel lock was awaited.
		_, err = tx.Exec(ctx, `UPDATE notification_deliveries SET state='cancelled',lease_token=NULL,lease_until=NULL WHERE project_id=$1 AND channel_id=$2 AND state IN ('pending','sending')`, c.ProjectID, c.ID)
		return err
	})
	return saved, err
}
func (s *Store) DeleteChannel(ctx context.Context, projectID, id string) error {
	return pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM notification_channels WHERE project_id=$1 AND id=$2`, projectID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		_, err = tx.Exec(ctx, `UPDATE notification_deliveries SET state='cancelled',lease_token=NULL,lease_until=NULL WHERE project_id=$1 AND channel_id=$2 AND state IN ('pending','sending')`, projectID, id)
		return err
	})
}

// Enqueue snapshots all enabled channels inside the business transaction. Network delivery is separate.
func (s *Store) Enqueue(ctx context.Context, tx pgx.Tx, e domain.Event) error {
	var projectID, projectKey, title, priority string
	var number, generation int64
	if e.Kind == "result" {
		err := tx.QueryRow(ctx, `SELECT s.incident_id::text,s.lifecycle_generation FROM remediation_run r JOIN remediation_series s ON s.id=r.series_id WHERE r.id=$1`, e.RunID).Scan(&e.IncidentID, &e.Generation)
		if err != nil {
			return fmt.Errorf("resolve notification run: %w", err)
		}
		var previousResult bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM remediation_run earlier JOIN remediation_series series ON series.id=earlier.series_id
WHERE series.incident_id=$1 AND series.lifecycle_generation=$2 AND earlier.id<>$3
AND (earlier.state IN ('completed_non_code','blocked_manual_review','failed','budget_exhausted','awaiting_human_review')
OR (earlier.state='diagnosis_ready_for_review' AND (earlier.execution_mode<>'auto_hotfix' OR earlier.analysis_only))))`, e.IncidentID, e.Generation, e.RunID).Scan(&previousResult); err != nil {
			return err
		}
		if previousResult {
			return nil
		}
	}
	err := tx.QueryRow(ctx, `SELECT i.project_id::text,p.project_key,i.incident_number,i.title,i.priority,i.lifecycle_generation FROM incidents i JOIN projects p ON p.id=i.project_id WHERE i.id=$1`, e.IncidentID).Scan(&projectID, &projectKey, &number, &title, &priority, &generation)
	if err != nil {
		return fmt.Errorf("resolve notification incident: %w", err)
	}
	if generation != e.Generation {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT `+channelColumns+` FROM notification_channels WHERE project_id=$1 AND enabled ORDER BY id FOR SHARE`, projectID)
	if err != nil {
		return err
	}
	channels := []domain.Channel{}
	for rows.Next() {
		c, scanErr := scanChannel(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		channels = append(channels, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(channels) == 0 {
		return nil
	}
	eventID := uuid.NewString()
	heading := "New incident"
	if e.Kind == "result" {
		heading = "AI result: " + e.State
	}
	// Only identifiers and the bounded incident title leave the system, never logs or model reasoning.
	message := fmt.Sprintf("%s\nProject: %s\nIncident #%d [%s]: %s\nLifecycle: %d", heading, projectKey, number, priority, bound(title, 500), e.Generation)
	if e.Kind == "result" {
		var attempt int
		if err := tx.QueryRow(ctx, `SELECT attempt_number FROM remediation_run WHERE id=$1`, e.RunID).Scan(&attempt); err != nil {
			return err
		}
		message += fmt.Sprintf("\nAttempt: %d\nAI processing has stopped; this does not imply incident recovery.", attempt)
	}
	if s.publicURL != "" {
		message += "\n" + s.publicURL + "/projects/" + url.PathEscape(projectKey) + "/incidents/INC-" + fmt.Sprint(number)
	}
	key := fmt.Sprintf("%s:%s:%d", e.Kind, e.IncidentID, e.Generation)
	tag, err := tx.Exec(ctx, `INSERT INTO notification_events(id,project_id,incident_id,incident_number,generation,kind,deduplication_key,message) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(deduplication_key) DO NOTHING`, eventID, projectID, e.IncidentID, number, e.Generation, e.Kind, key, message)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	for _, c := range channels {
		_, err = tx.Exec(ctx, `INSERT INTO notification_deliveries(id,event_id,project_id,channel_id,channel_name,platform,ciphertext,nonce) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.NewString(), eventID, projectID, c.ID, c.Name, c.Platform, c.Ciphertext, c.Nonce)
		if err != nil {
			return err
		}
	}
	return nil
}
func bound(value string, max int) string {
	r := []rune(value)
	if len(r) > max {
		return string(r[:max])
	}
	return value
}

const deliveryColumns = `d.id::text,d.project_id::text,d.channel_id::text,d.channel_name,d.platform,e.kind,e.incident_number,e.generation,d.state,d.attempts,d.last_error,d.created_at,d.next_attempt_at,d.delivered_at,d.ciphertext,d.nonce,e.message,COALESCE(d.lease_token::text,'')`

func scanDelivery(row pgx.Row) (domain.Delivery, error) {
	var d domain.Delivery
	err := row.Scan(&d.ID, &d.ProjectID, &d.ChannelID, &d.ChannelName, &d.Platform, &d.Kind, &d.IncidentNumber, &d.Generation, &d.State, &d.Attempts, &d.LastError, &d.CreatedAt, &d.NextAttemptAt, &d.DeliveredAt, &d.Ciphertext, &d.Nonce, &d.Message, &d.LeaseToken)
	if errors.Is(err, pgx.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return d, err
}
func (s *Store) ListDeliveries(ctx context.Context, projectID string) ([]domain.Delivery, error) {
	rows, err := s.db.Query(ctx, `SELECT `+deliveryColumns+` FROM notification_deliveries d JOIN notification_events e ON e.id=d.event_id WHERE d.project_id=$1 ORDER BY d.sequence DESC LIMIT 100`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Delivery{}
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

const claimedDeliveryColumns = `d.id::text,d.project_id::text,d.channel_id::text,d.channel_name,c.platform,e.kind,e.incident_number,e.generation,d.state,d.attempts,d.last_error,d.created_at,d.next_attempt_at,d.delivered_at,c.ciphertext,c.nonce,e.message,COALESCE(d.lease_token::text,'')`

func (s *Store) Claim(ctx context.Context) (domain.Delivery, error) {
	// Only pending/sending predecessors gate ordering; failed/cancelled jobs cannot starve results.
	return scanDelivery(s.db.QueryRow(ctx, `WITH candidate AS (
 SELECT d.id FROM notification_deliveries d JOIN notification_events e ON e.id=d.event_id
 JOIN notification_channels c ON c.id=d.channel_id AND c.project_id=d.project_id AND c.enabled
 WHERE ((d.state='pending' AND d.next_attempt_at<=now()) OR (d.state='sending' AND d.lease_until<=now()))
 AND NOT EXISTS(SELECT 1 FROM notification_deliveries older JOIN notification_events oe ON oe.id=older.event_id
 WHERE older.channel_id=d.channel_id AND older.sequence<d.sequence AND oe.incident_id=e.incident_id AND oe.generation=e.generation AND older.state IN ('pending','sending'))
 ORDER BY d.sequence FOR UPDATE OF d SKIP LOCKED FOR SHARE OF c SKIP LOCKED LIMIT 1
 ), claimed AS (
 UPDATE notification_deliveries d SET state='sending',attempts=attempts+1,lease_token=$1,lease_until=now()+interval '60 seconds' FROM candidate c WHERE d.id=c.id RETURNING d.*
 ) SELECT `+claimedDeliveryColumns+` FROM claimed d JOIN notification_events e ON e.id=d.event_id
 JOIN notification_channels c ON c.id=d.channel_id AND c.project_id=d.project_id AND c.enabled`, uuid.NewString()))
}
func (s *Store) Finish(ctx context.Context, d domain.Delivery, sendErr error) error {
	state, last := "delivered", ""
	var delay time.Duration
	if sendErr != nil {
		state = "pending"
		last = "platform_delivery_failed"
		delay = time.Duration(1<<min(d.Attempts, 10)) * time.Second
		if d.Attempts >= 12 {
			state = "failed"
		}
	}
	tag, err := s.db.Exec(ctx, `UPDATE notification_deliveries SET state=$3,last_error=$4,next_attempt_at=now()+($5*interval '1 second'),delivered_at=CASE WHEN $3='delivered' THEN now() ELSE NULL END,lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_token=$2 AND state='sending'`, d.ID, d.LeaseToken, state, last, int(delay.Seconds()))
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	return err
}
func (s *Store) RetryDelivery(ctx context.Context, projectID, id string) error {
	tag, err := s.db.Exec(ctx, `WITH current_channel AS (
 SELECT c.id,c.project_id FROM notification_channels c JOIN notification_deliveries d ON d.channel_id=c.id AND d.project_id=c.project_id
 WHERE d.project_id=$1 AND d.id=$2 AND c.enabled FOR SHARE OF c
) UPDATE notification_deliveries d SET state='pending',attempts=0,last_error='',next_attempt_at=now(),lease_token=NULL,lease_until=NULL FROM current_channel c WHERE d.project_id=$1 AND d.id=$2 AND d.state='failed' AND c.id=d.channel_id AND c.project_id=d.project_id`, projectID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	return err
}
