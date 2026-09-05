package webassets

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed dist/*
var files embed.FS

var distribution = mustDistribution()

type Options struct {
	ExternalRoot string
	APIVersion   string
	resolvePath  func(string) (string, error)
}

func Handler(options Options) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		activeDistribution := distribution
		if external, _, err := openCurrentWithResolver(options.ExternalRoot, options.APIVersion, options.resolvePath); err == nil {
			activeDistribution = external
		}
		fileServer := http.FileServer(http.FS(activeDistribution))
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requestedPath := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if requestedPath == "." || requestedPath == "" {
			requestedPath = "index.html"
		}
		if _, err := fs.Stat(activeDistribution, requestedPath); err != nil {
			if path.Ext(requestedPath) != "" || strings.HasPrefix(requestedPath, "assets/") {
				http.NotFound(response, request)
				return
			}
			requestedPath = "index.html"
		}
		if requestedPath == "index.html" {
			response.Header().Set("Cache-Control", "no-cache")
			contents, err := fs.ReadFile(activeDistribution, "index.html")
			if err != nil {
				response.WriteHeader(http.StatusInternalServerError)
				return
			}
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.ServeContent(response, request, "index.html", time.Time{}, bytes.NewReader(contents))
			return
		} else if requestedPath == "manifest.json" {
			response.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(requestedPath, "assets/") {
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		clone := request.Clone(request.Context())
		clone.URL.Path = "/" + requestedPath
		fileServer.ServeHTTP(response, clone)
	})
}

func mustDistribution() fs.FS {
	distribution, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err)
	}
	return distribution
}
