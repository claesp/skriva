package store

import (
	"context"
	"encoding/json"
	"strconv"
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
	AuditNodeClassLimits      = "node.class_limits"
	AuditSpaceClassLimits     = "space.class_limits"
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

// sealedAudit is the plain text of audit_events.sealed.
type sealedAudit struct {
	Target  string `json:"target"`
	Details string `json:"details"`
}

func auditPlace(id int64) string { return "audit_events.sealed:" + strconv.FormatInt(id, 10) }

// audit writes an event into the audit log. The caller gives the
// transaction of the change. Thus the change and the event are saved
// together, or not at all. The event keeps the name of the actor and the
// code of the space at this time. The store encrypts the target and the
// details of an event of a tenant, because they can contain titles.
func (s *Store) audit(ctx context.Context, q querier, e auditEntry) error {
	actor := e.ActorID
	if actor == 0 {
		actor = actorFrom(ctx)
	}
	tenantID := e.TenantID
	if tenantID == nil && e.SpaceID != 0 {
		t, err := s.tenantOfSpace(ctx, e.SpaceID)
		if err != nil {
			return err
		}
		tenantID = &t
	}
	var id int64
	if err := q.QueryRow(ctx, "SELECT nextval(pg_get_serial_sequence('audit_events', 'id'))").Scan(&id); err != nil {
		return err
	}
	target, details := e.Target, e.Details
	var sealed []byte
	var version *int
	if tenantID != nil {
		plain, err := json.Marshal(sealedAudit{Target: target, Details: details})
		if err != nil {
			return err
		}
		ct, v, err := s.seal(ctx, *tenantID, auditPlace(id), plain)
		if err != nil {
			return err
		}
		sealed, version, target, details = ct, &v, "", ""
	}
	_, err := q.Exec(ctx, `
		INSERT INTO audit_events (id, tenant_id, created_at, actor_id, actor_name, action, space_slug,
		                          target, target_id, details, sealed, key_version)
		OVERRIDING SYSTEM VALUE
		SELECT $9, $1, $3, u.id,
		       COALESCE(NULLIF(u.display_name, ''), u.username, 'System'),
		       $5, COALESCE(sp.slug, ''), $6, $7, $8, $10, $11
		FROM (SELECT 1) one
		LEFT JOIN spaces sp ON sp.id = $2
		LEFT JOIN users u ON u.id = $4`,
		tenantID, e.SpaceID, s.now(), actor, e.Action, target, e.TargetID, details, id, sealed, version)
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
		SELECT id, created_at, actor_name, action, space_slug, target, target_id, details, sealed
		FROM audit_events
		WHERE tenant_id IS NOT DISTINCT FROM $1 AND ($2 = 0 OR id < $2)
		ORDER BY id DESC
		LIMIT $3`, tenantID, before, limit)
	if err != nil {
		return nil, err
	}
	type row struct {
		AuditEvent
		sealed []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var e row
		err := r.Scan(&e.ID, &e.CreatedAt, &e.Actor, &e.Action, &e.Space, &e.Target, &e.TargetID, &e.Details, &e.sealed)
		return e, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]AuditEvent, 0, len(list))
	for _, e := range list {
		if e.sealed != nil && tenantID != nil {
			plain, err := s.open(ctx, *tenantID, auditPlace(e.ID), e.sealed)
			if err != nil {
				return nil, err
			}
			var a sealedAudit
			if err := json.Unmarshal(plain, &a); err != nil {
				return nil, err
			}
			e.Target, e.Details = a.Target, a.Details
		}
		out = append(out, e.AuditEvent)
	}
	return out, nil
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
