package web

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// svgType is the content type of a draw.io diagram in SVG format.
const svgType = "image/svg+xml"

// maxSVGDepth is the maximum depth of the elements of an SVG file.
const maxSVGDepth = 256

// Errors of checkDrawioSVG.
var (
	errNotXML      = errors.New("the file is not XML")
	errDrawioXML   = errors.New("the file is a draw.io diagram without an image")
	errNotDrawio   = errors.New("the SVG file is not a draw.io diagram")
	errUnsafeSVG   = errors.New("the SVG file contains items that are not permitted")
	errSVGTooDeep  = errors.New("the SVG file has too many levels")
	errSVGEntities = errors.New("the SVG file defines entities")
)

// unsafeSVGElements are elements that can run scripts or load documents.
// draw.io does not make them. The animation elements can set an
// attribute, for example href to a javascript: URL.
var unsafeSVGElements = map[string]bool{
	"script": true, "iframe": true, "object": true, "embed": true, "frame": true,
	"frameset": true, "applet": true, "base": true, "meta": true, "link": true,
	"handler": true, "listener": true,
	"set": true, "animate": true, "animatemotion": true, "animatetransform": true, "animatecolor": true,
}

// checkDrawioSVG makes sure that data is a draw.io diagram in SVG format:
// an SVG file that contains the diagram in the "content" attribute of the
// root element. draw.io makes this file with File > Export as > SVG and
// the option "Include a copy of my diagram". Thus draw.io can open and
// change the file again.
//
// An SVG file can contain scripts. The browser does not run scripts in
// an img element, and the server sends files with a policy that does not
// permit scripts (fileSVGSecurityPolicy). As a third layer, the function
// refuses files with script elements, event attributes, javascript: URLs
// and entity definitions.
func checkDrawioSVG(data []byte) error {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	depth := 0
	root := true
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errNotXML
		}
		switch t := tok.(type) {
		case xml.ProcInst:
			// Only the XML declaration. A style sheet can load a file.
			if !strings.EqualFold(t.Target, "xml") {
				return errUnsafeSVG
			}
		case xml.Directive:
			// draw.io writes a DOCTYPE without an internal subset. An
			// internal subset can define entities.
			if bytes.ContainsAny(t, "[") || bytes.Contains(bytes.ToUpper(t), []byte("ENTITY")) {
				return errSVGEntities
			}
		case xml.StartElement:
			depth++
			if depth > maxSVGDepth {
				return errSVGTooDeep
			}
			name := strings.ToLower(t.Name.Local)
			if root {
				root = false
				if name == "mxfile" {
					return errDrawioXML
				}
				if name != "svg" || (t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg") ||
					!hasDrawioContent(t.Attr) {
					return errNotDrawio
				}
			}
			if unsafeSVGElements[name] {
				return errUnsafeSVG
			}
			for _, a := range t.Attr {
				if unsafeSVGAttr(a) {
					return errUnsafeSVG
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	if root {
		return errNotXML
	}
	return nil
}

// hasDrawioContent reports whether the attributes contain the diagram of
// draw.io: the "content" attribute with an mxfile element.
func hasDrawioContent(attrs []xml.Attr) bool {
	for _, a := range attrs {
		if a.Name.Local == "content" && a.Name.Space == "" && strings.Contains(a.Value, "<mxfile") {
			return true
		}
	}
	return false
}

// unsafeSVGAttr reports whether an attribute can run a script: an event
// attribute (onload, onclick, ...) or a javascript: or data: URL in a
// link. Images in data: URLs are permitted.
func unsafeSVGAttr(a xml.Attr) bool {
	name := strings.ToLower(a.Name.Local)
	if strings.HasPrefix(name, "on") {
		return true
	}
	if name != "href" && name != "src" && name != "action" && name != "formaction" {
		return false
	}
	v := strings.ToLower(strings.Map(func(r rune) rune {
		// Browsers ignore spaces and control characters in the scheme.
		if r <= ' ' {
			return -1
		}
		return r
	}, a.Value))
	if strings.HasPrefix(v, "data:") {
		return !strings.HasPrefix(v, "data:image/") || strings.HasPrefix(v, "data:image/svg")
	}
	return strings.HasPrefix(v, "javascript:") || strings.HasPrefix(v, "vbscript:")
}
