package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// AuditDays is the number of days that the audit log keeps an event. Then
// DeleteExpired removes it.
const AuditDays = 365

// Audit actions. The web package has a text for each action.
const (
	AuditLogin                = "user.login"
	AuditUserDisable          = "user.disable"
	AuditUserEnable           = "user.enable"
	AuditGroupCreate          = "group.create"
	AuditGroupDelete          = "group.delete"
	AuditMemberAdd            = "group.member.add"
	AuditMemberRemove         = "group.member.remove"
	AuditRoleCreate           = "role.create"
	AuditRoleDelete           = "role.delete"
	AuditBindingCreate        = "binding.create"
	AuditBindingDelete        = "binding.delete"
	AuditSpaceCreate          = "space.create"
	AuditSpaceUpdate          = "space.update"
	AuditSpaceDelete          = "space.delete"
	AuditSpaceDefaultRole     = "space.default_role"
	AuditNodeDefaultRole      = "node.default_role"
	AuditNodeMove             = "node.move"
	AuditNodeRevert           = "node.revert"
	AuditNodeTrash            = "node.trash"
	AuditNodeRestore          = "node.restore"
	AuditNodePurge            = "node.purge"
	AuditClassificationCreate = "classification.create"
	AuditClassificationUpdate = "classification.update"
	AuditClassificationDelete = "classification.delete"
	AuditNodeClassify         = "node.classify"
	AuditTenantCreate         = "tenant.create"
	AuditTenantUpdate         = "tenant.update"
	AuditTenantDelete         = "tenant.delete"
	AuditTenantKeyRotate      = "tenant.key_rotate"
)

type actorKey struct{}

// WithActor returns a context that tells the store which user makes the
// changes. The store writes the user into the audit log. Without an
// actor, the audit log shows "System", for example for the cleanup task.
func WithActor(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, actorKey{}, userID)
}

func actorFrom(ctx context.Context) int64 {
	id, _ := ctx.Value(actorKey{}).(int64)
	return id
}

// auditEntry is an event for the audit log.
type auditEntry struct {
	// TenantID is the tenant of the event. If it is nil and SpaceID is
	// set, the event gets the tenant of the space. If both are unset,
	// the event belongs to the system administration.
	TenantID *int64
	SpaceID  int64
	// ActorID replaces the actor of the context, if it is not 0.
	ActorID  int64
	Action   string
	Target   string
	TargetID string
	Details  string
}

// audit writes an event into the audit log. The caller gives the
// transaction of the change. Thus the change and the event are saved
// together, or not at all. The event keeps the name of the actor and the
// code of the space at this time.
func (s *Store) audit(ctx context.Context, q querier, e auditEntry) error {
	actor := e.ActorID
	if actor == 0 {
		actor = actorFrom(ctx)
	}
	_, err := q.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, created_at, actor_id, actor_name, action, space_slug,
		                          target, target_id, details)
		SELECT COALESCE($1, sp.tenant_id), $3, u.id,
		       COALESCE(NULLIF(u.display_name, ''), u.username, 'System'),
		       $5, COALESCE(sp.slug, ''), $6, $7, $8
		FROM (SELECT 1) one
		LEFT JOIN spaces sp ON sp.id = $2
		LEFT JOIN users u ON u.id = $4`,
		e.TenantID, e.SpaceID, s.now(), actor, e.Action, e.Target, e.TargetID, e.Details)
	return err
}

// AuditEvent is one event of the audit log.
type AuditEvent struct {
	ID        int64
	CreatedAt time.Time
	Actor     string
	Action    string
	Space     string
	Target    string
	TargetID  string
	Details   string
}

// AuditEvents returns the events of a tenant, newest first. A nil tenant
// gives the events of the system administration. If before is not 0, the
// result contains only events with a smaller ID.
func (s *Store) AuditEvents(ctx context.Context, tenantID *int64, before int64, limit int) ([]AuditEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, created_at, actor_name, action, space_slug, target, target_id, details
		FROM audit_events
		WHERE tenant_id IS NOT DISTINCT FROM $1 AND ($2 = 0 OR id < $2)
		ORDER BY id DESC
		LIMIT $3`, tenantID, before, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (AuditEvent, error) {
		var e AuditEvent
		err := row.Scan(&e.ID, &e.CreatedAt, &e.Actor, &e.Action, &e.Space, &e.Target, &e.TargetID, &e.Details)
		return e, err
	})
}

// tx runs fn in a transaction.
func (s *Store) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.pool, fn)
}

// roleText describes a default role for the audit log. nil means that
// the item uses the role of its parent.
func roleText(role *string, inherited string) string {
	switch {
	case role == nil:
		return "Same as the " + inherited
	case *role == "":
		return "No default role"
	}
	return *role
}
