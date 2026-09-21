package network

import (
	"context"
	"errors"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
)

func Serve(parent context.Context, listeners []models.Listener, sites map[string]models.Site, drainTimeout time.Duration) error {
	for _, listener := range listeners {
		if !listener.IsHTTP {
			continue
		}
		return serveHTTP(parent, listener, sites, drainTimeout)
	}
	return errors.New("no http listener")
}

func serveHTTP(parent context.Context, listener models.Listener, sites map[string]models.Site, drainTimeout time.Duration) error {
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
	server := &http.Server{Addr: listener.Address, Handler: handler}
	serveError := make(chan error, 1)
	go func() { serveError <- server.ListenAndServe() }()
	select {
	case err := <-serveError:
		return err
	case <-parent.Done():
		ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return err
		}
		return nil
	}
}

func matchedDirectorySite(requestPath string, routes []models.Route, sites map[string]models.Site) (models.Site, bool) {
	for _, route := range routes {
		if !route.When.Matches(requestPath) {
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