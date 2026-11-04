package web

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestCheckDrawioSVG(t *testing.T) {
	sample, err := os.ReadFile("testdata/flow.drawio.svg")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkDrawioSVG(sample); err != nil {
		t.Fatalf("draw.io export: %v", err)
	}
	if err := checkDrawioSVG(append([]byte("\xef\xbb\xbf"), sample...)); err != nil {
		t.Fatalf("draw.io export with a byte order mark: %v", err)
	}
	// insert puts text into the sample, after the start tag of the root.
	insert := func(s string) []byte {
		return []byte(strings.Replace(string(sample), "<defs/>", s+"<defs/>", 1))
	}
	const content = `content="&lt;mxfile&gt;&lt;/mxfile&gt;"`
	for name, c := range map[string]struct {
		data []byte
		want error
	}{
		"plain draw.io file": {[]byte(`<mxfile host="drawio"><diagram id="a">x</diagram></mxfile>`), errDrawioXML},
		"other SVG":          {[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`), errNotDrawio},
		"HTML":               {[]byte(`<html ` + content + `><body/></html>`), errNotDrawio},
		"not XML":            {[]byte("hello"), errNotXML},
		"empty":              {nil, errNotXML},
		"broken XML":         {[]byte(`<svg ` + content + `><g></svg>`), errNotXML},
		"script":             {insert(`<script>alert(1)</script>`), errUnsafeSVG},
		"script in label":    {insert(`<foreignObject><div xmlns="http://www.w3.org/1999/xhtml"><script>alert(1)</script></div></foreignObject>`), errUnsafeSVG},
		"iframe":             {insert(`<foreignObject><iframe xmlns="http://www.w3.org/1999/xhtml" src="https://evil.example"/></foreignObject>`), errUnsafeSVG},
		"event attribute":    {insert(`<rect onload="alert(1)"/>`), errUnsafeSVG},
		"event upper case":   {insert(`<rect ONCLICK="alert(1)"/>`), errUnsafeSVG},
		"javascript link":    {insert(`<a xlink:href=" java&#9;script:alert(1)"><text>x</text></a>`), errUnsafeSVG},
		"data HTML link":     {insert(`<a href="data:text/html,x"><text>x</text></a>`), errUnsafeSVG},
		"nested SVG image":   {insert(`<image href="data:image/svg+xml;base64,PHN2Zz4="/>`), errUnsafeSVG},
		"set href":           {insert(`<a><set attributeName="href" to="javascript:alert(1)"/><text>x</text></a>`), errUnsafeSVG},
		"animate":            {insert(`<a><animate attributeName="href" values="javascript:alert(1)"/></a>`), errUnsafeSVG},
		"style sheet":        {[]byte(`<?xml version="1.0"?><?xml-stylesheet href="https://evil.example/x.css"?><svg ` + content + `/>`), errUnsafeSVG},
		"entities":           {[]byte(`<!DOCTYPE svg [<!ENTITY x "y">]><svg ` + content + `>&x;</svg>`), errSVGEntities},
		"too deep":           {[]byte(`<svg ` + content + `>` + strings.Repeat("<g>", maxSVGDepth) + strings.Repeat("</g>", maxSVGDepth) + `</svg>`), errSVGTooDeep},
	} {
		if err := checkDrawioSVG(c.data); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	// A PNG image in a data: URL and a normal link are permitted.
	if err := checkDrawioSVG(insert(`<image href="data:image/png;base64,iVBORw0KGgo="/><a href="https://example.com"><text>x</text></a>`)); err != nil {
		t.Errorf("image and link: %v", err)
	}
}
