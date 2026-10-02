package repository

import (
	"context"
	"errors"
	"maps"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Account insertion and durable notification always use one transaction. The
// grouped variant also writes exact binding priorities/model restrictions there.
// Caller-owned transactions are reused and never committed or published here.
func (r *accountRepository) createAccountAtomic(ctx context.Context, account *service.Account, groups []service.AccountGroup, bindGroups bool) error {
	if account == nil {
		return service.ErrAccountNilInput
	}
	draft := *account
	draft.Credentials = maps.Clone(account.Credentials)
	if err := service.NormalizeClineCredentials(draft.Platform, draft.Type, draft.Credentials); err != nil {
		return err
	}
	client := clientFromContext(ctx, r.client)
	tx, err := client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return err
	}
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		client = tx.Client()
	}
	bindings := append([]service.AccountGroup(nil), groups...)
	groupIDs := append([]int64(nil), account.GroupIDs...)
	if bindGroups {
		groupIDs = make([]int64, 0, len(bindings))
		for _, group := range bindings {
			groupIDs = append(groupIDs, group.GroupID)
		}
		if err := lockLiveGroups(ctx, client, groupIDs); err != nil {
			return err
		}
	}
	if err := createAccountRecord(ctx, client, &draft); err != nil {
		return err
	}
	if bindGroups {
		builders := make([]*dbent.AccountGroupCreate, 0, len(bindings))
		for i := range bindings {
			bindings[i].AccountID = draft.ID
			bindings[i].AllowedModels = service.NormalizeGroupAllowedModels(bindings[i].AllowedModels)
			builder := client.AccountGroup.Create().SetAccountID(draft.ID).SetGroupID(bindings[i].GroupID).SetPriority(bindings[i].Priority)
			if len(bindings[i].AllowedModels) > 0 {
				builder.SetAllowedModels(bindings[i].AllowedModels)
			}
			builders = append(builders, builder)
		}
		if len(builders) > 0 {
			if _, err := client.AccountGroup.CreateBulk(builders...).Save(ctx); err != nil {
				return err
			}
		}
		draft.GroupIDs = groupIDs
		draft.AccountGroups = bindings
	}
	if err := enqueueSchedulerOutbox(ctx, client, service.SchedulerOutboxEventAccountChanged, &draft.ID, nil, buildSchedulerGroupPayload(groupIDs)); err != nil {
		return err
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	// Do not expose a generated ID after a failed owned transaction. For an
	// outer transaction the caller retains the usual responsibility to roll back.
	*account = draft
	return nil
}
