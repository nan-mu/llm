package babeldoc

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validHash(h string) bool {
	return hashRE.MatchString(h)
}

func sha256Bytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func looksLikePDF(b []byte) bool {
	return len(b) >= 5 && string(b[:5]) == "%PDF-"
}