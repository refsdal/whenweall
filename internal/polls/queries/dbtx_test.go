package queries_test

import (
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/polls/queries"
)

// Compile-time assertion: internal/db.DBTX satisfies sqlc's generated queries.DBTX, so
// queries.New(db.DBTX) — a *sql.DB or *sql.Tx — compiles.
var _ queries.DBTX = (db.DBTX)(nil)
