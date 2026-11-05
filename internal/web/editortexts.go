package web

import (
	"encoding/json"

	"github.com/claesp/skriva/internal/i18n"
)

// editorTexts returns the translations of the texts of the editor script
// (web/editor/src/editor.js) as a JSON object. The script uses the
// English text as the key. Keep this list the same as the calls of tr in
// the script.
func editorTexts(p *i18n.Printer) string {
	data, err := json.Marshal(p.Catalog(
		"Text",
		"The upload failed.",
		"Uploading %s…",
		"The search failed.",
		"in %s",
		"Links to the page \"%s\".",
		"The page does not exist, or you cannot read it.",
		"Select a page in the list, or type an address: https://…, mailto:…, wiki:Title or a path that starts with /.",
	))
	if err != nil {
		return "{}"
	}
	return string(data)
}
