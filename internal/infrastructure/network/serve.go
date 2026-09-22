package network

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
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
		if route.Headers != nil {
			applyHeaderActions(request.Header, route.Headers.Request)
		}
		if route.Rewrite != nil && route.Rewrite.Pattern != nil {
			request.URL.Path = route.Rewrite.Pattern.ReplaceAllString(request.URL.Path, route.Rewrite.Replacement)
		}
		if route.Redirect != nil {
			serveRedirect(writer, request, *route.Redirect, route.Headers)
			return
		}
		responseWriter := writer
		if route.Headers != nil && headerSetNonEmpty(route.Headers.Response) {
			responseWriter = &headerActionsWriter{ResponseWriter: writer, actions: route.Headers.Response}
		}
		if route.Proxy != nil {
			if proxied := proxies[index]; proxied != nil {
				proxied.ServeHTTP(responseWriter, request)
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
		http.ServeFile(responseWriter, request, candidate)
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

func applyHeaderActions(header http.Header, actions models.HeaderSet) {
	for name, value := range actions.Set {
		header.Set(name, value)
	}
	for name, value := range actions.SetIfAbsent {
		if header.Get(name) == "" {
			header.Set(name, value)
		}
	}
	for _, name := range actions.Delete {
		header.Del(name)
	}
}

func headerSetNonEmpty(actions models.HeaderSet) bool {
	return len(actions.Set) > 0 || len(actions.SetIfAbsent) > 0 || len(actions.Delete) > 0
}

func serveRedirect(writer http.ResponseWriter, request *http.Request, redirect models.RouteRedirect, headers *models.HeaderActions) {
	status := redirect.Status
	if status == 0 {
		status = 308
	}
	if headers != nil {
		applyHeaderActions(writer.Header(), headers.Response)
	}
	writer.Header().Set("Location", redirectLocation(request, redirect))
	writer.WriteHeader(status)
}

func redirectLocation(request *http.Request, redirect models.RouteRedirect) string {
	scheme := redirect.Scheme
	if scheme == "" {
		scheme = "http"
		if request.TLS != nil {
			scheme = "https"
		}
	}
	host := redirect.Host
	if host == "" {
		host = request.Host
	}
	target := &url.URL{Scheme: scheme, Host: host, Path: request.URL.Path}
	if redirect.Path != "" {
		target.Path = redirect.Path
	}
	if redirect.PreserveQuery && request.URL.RawQuery != "" {
		target.RawQuery = request.URL.RawQuery
	}
	return target.String()
}

type headerActionsWriter struct {
	http.ResponseWriter
	actions models.HeaderSet
	wrote   bool
}

func (writer *headerActionsWriter) WriteHeader(status int) {
	if !writer.wrote {
		writer.wrote = true
		applyHeaderActions(writer.Header(), writer.actions)
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *headerActionsWriter) Write(body []byte) (int, error) {
	if !writer.wrote {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *headerActionsWriter) Flush() {
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *headerActionsWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking not supported")
	}
	return hijacker.Hijack()
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
