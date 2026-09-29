// Package dochash is the document identity rule for Zotero and BabelDOC tasks.
// Task id is the SHA-256 of the source PDF bytes, as 64 lowercase hex characters.
package dochash

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

var idRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// SourceID is the SHA-256 hex of the source PDF.
func SourceID(pdf []byte) string {
	sum := sha256.Sum256(pdf)
	return hex.EncodeToString(sum[:])
}

// Valid reports whether id is 64 lowercase hex characters.
func Valid(id string) bool {
	return idRE.MatchString(id)
}

// LooksLikePDF reports whether b starts with the PDF header.
func LooksLikePDF(b []byte) bool {
	return len(b) >= 4 && string(b[:4]) == "%PDF"
}
