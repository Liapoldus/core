package network

import (
	"errors"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/Liapoldus/core/internal/domain/models"
)

func Serve(listeners []models.Listener, sites map[string]models.Site) error {
	for _, listener := range listeners {
		if !listener.IsHTTP {
			continue
		}
		return serveHTTP(listener, sites)
	}
	return errors.New("no http listener")
}

func serveHTTP(listener models.Listener, sites map[string]models.Site) error {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		site, found := matchedDirectorySite(request.URL.Path, listener.Routes, sites)
		if !found || site.Source != models.SourceDirectory {
			http.NotFound(writer, request)
			return
		}
		requested := path.Clean(request.URL.Path)
		if requested == "/" {
			requested = "/" + site.Index
		}
		candidate := filepath.Join(site.Root, filepath.FromSlash(strings.TrimPrefix(requested, "/")))
		if !isWithin(site.Root, candidate) {
			http.NotFound(writer, request)
			return
		}
		http.ServeFile(writer, request, candidate)
	})
	return http.ListenAndServe(listener.Address, handler)
}

func matchedDirectorySite(requestPath string, routes []models.Route, sites map[string]models.Site) (models.Site, bool) {
	for _, route := range routes {
		if route.PathPrefix != "" && !strings.HasPrefix(requestPath, route.PathPrefix) {
			continue
		}
		site, exists := sites[route.Site]
		return site, exists
	}
	return models.Site{}, false
}

func isWithin(root, candidate string) bool {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	resolvedCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}