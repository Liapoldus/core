package network

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
)

func Serve(parent context.Context, listeners []models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, drainTimeout time.Duration) error {
	for _, listener := range listeners {
		if !listener.IsHTTP {
			continue
		}
		return serveHTTP(parent, listener, sites, upstreams, drainTimeout)
	}
	return errors.New("no http listener")
}

func serveHTTP(parent context.Context, listener models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, drainTimeout time.Duration) error {
	proxies := buildProxies(listener.Routes, upstreams)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		index, route, found := matchedRoute(request.URL.Path, listener.Routes)
		if !found {
			http.NotFound(writer, request)
			return
		}
		if route.Proxy != nil {
			if proxied := proxies[index]; proxied != nil {
				proxied.ServeHTTP(writer, request)
				return
			}
			http.NotFound(writer, request)
			return
		}
		site, exists := sites[route.Site]
		if !exists || site.Source != models.SourceDirectory && site.Source != models.SourceRelease {
			http.NotFound(writer, request)
			return
		}
		if request.URL.Path == "" || strings.Contains(request.URL.Path, "/..") {
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
		info, err := os.Stat(candidate)
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		if info.IsDir() {
			candidate = filepath.Join(candidate, site.Index)
			indexInfo, err := os.Stat(candidate)
			if err != nil || indexInfo.IsDir() {
				http.NotFound(writer, request)
				return
			}
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

func matchedRoute(requestPath string, routes []models.Route) (int, models.Route, bool) {
	for index, route := range routes {
		if !route.When.Matches(requestPath) {
			continue
		}
		return index, route, true
	}
	return -1, models.Route{}, false
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