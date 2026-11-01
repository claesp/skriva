package web

import (
	"net/http"
	"strconv"

	"github.com/clpe/skriva/internal/store"
)

// auditPageSize is the number of events on one page of the audit log.
const auditPageSize = 100

// auditActions are the texts of the actions in the audit log.
var auditActions = map[string]string{
	store.AuditLogin:                "Signed in",
	store.AuditUserDisable:          "Disabled a user",
	store.AuditUserEnable:           "Enabled a user",
	store.AuditGroupCreate:          "Made a group",
	store.AuditGroupDelete:          "Deleted a group",
	store.AuditMemberAdd:            "Added a group member",
	store.AuditMemberRemove:         "Removed a group member",
	store.AuditRoleCreate:           "Made a role",
	store.AuditRoleDelete:           "Deleted a role",
	store.AuditBindingCreate:        "Gave a role",
	store.AuditBindingDelete:        "Removed a role",
	store.AuditSpaceCreate:          "Made a space",
	store.AuditSpaceUpdate:          "Changed a space",
	store.AuditSpaceDelete:          "Deleted a space",
	store.AuditSpaceDefaultRole:     "Changed the default role of a space",
	store.AuditNodeDefaultRole:      "Changed the default role of an item",
	store.AuditNodeMove:             "Moved an item",
	store.AuditNodeRevert:           "Restored an old version",
	store.AuditNodeTrash:            "Deleted an item (to the trash)",
	store.AuditNodeRestore:          "Restored an item from the trash",
	store.AuditNodePurge:            "Deleted an item permanently",
	store.AuditClassificationCreate: "Made a classification",
	store.AuditClassificationUpdate: "Changed a classification",
	store.AuditClassificationDelete: "Deleted a classification",
	store.AuditNodeClassify:         "Changed the classification of a document",
	store.AuditTenantCreate:         "Made a tenant",
	store.AuditTenantUpdate:         "Changed a tenant",
	store.AuditTenantDelete:         "Deleted a tenant",
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
			s.badRequest(w, r, "The page is not valid.")
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
	for _, e := range events {
		text, ok := auditActions[e.Action]
		if !ok {
			text = e.Action
		}
		page.Rows = append(page.Rows, auditRow{e, text})
	}
	s.renderAdmin(w, r, http.StatusOK, "audit", "admin-audit", "Audit log", page)
}
