package network

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/observability"
)

func Serve(parent context.Context, listeners []models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, drainTimeout time.Duration, metrics ...*observability.Registry) error {
	var started int
	errs := make(chan error, len(listeners))
	for _, listener := range listeners {
		started++
		go func(current models.Listener) {
			switch current.Type {
			case "tcp":
				errs <- serveTCP(parent, current, upstreams, drainTimeout)
			case "udp":
				errs <- serveUDP(parent, current, upstreams, drainTimeout)
			default:
				errs <- serveHTTP(parent, current, sites, upstreams, profiles, drainTimeout, firstRegistry(metrics))
			}
		}(listener)
	}
	if started == 0 {
		return errors.New("no http listener")
	}
	for i := 0; i < started; i++ {
		if err := <-errs; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	return nil
}

func firstRegistry(registries []*observability.Registry) *observability.Registry {
	if len(registries) == 0 {
		return nil
	}
	return registries[0]
}

// serveTCP owns the public socket and relays each accepted stream to the first
// healthy configured target. L4 rules are deliberately evaluated before any
// plugin boundary; plugins never receive the public socket.
func serveTCP(parent context.Context, listener models.Listener, upstreams map[string]models.Upstream, drainTimeout time.Duration) error {
	ln, err := net.Listen("tcp", listener.Address)
	if err != nil {
		return err
	}
	defer ln.Close()
	go func() { <-parent.Done(); _ = ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if parent.Err() != nil {
				return nil
			}
			return err
		}
		go relayTCP(parent, conn, listener.Rules, upstreams, drainTimeout)
	}
}

func relayTCP(parent context.Context, client net.Conn, rules []models.Route, upstreams map[string]models.Upstream, drainTimeout time.Duration) {
	defer client.Close()
	target := l4Target(rules, upstreams)
	if target == "" {
		return
	}
	dialer := net.Dialer{}
	server, err := dialer.DialContext(parent, "tcp", target)
	if err != nil {
		return
	}
	defer server.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(server, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, server); done <- struct{}{} }()
	select {
	case <-func() <-chan struct{} {
		finished := make(chan struct{})
		go func() { <-done; <-done; close(finished) }()
		return finished
	}():
	case <-parent.Done():
	}
	if drainTimeout > 0 {
		_ = client.SetDeadline(time.Now().Add(drainTimeout))
		_ = server.SetDeadline(time.Now().Add(drainTimeout))
	}
}

func serveUDP(parent context.Context, listener models.Listener, upstreams map[string]models.Upstream, drainTimeout time.Duration) error {
	addr, err := net.ResolveUDPAddr("udp", listener.Address)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() { <-parent.Done(); _ = conn.Close() }()
	buffer := make([]byte, 64*1024)
	for {
		n, source, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if parent.Err() != nil {
				return nil
			}
			return err
		}
		target := l4Target(listener.Rules, upstreams)
		if target == "" {
			continue
		}
		payload := append([]byte(nil), buffer[:n]...)
		go relayUDP(parent, conn, source, target, payload, drainTimeout)
	}
}

func relayUDP(parent context.Context, public *net.UDPConn, source *net.UDPAddr, target string, payload []byte, idle time.Duration) {
	addr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return
	}
	upstream, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return
	}
	defer upstream.Close()
	if idle <= 0 {
		idle = 30 * time.Second
	}
	_ = upstream.SetReadDeadline(time.Now().Add(idle))
	if _, err = upstream.Write(payload); err != nil {
		return
	}
	response := make([]byte, 64*1024)
	if n, err := upstream.Read(response); err == nil {
		_, _ = public.WriteToUDP(response[:n], source)
	}
	select {
	case <-parent.Done():
	default:
	}
}

func l4Target(rules []models.Route, upstreams map[string]models.Upstream) string {
	for _, rule := range rules {
		if rule.Deny != nil {
			return ""
		}
		if rule.Proxy == nil {
			continue
		}
		upstream, ok := upstreams[rule.Proxy.Upstream]
		if !ok || len(upstream.Targets) == 0 {
			continue
		}
		return upstream.Targets[0].Address
	}
	return ""
}

func serveHTTP(parent context.Context, listener models.Listener, sites map[string]models.Site, upstreams map[string]models.Upstream, profiles map[string]models.TLSProfile, drainTimeout time.Duration, metrics *observability.Registry) error {
	proxies := buildProxies(listener.Routes, upstreams)
	baseHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
		if route.Deny != nil {
			if route.Deny.Code != "" {
				writer.Header().Set("Content-Type", "application/problem+json")
			}
			writer.WriteHeader(route.Deny.Status)
			return
		}
		if route.Plugin != nil {
			// Capability dispatch is attached by the plugin runtime adapter. A
			// missing adapter is an explicit unavailable response, never 404.
			writer.Header().Set("Content-Type", "application/problem+json")
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if route.Proxy != nil {
			responseWriter := responseWriterWithActions(writer, route.Headers)
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
		if redirect, found := siteRedirect(request.URL.Path, site.Redirects); found {
			serveSiteRedirect(writer, redirect, responseHeaderActions(site.Headers, route.Headers))
			return
		}
		responseWriter := responseWriterWithActions(writer, site.Headers, route.Headers)
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
			if shouldSPAFallback(request, site, requested) {
				candidate = filepath.Join(site.Root, site.Index)
				info, err = os.Stat(candidate)
			}
		}
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
			info = indexInfo
		}
		if !isWithin(site.Root, candidate) || info.IsDir() {
			http.NotFound(writer, request)
			return
		}
		etag := fmt.Sprintf(`"lpg-r1-%x-%x"`, info.Size(), info.ModTime().UnixNano())
		responseWriter.Header().Set("ETag", etag)
		if request.Header.Get("If-None-Match") == etag {
			responseWriter.WriteHeader(http.StatusNotModified)
			return
		}
		applyStaticCache(responseWriter.Header(), site.Cache)
		http.ServeFile(responseWriter, request, candidate)
	})
	handler := gzipHandler(baseHandler)
	if metrics != nil {
		handler = instrumentHTTP(handler, metrics, listener.Address)
	}
	server := &http.Server{Addr: listener.Address, Handler: handler}
	var tlsConfig *tls.Config
	if listener.TLSProfile != "" {
		profile, ok := profiles[listener.TLSProfile]
		if !ok {
			return fmt.Errorf("tls profile %q not found", listener.TLSProfile)
		}
		loaded, err := loadTLSConfig(profile)
		if err != nil {
			return err
		}
		tlsConfig = loaded
		server.TLSConfig = tlsConfig
	}
	serveError := make(chan error, 1)
	go func() {
		if tlsConfig != nil {
			serveError <- server.ListenAndServeTLS("", "")
		} else {
			serveError <- server.ListenAndServe()
		}
	}()
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

type metricsResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricsResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *metricsResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *metricsResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}
func (w *metricsResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
func instrumentHTTP(next http.Handler, metrics *observability.Registry, listener string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := &metricsResponseWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}
		metrics.ObserveHTTP(listener, r.URL.Path, "", r.Method, fmt.Sprintf("%d", status), time.Since(started))
	})
}

func loadTLSConfig(profile models.TLSProfile) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: profile.Protocols}
	for _, certificate := range profile.Certificates {
		if certificate.Cert == "" || certificate.Key == "" {
			continue
		}
		pair, err := tls.LoadX509KeyPair(certificate.Cert, certificate.Key)
		if err != nil {
			return nil, fmt.Errorf("load tls certificate: %w", err)
		}
		config.Certificates = append(config.Certificates, pair)
	}
	if len(config.Certificates) == 0 {
		return nil, errors.New("tls profile has no certificate")
	}
	if profile.ClientAuth.Mode != "" {
		caBytes, err := os.ReadFile(profile.ClientAuth.CA)
		if err != nil {
			return nil, fmt.Errorf("load client ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return nil, errors.New("invalid client ca")
		}
		config.ClientCAs = pool
		if profile.ClientAuth.Mode == "require" {
			config.ClientAuth = tls.RequireAndVerifyClientCert
		} else {
			config.ClientAuth = tls.VerifyClientCertIfGiven
		}
	}
	return config, nil
}

// LoadTLSConfig exposes the transport TLS adapter to the management
// presentation layer without leaking certificate loading into domain code.
func LoadTLSConfig(profile models.TLSProfile) (*tls.Config, error) {
	return loadTLSConfig(profile)
}

func shouldSPAFallback(request *http.Request, site models.Site, requested string) bool {
	if !site.SPA || (request.Method != http.MethodGet && request.Method != http.MethodHead) || strings.Contains(path.Base(requested), ".") {
		return false
	}
	return true
}

func gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.Contains(request.Header.Get("Accept-Encoding"), "gzip") || request.Header.Get("Range") != "" {
			next.ServeHTTP(writer, request)
			return
		}
		wrapped := &gzipResponseWriter{ResponseWriter: writer, request: request}
		defer wrapped.close()
		next.ServeHTTP(wrapped, request)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	request  *http.Request
	gzip     *gzip.Writer
	decided  bool
	compress bool
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.decided {
		return
	}
	w.decided = true
	contentType := w.Header().Get("Content-Type")
	w.compress = status >= 200 && status < 300 && (strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "javascript") || strings.Contains(contentType, "json") || contentType == "") && w.Header().Get("Content-Encoding") == ""
	if w.compress {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length")
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	if !w.decided {
		w.WriteHeader(http.StatusOK)
	}
	if w.compress {
		if w.gzip == nil {
			w.gzip = gzip.NewWriter(w.ResponseWriter)
		}
		return len(data), func() error { _, err := w.gzip.Write(data); return err }()
	}
	return w.ResponseWriter.Write(data)
}
func (w *gzipResponseWriter) close() {
	if w.gzip != nil {
		_ = w.gzip.Close()
	}
}

func applyStaticCache(header http.Header, cache *models.SiteCache) {
	if cache == nil || cache.Static.Visibility == "" {
		return
	}
	if cache.Static.Visibility == "no-store" {
		header.Set("Cache-Control", "no-store")
		return
	}
	value := cache.Static.Visibility
	if cache.Static.MaxAge != "" {
		if duration, err := time.ParseDuration(cache.Static.MaxAge); err == nil {
			value += fmt.Sprintf(", max-age=%d", int64(duration/time.Second))
		}
	}
	header.Set("Cache-Control", value)
}

func siteRedirect(requestPath string, redirects []models.SiteRedirect) (models.SiteRedirect, bool) {
	for _, redirect := range redirects {
		if redirect.From == requestPath {
			return redirect, true
		}
	}
	return models.SiteRedirect{}, false
}

func serveSiteRedirect(writer http.ResponseWriter, redirect models.SiteRedirect, actions []models.HeaderSet) {
	for _, action := range actions {
		applyHeaderActions(writer.Header(), action)
	}
	writer.Header().Set(redirect.LocationHeader, redirect.To)
	writer.WriteHeader(redirect.Status)
}

func responseHeaderActions(sets ...*models.HeaderActions) []models.HeaderSet {
	var actions []models.HeaderSet
	for _, set := range sets {
		if set != nil && headerSetNonEmpty(set.Response) {
			actions = append(actions, set.Response)
		}
	}
	return actions
}

func responseWriterWithActions(writer http.ResponseWriter, sets ...*models.HeaderActions) http.ResponseWriter {
	actions := responseHeaderActions(sets...)
	if len(actions) == 0 {
		return writer
	}
	return &headerActionsWriter{ResponseWriter: writer, actions: actions}
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
	actions []models.HeaderSet
	wrote   bool
}

func (writer *headerActionsWriter) WriteHeader(status int) {
	if !writer.wrote {
		writer.wrote = true
		for _, actions := range writer.actions {
			applyHeaderActions(writer.Header(), actions)
		}
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
