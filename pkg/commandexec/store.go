package commandexec

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Record struct {
	MessageID      string
	FromAddr       string
	Subject        string
	Command        string
	Stdout         string
	Stderr         string
	ExitCode       int
	ExecutedAt     time.Time
	ResponseSentAt sql.NullTime
}

func NewStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create execution db directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open execution db: %w", err)
	}

	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) init() error {
	const schema = `
CREATE TABLE IF NOT EXISTS executed_messages (
  message_id TEXT PRIMARY KEY,
  from_addr TEXT NOT NULL,
  subject TEXT NOT NULL,
  command_text TEXT NOT NULL,
  stdout_text TEXT NOT NULL,
  stderr_text TEXT NOT NULL,
  exit_code INTEGER NOT NULL,
  executed_at TEXT NOT NULL,
  response_sent_at TEXT
);
`
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("create execution schema: %w", err)
	}
	return nil
}

func (s *Store) HasExecuted(ctx context.Context, messageID string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM executed_messages WHERE message_id = ?`, messageID).Scan(&count); err != nil {
		return false, fmt.Errorf("check execution record %s: %w", messageID, err)
	}
	return count > 0, nil
}

func (s *Store) GetRecord(ctx context.Context, messageID string) (Record, bool, error) {
	var record Record
	var executedAt string
	var responseSentAt sql.NullString
	err := s.db.QueryRowContext(
		ctx,
		`SELECT message_id, from_addr, subject, command_text, stdout_text, stderr_text, exit_code, executed_at, response_sent_at
		 FROM executed_messages
		 WHERE message_id = ?`,
		messageID,
	).Scan(
		&record.MessageID,
		&record.FromAddr,
		&record.Subject,
		&record.Command,
		&record.Stdout,
		&record.Stderr,
		&record.ExitCode,
		&executedAt,
		&responseSentAt,
	)
	switch {
	case err == nil:
		record.ExecutedAt, err = time.Parse(time.RFC3339Nano, executedAt)
		if err != nil {
			return Record{}, false, fmt.Errorf("parse executed_at for %s: %w", messageID, err)
		}
		if responseSentAt.Valid {
			parsed, err := time.Parse(time.RFC3339Nano, responseSentAt.String)
			if err != nil {
				return Record{}, false, fmt.Errorf("parse response_sent_at for %s: %w", messageID, err)
			}
			record.ResponseSentAt = sql.NullTime{Time: parsed, Valid: true}
		}
		return record, true, nil
	case err == sql.ErrNoRows:
		return Record{}, false, nil
	default:
		return Record{}, false, fmt.Errorf("get execution record %s: %w", messageID, err)
	}
}

func (s *Store) SaveExecution(ctx context.Context, record Record) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO executed_messages (message_id, from_addr, subject, command_text, stdout_text, stderr_text, exit_code, executed_at, response_sent_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(message_id) DO UPDATE SET
		   from_addr=excluded.from_addr,
		   subject=excluded.subject,
		   command_text=excluded.command_text,
		   stdout_text=excluded.stdout_text,
		   stderr_text=excluded.stderr_text,
		   exit_code=excluded.exit_code,
		   executed_at=excluded.executed_at,
		   response_sent_at=COALESCE(executed_messages.response_sent_at, excluded.response_sent_at)`,
		record.MessageID,
		record.FromAddr,
		record.Subject,
		record.Command,
		record.Stdout,
		record.Stderr,
		record.ExitCode,
		record.ExecutedAt.UTC().Format(time.RFC3339Nano),
		nullTimeArg(record.ResponseSentAt),
	)
	if err != nil {
		return fmt.Errorf("save execution record %s: %w", record.MessageID, err)
	}
	return nil
}

func (s *Store) MarkResponseSent(ctx context.Context, messageID string, sentAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE executed_messages SET response_sent_at = ? WHERE message_id = ?`, sentAt.UTC().Format(time.RFC3339Nano), messageID)
	if err != nil {
		return fmt.Errorf("mark response sent for %s: %w", messageID, err)
	}
	return nil
}

func nullTimeArg(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}
