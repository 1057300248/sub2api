//go:build integration

package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClinePostgresCreateOutboxFailureIsAtomic(t *testing.T) {
	ctx := context.Background()
	repo, _ := clinePostgresAccount(t)
	name := fmt.Sprintf("cline_create_%d", time.Now().UnixNano())
	group, err := repo.client.Group.Create().SetName(name).SetPlatform(service.PlatformCline).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, repo.client.Group.DeleteOneID(group.ID).Exec(ctx)) })
	// Fail only the test's account, never unrelated parallel tests.
	function := name + "_reject"
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF EXISTS(SELECT 1 FROM accounts WHERE id=NEW.account_id AND name='%s') THEN RAISE EXCEPTION 'synthetic outbox rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER %s BEFORE INSERT ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION %s();`, function, name, function, function))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON scheduler_outbox; DROP FUNCTION IF EXISTS %s();", function, function))
		require.NoError(t, err)
	})
	a := clineRepositoryFixture(t)
	a.ID = 0
	a.Name = name
	for _, grouped := range []bool{false, true} {
		if grouped {
			err = repo.CreateWithAccountGroups(ctx, a, []service.AccountGroup{{GroupID: group.ID, Priority: 17, AllowedModels: []string{"public-model"}}})
		} else {
			err = repo.Create(ctx, a)
		}
		require.Error(t, err)
		require.Zero(t, a.ID)
		var count int
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM accounts WHERE name=$1", name).Scan(&count))
		require.Zero(t, count)
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM account_groups WHERE group_id=$1", group.ID).Scan(&count))
		require.Zero(t, count)
	}
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER %s ON scheduler_outbox; DROP FUNCTION %s();", function, function))
	require.NoError(t, err)
	require.NoError(t, repo.CreateWithAccountGroups(ctx, a, []service.AccountGroup{{GroupID: group.ID, Priority: 17, AllowedModels: []string{"public-model"}}}))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
		require.NoError(t, err)
		require.NoError(t, repo.client.Account.DeleteOneID(a.ID).Exec(ctx))
	})
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM accounts WHERE name=$1", name).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_outbox WHERE account_id=$1", a.ID).Scan(&count))
	require.Equal(t, 1, count)
	current, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, current.AccountGroups, 1)
	require.Equal(t, 17, current.AccountGroups[0].Priority)
	require.Equal(t, []string{"public-model"}, current.AccountGroups[0].AllowedModels)
}

func TestClinePostgresCreateReusesOuterTransaction(t *testing.T) {
	ctx := context.Background()
	repo, _ := clinePostgresAccount(t)
	tx, err := repo.client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	a := clineRepositoryFixture(t)
	a.ID = 0
	a.Name = fmt.Sprintf("cline_outer_%d", time.Now().UnixNano())
	txCtx := dbent.NewTxContext(ctx, tx)
	require.NoError(t, repo.Create(txCtx, a))
	require.Positive(t, a.ID)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM accounts WHERE id=$1", a.ID).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_outbox WHERE account_id=$1", a.ID).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, tx.Rollback())
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM accounts WHERE id=$1", a.ID).Scan(&count))
	require.Zero(t, count)
}

func TestClinePostgresMaximumFreeScopeUsesUnfingerprintedColumn(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	model := strings.Repeat("m", 256)
	scope := cline.ScopeFree + model
	require.True(t, cline.ValidModelID(model))
	require.Len(t, scope, 267)
	require.Len(t, service.ClineRateLimitScope(a, scope), 332)
	subject, err := cline.ParseSubjectHash([]byte(fmt.Sprintf(`{"id":"fixture-user","active_account_id":"scope-%d"}`, a.ID)))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM cline_shared_limits WHERE subject_hash=$1", subject)
		require.NoError(t, err)
	})
	now := time.Now().UTC()
	state := clineVerifiedState(a, subject, now)
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	a.Extra[service.ClineStateExtraKey] = state
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, scope, now.Add(time.Hour), "observed_free_limit", true))
	state.FetchedAt = &now
	saved, err = repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	var length int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT length(scope) FROM cline_shared_limits WHERE subject_hash=$1 AND scope=$2", subject, scope).Scan(&length))
	require.Equal(t, 267, length)
	// Storage-only observation test; Free inference entitlement remains disabled.
	require.ErrorIs(t, cline.ValidateUpstreamModel(cline.ModeFree, model), cline.ErrFreeAPIUnsupported)
}
