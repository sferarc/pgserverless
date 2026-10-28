package pgserverless

import (
	"net/http"
	"slices"
	"strings"
)

// corsAllowHeaders lists every request header the handler reads, under both spellings, plus the two a hand-written client sends.
var corsAllowHeaders = func() string {
	names := []string{"Authorization", "Content-Type"}
	for _, m := range headerAliases {
		names = append(names, m[0], m[1])
	}
	return strings.Join(names, ", ")
}()

// allowOrigin sets Access-Control-Allow-Origin when origins admits the request's Origin, and reports whether it did.
func allowOrigin(w http.ResponseWriter, r *http.Request, origins []string) bool {
	if len(origins) == 0 {
		return false
	}
	if slices.Contains(origins, "*") {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		return true
	}
	w.Header().Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	if origin == "" || !slices.Contains(origins, origin) {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	return true
}

// servePreflight answers OPTIONS /sql with the headers Neon's proxy sends.
func servePreflight(w http.ResponseWriter, r *http.Request, origins []string) {
	w.Header().Set("Allow", "OPTIONS, POST")
	if allowOrigin(w, r, origins) {
		w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
		w.Header().Set("Access-Control-Max-Age", "86400")
	}
	w.WriteHeader(http.StatusOK)
}
