package zotero

import (
	"encoding/json"
	"net/http"
	"regexp"
)

var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validHash(h string) bool {
	return hashRE.MatchString(h)
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	var body apiError
	body.Error.Code = code
	body.Error.Message = message
	writeJSON(w, status, body)
}
