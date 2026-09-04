package manager

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web
var webFS embed.FS

// assetSub is the web/ subtree, served under /assets/.
func assetSub() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic("manager: embedded web assets missing: " + err.Error())
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

// serveShell returns the single-page shell for the app root.
func serveShell(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "shell missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}
