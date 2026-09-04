package installgui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web
var webFS embed.FS

func assetSub() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic("installgui: embedded web assets missing: " + err.Error())
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
