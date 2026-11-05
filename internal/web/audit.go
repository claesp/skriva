package web

import (
	"net/http"
	"strconv"

	"github.com/claesp/skriva/internal/i18n"
	"github.com/claesp/skriva/internal/store"
)

// auditPageSize is the number of events on one page of the audit log.
const auditPageSize = 100

// auditActionText returns the text of an action in the audit log, or the
// code of an unknown action.
func auditActionText(p *i18n.Printer, action string) string {
	switch action {
	case store.AuditLogin:
		return p.T("Signed in")
	case store.AuditUserDisable:
		return p.T("Disabled a user")
	case store.AuditUserEnable:
		return p.T("Enabled a user")
	case store.AuditGroupCreate:
		return p.T("Made a group")
	case store.AuditGroupDelete:
		return p.T("Deleted a group")
	case store.AuditMemberAdd:
		return p.T("Added a group member")
	case store.AuditMemberRemove:
		return p.T("Removed a group member")
	case store.AuditRoleCreate:
		return p.T("Made a role")
	case store.AuditRoleDelete:
		return p.T("Deleted a role")
	case store.AuditBindingCreate:
		return p.T("Gave a role")
	case store.AuditBindingDelete:
		return p.T("Removed a role")
	case store.AuditSpaceCreate:
		return p.T("Made a space")
	case store.AuditSpaceUpdate:
		return p.T("Changed a space")
	case store.AuditSpaceDelete:
		return p.T("Deleted a space")
	case store.AuditSpaceDefaultRole:
		return p.T("Changed the default role of a space")
	case store.AuditNodeDefaultRole:
		return p.T("Changed the default role of an item")
	case store.AuditNodeMove:
		return p.T("Moved an item")
	case store.AuditNodeRevert:
		return p.T("Restored an old version")
	case store.AuditNodeTrash:
		return p.T("Deleted an item (to the trash)")
	case store.AuditNodeRestore:
		return p.T("Restored an item from the trash")
	case store.AuditNodePurge:
		return p.T("Deleted an item permanently")
	case store.AuditClassificationCreate:
		return p.T("Made a classification")
	case store.AuditClassificationUpdate:
		return p.T("Changed a classification")
	case store.AuditClassificationDelete:
		return p.T("Deleted a classification")
	case store.AuditNodeClassify:
		return p.T("Changed the classification of a document")
	case store.AuditNodeClassLimits:
		return p.T("Changed the classification limits of a folder")
	case store.AuditSpaceClassLimits:
		return p.T("Changed the classification limits of a space")
	case store.AuditTenantKeyRotate:
		return p.T("Made a new data key")
	case store.AuditTenantCreate:
		return p.T("Made a tenant")
	case store.AuditTenantUpdate:
		return p.T("Changed a tenant")
	case store.AuditTenantDelete:
		return p.T("Deleted a tenant")
	}
	return action
}

// auditRow is one row of the audit log page.
type auditRow struct {
	store.AuditEvent
	ActionText string
}

// auditPage is the data of the audit log page. Older is the URL of the
// next page with older events, or "".
type auditPage struct {
	Rows  []auditRow
	Older string
	Days  int
}

// handleAudit shows the audit log of the tenant of the request, or of the
// system administration.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	ri := reqInfoFrom(r.Context())
	var tenantID *int64
	if ri.Tenant != nil && !ri.Session.System() {
		tenantID = &ri.Tenant.ID
	}
	var before int64
	if v := r.URL.Query().Get("before"); v != "" {
		var err error
		if before, err = strconv.ParseInt(v, 10, 64); err != nil || before < 1 {
			s.badRequest(w, r, s.tr(r).T("The page is not valid."))
			return
		}
	}
	events, err := s.store.AuditEvents(r.Context(), tenantID, before, auditPageSize+1)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := auditPage{Days: store.AuditDays}
	if len(events) > auditPageSize {
		events = events[:auditPageSize]
		page.Older = r.URL.Path + "?before=" + strconv.FormatInt(events[len(events)-1].ID, 10)
	}
	p := s.tr(r)
	for _, e := range events {
		page.Rows = append(page.Rows, auditRow{e, auditActionText(p, e.Action)})
	}
	s.renderAdmin(w, r, http.StatusOK, "audit", "admin-audit", s.tr(r).T("Audit log"), page)
}
