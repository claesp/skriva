package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/claesp/skriva/internal/rbac"
	"github.com/claesp/skriva/internal/store"
)

// maxUploadBytes is the largest size of an uploaded file.
const maxUploadBytes = 10 << 20

// imageTypes are the permitted types of uploaded images. The server finds
// the type from the data, not from the name or the request. Other SVG
// files are not permitted, because an SVG file can contain scripts. A
// draw.io diagram in SVG format is permitted after a check. See
// checkDrawioSVG.
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
		switch err := checkDrawioSVG(data); {
		case err == nil:
			contentType = svgType
		case errors.Is(err, errDrawioXML):
			uploadError(w, http.StatusUnsupportedMediaType, "This draw.io file contains no image. "+
				"In draw.io, use File > Export as > SVG (or PNG) and select \"Include a copy of my diagram\".")
			return
		case errors.Is(err, errNotXML), errors.Is(err, errNotDrawio):
			uploadError(w, http.StatusUnsupportedMediaType, "The file must be a PNG, JPEG, GIF or WebP image, "+
				"or a draw.io diagram in SVG format (.drawio.svg).")
			return
		default:
			uploadError(w, http.StatusUnsupportedMediaType, "The SVG file contains items that are not permitted, "+
				"for example scripts.")
			return
		}
	}
	fileID, err := s.store.CreateFile(r.Context(), ri.Space.ID, contentType, data, ri.Session.User.ID)
	if errors.Is(err, store.ErrQuota) {
		s.log.Info("upload quota full", "tenant", ri.Tenant.Code, "space", ri.Space.Slug,
			"bytes", len(data), "user_id", ri.Session.User.ID)
		uploadError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("The space has no room for this file. "+
			"All files of the space together can use %d MiB. Ask an administrator of the space to increase the limit.",
			ri.Space.UploadQuotaMB))
		return
	}
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

// fileSVGSecurityPolicy applies to a draw.io diagram in SVG format. The
// diagram uses inline styles and can contain images and fonts in data:
// URLs. Scripts and other resources are not permitted, also when a user
// opens the file directly.
const fileSVGSecurityPolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; sandbox"

// handleFile sends an uploaded file. A user can read a file if the user
// can read a node that uses the file. The user that uploaded a file can
// read it until a node uses it. Thus the editor can show a new image
// before the user saves the document.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(r.PathValue("id"))
	if !store.ValidPublicID(id) {
		s.notFound(w, r)
		return
	}
	f, err := s.readableFile(r, id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	if f.ContentType == svgType {
		h.Set("Content-Security-Policy", fileSVGSecurityPolicy)
	} else {
		h.Set("Content-Security-Policy", fileSecurityPolicy)
	}
	h.Set("Content-Disposition", "inline")
	// The data of a file does not change, but the permissions can
	// change. Thus only the browser keeps a copy, for a short time.
	h.Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", f.CreatedAt, bytes.NewReader(f.Data))
}

// readableFile returns the uploaded file with the ID in the space of the
// request. A user can read a file that a readable document uses, and a
// file that the user uploaded and no document uses. For other files, the
// error is store.ErrNotFound.
func (s *Server) readableFile(r *http.Request, id string) (*store.File, error) {
	ri := reqInfoFrom(r.Context())
	f, err := s.store.File(r.Context(), ri.Space.ID, id)
	if err != nil {
		return nil, err
	}
	if len(f.AccessPaths) == 0 && f.CreatedBy == ri.Session.User.ID {
		return f, nil
	}
	for _, p := range f.AccessPaths {
		if canRead(r, p) {
			return f, nil
		}
	}
	return nil, store.ErrNotFound
}

// usableFiles returns the IDs in ids of the files that the user can read.
// A document gives read access to the files that it uses. Thus a user
// must not get access to a file through a new document, only because the
// user knows the ID of the file.
func (s *Server) usableFiles(r *http.Request, ids []string) ([]string, error) {
	out := ids[:0:0]
	for _, id := range ids {
		_, err := s.readableFile(r, id)
		switch {
		case err == nil:
			out = append(out, id)
		case !errors.Is(err, store.ErrNotFound):
			return nil, err
		}
	}
	return out, nil
}
