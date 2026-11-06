// Package ui embeds the installer's HTML/CSS/JS front end.
package ui

import _ "embed"

// IndexHTML is the single-page UI (kept under 2 MB: WebView2's NavigateToString limit).
//
//go:embed index.html
var IndexHTML []byte

// LicensesText is the third-party license text (the licenses/ files joined; see licenses_test.go). It is
// embedded so the notices travel inside the program file as well as beside it in the download (cmd/pecheck
// checks that they are there); the page does not show it.
//
//go:embed THIRD_PARTY_LICENSES.txt
var LicensesText []byte
