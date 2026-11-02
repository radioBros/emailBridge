package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // database/sql driver "sqlite" (pure Go, FTS5 compiled in)
)

// Connection settings (S12 §5). auto_vacuum comes first: it only takes effect before the first table is
// created. The pragmas are part of the DSN so every pooled connection gets them.
const (
	writerPragmas = "?_pragma=auto_vacuum(INCREMENTAL)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(5000)&_txlock=immediate"
	readerPragmas = "?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
)

// schema is applied to every new day file. seq is the stable integer rowid the external-content FTS5
// table needs; id is the public log_<ulid>.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS records (
		seq            INTEGER PRIMARY KEY,
		id             TEXT    NOT NULL UNIQUE,
		time           INTEGER NOT NULL,
		level          TEXT    NOT NULL,
		lvl            INTEGER NOT NULL,
		category       TEXT    NOT NULL,
		component      TEXT    NOT NULL DEFAULT '',
		node           TEXT,
		role           TEXT,
		msg            TEXT    NOT NULL,
		organizationId TEXT,
		accountId      TEXT,
		requestId      TEXT,
		traceId        TEXT,
		spanId         TEXT,
		eventId        TEXT,
		deliveryId     TEXT,
		outboundId     TEXT,
		actor          TEXT,
		jobId          TEXT,
		kind           TEXT,
		code           TEXT,
		attrs          TEXT,
		stack          TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS records_time ON records(time, id)`,
	`CREATE INDEX IF NOT EXISTS records_level_time ON records(lvl, time)`,
	`CREATE INDEX IF NOT EXISTS records_org_time ON records(organizationId, time)`,
	`CREATE INDEX IF NOT EXISTS records_account_time ON records(accountId, time)`,
	`CREATE INDEX IF NOT EXISTS records_request ON records(requestId)`,
	`CREATE INDEX IF NOT EXISTS records_trace ON records(traceId)`,
	`CREATE INDEX IF NOT EXISTS records_event ON records(eventId)`,
	`CREATE INDEX IF NOT EXISTS records_delivery ON records(deliveryId)`,
	`CREATE INDEX IF NOT EXISTS records_outbound ON records(outboundId)`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS records_fts USING fts5(msg, attrs, content='records', content_rowid='seq')`,
	`CREATE TRIGGER IF NOT EXISTS records_ai AFTER INSERT ON records BEGIN
		INSERT INTO records_fts(rowid, msg, attrs) VALUES (new.seq, new.msg, new.attrs);
	END`,
	`CREATE TRIGGER IF NOT EXISTS records_ad AFTER DELETE ON records BEGIN
		INSERT INTO records_fts(records_fts, rowid, msg, attrs) VALUES ('delete', old.seq, old.msg, old.attrs);
	END`,
	`CREATE TRIGGER IF NOT EXISTS records_au AFTER UPDATE ON records BEGIN
		INSERT INTO records_fts(records_fts, rowid, msg, attrs) VALUES ('delete', old.seq, old.msg, old.attrs);
		INSERT INTO records_fts(rowid, msg, attrs) VALUES (new.seq, new.msg, new.attrs);
	END`,
}

const insertSQL = `INSERT OR IGNORE INTO records (id, time, level, lvl, category, component, node, role, msg,
	organizationId, accountId, requestId, traceId, spanId, eventId, deliveryId, outboundId, actor, jobId,
	kind, code, attrs, stack) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// selectCols is the column list every read uses, in scanRecord order.
const selectCols = `SELECT id, time, level, category, component, node, role, msg, organizationId, accountId,
	requestId, traceId, spanId, eventId, deliveryId, outboundId, actor, jobId, kind, code, attrs, stack`

// ErrNoFTS5 means the SQLite build lacks the FTS5 module; the store refuses to start (S12 §5).
var ErrNoFTS5 = errors.New("logstore: the SQLite driver has no FTS5 module")

// checkFTS5 verifies the pinned driver build supports FTS5.
func checkFTS5(ctx context.Context) error {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return fmt.Errorf("logstore: open probe database: %w", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `CREATE VIRTUAL TABLE probe USING fts5(x)`); err != nil {
		return fmt.Errorf("%w: %w", ErrNoFTS5, err)
	}
	return nil
}

// createSchema applies the schema to a freshly opened writer connection.
func createSchema(ctx context.Context, db *sql.DB) error {
	for _, stmt := range schema {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("logstore: schema: %w", err)
		}
	}
	return nil
}
