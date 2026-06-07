// Command pion-stream — template.go embeds the HTMX viewer UI
// template, companion landing page, and browser studio into
// the binary at compile time.
package main

import _ "embed"

// viewerTemplate contains the full HTML for the stream viewer UI,
// including HTMX markup for live status polling and video playback.
//
//go:embed templates/index.html
var viewerTemplate []byte

// siteTemplate contains the companion landing page — a standalone
// documentation site for the project.
//
//go:embed static/site.html
var siteTemplate []byte

// studioTemplate contains the browser studio — a full-featured
// broadcasting interface for publishing directly from the browser.
//
//go:embed studio/index.html
var studioTemplate []byte
