package repository

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/stretchr/testify/require"
)

// Runs the actual host User repository and Ent query stack over an isolated
// SQL driver. No server, credentials or PostgreSQL process is involved.
func TestWebCodexHostUserEntAdapter(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "deleted context cannot grant authority"}[deleted], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			users := NewUserRepository(client, db)
			resolve := NewWebCodexHostUserResolver(users)
			ctx := context.Background()
			var deletedAt any
			if deleted {
				ctx = mixins.SkipSoftDelete(ctx)
				deletedAt = time.Unix(5, 0)
			}
			mock.ExpectQuery(`SELECT .* FROM "users" WHERE "users"\."id" = \$1`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "status", "username", "deleted_at"}).AddRow(int64(7), "active", "mutable profile", deletedAt))
			mock.ExpectQuery(`SELECT .* FROM "user_allowed_groups" WHERE "user_allowed_groups"\."user_id" IN \(\$1\)`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "group_id"}))
			identity, err := resolve(ctx, 7)
			require.NoError(t, err)
			require.Equal(t, int64(7), identity.ID)
			require.Equal(t, !deleted, identity.Active)
		})
	}
}
