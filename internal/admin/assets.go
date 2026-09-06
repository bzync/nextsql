package admin

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
)

//go:embed web
var webFS embed.FS

// webpwaFS holds the manifest, service worker, and icons — deliberately a
// *separate* embed.FS from webFS, not a subdirectory of web/. assetHandler
// below exposes the whole of webFS under /assets/, so anything that must
// exist ONLY at its own top-level path (sw.js above all — its default scope
// otherwise wouldn't cover the whole origin) cannot live anywhere inside
// web/, however deeply nested. internal/admin/frontend/build.mjs writes
// these into ../webpwa/, a sibling of ../web/, for exactly this reason.
//
//go:embed webpwa
var webpwaFS embed.FS

// assetSub is the web/ subtree, served under /assets/. It is the one shared
// bundle for every mode — built once from internal/admin/frontend/ and
// committed here so `go build ./...` needs no Node toolchain.
func assetSub() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic("admin: embedded web assets missing: " + err.Error())
	}
	return sub
}

func assetHandler() http.Handler {
	fileSrv := http.FileServer(http.FS(assetSub()))
	return http.StripPrefix("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		fileSrv.ServeHTTP(w, r)
	}))
}

// iconsHandler serves the PWA manifest's icon set from webpwa/icons/, same
// caching as the JS/CSS bundle under /assets/ — these are content-hashed by
// neither name, so a moderate max-age (not immutable) is the safe default.
func iconsHandler() http.Handler {
	sub, err := fs.Sub(webpwaFS, "webpwa/icons")
	if err != nil {
		panic("admin: embedded icons missing: " + err.Error())
	}
	fileSrv := http.FileServer(http.FS(sub))
	return http.StripPrefix("/icons/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		fileSrv.ServeHTTP(w, r)
	}))
}

// servePWAFile serves one top-level embedded PWA static file (manifest,
// service worker, favicon, apple touch icon) from webpwa/. These must be
// reachable at their exact top-level path — not under /assets/ — so the
// service worker's default scope covers the whole origin rather than just
// /assets/*; being embedded from a directory entirely separate from web/ is
// what guarantees that (see the webpwaFS doc comment above).
func servePWAFile(name, contentType, cacheControl string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := webpwaFS.ReadFile("webpwa/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", cacheControl)
		_, _ = w.Write(b)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{msg})
}
