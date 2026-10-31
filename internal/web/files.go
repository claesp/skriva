package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/clpe/skriva/internal/rbac"
	"github.com/clpe/skriva/internal/store"
)

// maxUploadBytes is the largest size of an uploaded file.
const maxUploadBytes = 10 << 20

// imageTypes are the permitted types of uploaded files. The server finds
// the type from the data, not from the name or the request. SVG is not
// permitted, because an SVG file can contain scripts.
var imageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

// isUpload reports whether the request uploads a file. The body limit is
// larger for these requests.
func isUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return len(parts) == 3 && parts[2] == "files"
}

// uploadError sends an error as plain text. The editor shows the text.
func uploadError(w http.ResponseWriter, status int, msg string) {
	http.Error(w, msg, status)
}

// handleUpload saves an uploaded image. The query parameter "node" is the
// document that the user edits. The parameter "parent" is the parent of
// a new document. The user must have permission to edit the document, or
// to make a document in the parent. The response is JSON with the URL of
// the file.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	ri := reqInfoFrom(r.Context())
	perms, need := ri.Access.Root(), rbac.DocCreate
	q := r.URL.Query()
	id, edit := q.Get("node"), true
	if id == "" {
		id, edit = q.Get("parent"), false
	}
	if id != "" {
		if !store.ValidPublicID(id) {
			uploadError(w, http.StatusBadRequest, "The document is not valid.")
			return
		}
		n, err := s.store.Node(r.Context(), ri.Space.ID, id)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !canRead(r, n.AccessPathID)) {
			uploadError(w, http.StatusNotFound, "The document does not exist.")
			return
		}
		if err != nil {
			s.log.Error("upload", "error", err)
			uploadError(w, http.StatusInternalServerError, "The server cannot save the file.")
			return
		}
		perms = ri.Access.For(n.AccessPathID)
		if edit {
			need = rbac.DocEdit
		}
	}
	if !perms.Has(need) {
		uploadError(w, http.StatusForbidden, "You do not have permission to add images here.")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		uploadError(w, http.StatusBadRequest, "The request contains no file.")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		uploadError(w, http.StatusBadRequest, "The server cannot read the file.")
		return
	}
	if len(data) > maxUploadBytes {
		uploadError(w, http.StatusRequestEntityTooLarge, "The file is too large. The maximum is 10 MiB.")
		return
	}
	contentType := http.DetectContentType(data)
	if !imageTypes[contentType] {
		uploadError(w, http.StatusUnsupportedMediaType, "The file must be a PNG, JPEG, GIF or WebP image.")
		return
	}
	fileID, err := s.store.CreateFile(r.Context(), ri.Space.ID, contentType, data, ri.Session.User.ID)
	if err != nil {
		s.log.Error("upload", "error", err)
		uploadError(w, http.StatusInternalServerError, "The server cannot save the file.")
		return
	}
	s.log.Info("file uploaded", "tenant", ri.Tenant.Code, "space", ri.Space.Slug, "file", fileID,
		"type", contentType, "bytes", len(data), "user_id", ri.Session.User.ID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		URL string `json:"url"`
	}{links(r).Files + fileID})
}

// fileSecurityPolicy applies when a user opens a file directly.
const fileSecurityPolicy = "default-src 'none'; sandbox"

// handleFile sends an uploaded file. A user can read a file if the user
// can read a node that uses the file. The user that uploaded a file can
// read it until a node uses it. Thus the editor can show a new image
// before the user saves the document.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	ri := reqInfoFrom(r.Context())
	id := strings.ToLower(r.PathValue("id"))
	if !store.ValidPublicID(id) {
		s.notFound(w, r)
		return
	}
	f, err := s.store.File(r.Context(), ri.Space.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	permitted := len(f.AccessPaths) == 0 && f.CreatedBy == ri.Session.User.ID
	for _, p := range f.AccessPaths {
		if canRead(r, p) {
			permitted = true
			break
		}
	}
	if !permitted {
		s.notFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	h.Set("Content-Security-Policy", fileSecurityPolicy)
	h.Set("Content-Disposition", "inline")
	// The data of a file does not change, but the permissions can
	// change. Thus only the browser keeps a copy, for a short time.
	h.Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", f.CreatedAt, bytes.NewReader(f.Data))
}
