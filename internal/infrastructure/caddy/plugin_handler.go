package caddy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	coreassets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	caddycore "github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	caddyhttp "github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/google/uuid"
)

type pluginHandlerContract struct {
	App                    string   `json:"app"`
	Directive              string   `json:"directive"`
	OrderAnchor            string   `json:"orderAnchor"`
	HandlerModule          string   `json:"handlerModule"`
	CallMode               string   `json:"callMode"`
	MaxRequestBytes        int64    `json:"maxRequestBytes"`
	UnavailableStatus      int      `json:"unavailableStatus"`
	InvalidResponseStatus  int      `json:"invalidResponseStatus"`
	InvalidRequestStatus   int      `json:"invalidRequestStatus"`
	BlockedRequestHeaders  []string `json:"blockedRequestHeaders"`
	BlockedResponseHeaders []string `json:"blockedResponseHeaders"`
	Diagnostics            struct {
		InvalidDirective     string `json:"invalidDirective"`
		InvalidInstance      string `json:"invalidInstance"`
		UnsupportedMode      string `json:"unsupportedMode"`
		InstanceUnavailable  string `json:"instanceUnavailable"`
		InvalidCookiePolicy  string `json:"invalidCookiePolicy"`
		InvalidCookieRequest string `json:"invalidCookieRequest"`
	} `json:"diagnostics"`
}

type PluginInstance struct {
	Name               string            `json:"name"`
	Endpoint           string            `json:"endpoint"`
	Timeout            time.Duration     `json:"timeout"`
	StartTimeout       time.Duration     `json:"startTimeout"`
	MaxConcurrentCalls int               `json:"maxConcurrentCalls"`
	CookiePolicies     []json.RawMessage `json:"cookiePolicies,omitempty"`
}

type dispatchAppConfig struct {
	Instances []PluginInstance `json:"instances"`
}

type pluginBinding struct {
	client          *plugins.CapabilityClient
	conn            *plugins.Client
	modes           map[string]struct{}
	invocationModes map[string]map[pluginv1.InvocationMode]struct{}
	cookies         map[string]plugins.CookiePolicy
	timeout         time.Duration
}

type dispatchApp struct {
	Instances []PluginInstance `json:"instances,omitempty"`
	bindings  map[string]pluginBinding
}

type pluginCallHandler struct {
	Instance   string `json:"instance,omitempty"`
	Capability string `json:"capability,omitempty"`
	Mode       string `json:"mode,omitempty"`
	binding    pluginBinding
	contract   pluginHandlerContract
}

func init() {
	contract, err := loadPluginHandlerContract()
	if err != nil {
		panic(err)
	}
	caddycore.RegisterModule(dispatchApp{})
	caddycore.RegisterModule(pluginCallHandler{})
	httpcaddyfile.RegisterHandlerDirective(contract.Directive, parsePluginCallHandler)
	httpcaddyfile.RegisterDirectiveOrder(contract.Directive, httpcaddyfile.Before, contract.OrderAnchor)
}

func (dispatchApp) CaddyModule() caddycore.ModuleInfo {
	contract, _ := loadPluginHandlerContract()
	return caddycore.ModuleInfo{ID: caddycore.ModuleID(contract.App), New: func() caddycore.Module { return new(dispatchApp) }}
}

func (app *dispatchApp) Provision(ctx caddycore.Context) error {
	contract, err := loadPluginHandlerContract()
	if err != nil {
		return err
	}
	app.bindings = make(map[string]pluginBinding, len(app.Instances))
	for _, instance := range app.Instances {
		if instance.Name == "" || instance.Endpoint == "" || instance.Timeout <= 0 || instance.StartTimeout <= 0 || instance.MaxConcurrentCalls < 1 {
			return errors.New(contract.Diagnostics.InvalidInstance)
		}
		if _, duplicate := app.bindings[instance.Name]; duplicate {
			return errors.New(contract.Diagnostics.InvalidInstance)
		}
		client, err := plugins.NewClient(instance.Endpoint, instance.Timeout, instance.StartTimeout)
		if err != nil {
			return err
		}
		manifest, err := client.VerifyReady(ctx)
		if err != nil || manifest.GetName() != instance.Name {
			_ = client.Close()
			app.closeBindings()
			return errors.New(contract.Diagnostics.InstanceUnavailable)
		}
		capabilities := manifest.GetCapabilities()
		modes := make(map[string]struct{})
		invocationModes := make(map[string]map[pluginv1.InvocationMode]struct{})
		for _, descriptor := range manifest.GetCapabilityDescriptors() {
			declaredModes := make(map[pluginv1.InvocationMode]struct{}, len(descriptor.GetModes()))
			for _, mode := range descriptor.GetModes() {
				declaredModes[mode] = struct{}{}
			}
			invocationModes[descriptor.GetCapability()] = declaredModes
			if plugins.ManifestSupportsCall(manifest, descriptor.GetCapability()) {
				modes[descriptor.GetCapability()] = struct{}{}
			}
		}
		cookiePolicies := make(map[string]plugins.CookiePolicy, len(instance.CookiePolicies))
		for _, rawPolicy := range instance.CookiePolicies {
			policy, err := plugins.DecodeCookiePolicy(rawPolicy)
			if err != nil || policy.InstanceID != instance.Name {
				_ = client.Close()
				app.closeBindings()
				return errors.New(contract.Diagnostics.InvalidCookiePolicy)
			}
			if _, supported := modes[policy.Capability]; !supported {
				_ = client.Close()
				app.closeBindings()
				return errors.New(contract.Diagnostics.InvalidCookiePolicy)
			}
			if _, duplicate := cookiePolicies[policy.Capability]; duplicate {
				_ = client.Close()
				app.closeBindings()
				return errors.New(contract.Diagnostics.InvalidCookiePolicy)
			}
			cookiePolicies[policy.Capability] = policy
		}
		capabilityClient, err := plugins.NewCapabilityClient(client, instance.MaxConcurrentCalls, capabilities...)
		if err != nil {
			_ = client.Close()
			app.closeBindings()
			return err
		}
		app.bindings[instance.Name] = pluginBinding{client: capabilityClient, conn: client, modes: modes, invocationModes: invocationModes, cookies: cookiePolicies, timeout: instance.Timeout}
	}
	return nil
}

func (app *dispatchApp) Start() error { return nil }

func (app *dispatchApp) Stop() error {
	app.closeBindings()
	return nil
}

func (app *dispatchApp) closeBindings() {
	for _, binding := range app.bindings {
		_ = binding.conn.Close()
	}
	app.bindings = nil
}

func (pluginCallHandler) CaddyModule() caddycore.ModuleInfo {
	contract, _ := loadPluginHandlerContract()
	return caddycore.ModuleInfo{ID: caddycore.ModuleID(contract.HandlerModule), New: func() caddycore.Module { return new(pluginCallHandler) }}
}

func (handler *pluginCallHandler) Provision(ctx caddycore.Context) error {
	contract, err := loadPluginHandlerContract()
	if err != nil {
		return err
	}
	handler.contract = contract
	if handler.Mode != contract.CallMode {
		return errors.New(contract.Diagnostics.UnsupportedMode)
	}
	value, err := ctx.App(contract.App)
	if err != nil {
		return errors.New(contract.Diagnostics.InvalidInstance)
	}
	app, ok := value.(*dispatchApp)
	if !ok {
		return errors.New(contract.Diagnostics.InvalidInstance)
	}
	binding, ok := app.bindings[handler.Instance]
	if !ok {
		return errors.New(contract.Diagnostics.InvalidInstance)
	}
	if _, supported := binding.modes[handler.Capability]; !supported {
		return errors.New(contract.Diagnostics.UnsupportedMode)
	}
	handler.binding = binding
	return nil
}

func (handler *pluginCallHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request, _ caddyhttp.Handler) error {
	if request.ContentLength > handler.contract.MaxRequestBytes {
		writer.WriteHeader(http.StatusRequestEntityTooLarge)
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, handler.contract.MaxRequestBytes+1))
	if err != nil || int64(len(body)) > handler.contract.MaxRequestBytes {
		writer.WriteHeader(http.StatusRequestEntityTooLarge)
		return nil
	}
	blockedRequestHeaders := make(map[string]struct{}, len(handler.contract.BlockedRequestHeaders))
	for _, name := range handler.contract.BlockedRequestHeaders {
		blockedRequestHeaders[http.CanonicalHeaderKey(name)] = struct{}{}
	}
	headers := make(map[string]string, len(request.Header))
	for name, values := range request.Header {
		if _, forbidden := blockedRequestHeaders[http.CanonicalHeaderKey(name)]; forbidden || len(values) == 0 {
			continue
		}
		headers[name] = strings.Join(values, ",")
	}
	var cookies []plugins.CookiePair
	if values := request.Header.Values("Cookie"); len(values) > 0 {
		parsedCookies, err := plugins.ParseCookieHeader(values)
		if err != nil {
			writer.WriteHeader(handler.contract.InvalidRequestStatus)
			return nil
		}
		if policy, allowed := handler.binding.cookies[handler.Capability]; allowed {
			cookies, err = plugins.FilterCookiePairs(policy, handler.Instance, handler.Capability, parsedCookies)
			if err != nil {
				writer.WriteHeader(handler.contract.InvalidRequestStatus)
				return nil
			}
		}
	}
	callContext, cancel := context.WithTimeout(request.Context(), handler.binding.timeout)
	defer cancel()
	response, err := handler.binding.client.HTTP(callContext, handler.Capability, plugins.HTTPRequest{
		Method: request.Method, Path: request.URL.Path, Query: request.URL.RawQuery, Headers: headers,
		Body: body, RequestID: uuid.NewString(), RemoteAddr: request.RemoteAddr, Cookies: cookies, Host: request.Host,
	})
	if err != nil {
		if errors.Is(err, plugins.ErrInvalidHTTPResponseAction) {
			writer.WriteHeader(handler.contract.InvalidResponseStatus)
			return nil
		}
		writer.WriteHeader(handler.contract.UnavailableStatus)
		return nil
	}
	blockedResponseHeaders := make(map[string]struct{}, len(handler.contract.BlockedResponseHeaders))
	for _, name := range handler.contract.BlockedResponseHeaders {
		blockedResponseHeaders[http.CanonicalHeaderKey(name)] = struct{}{}
	}
	for name := range response.Headers {
		if _, blocked := blockedResponseHeaders[http.CanonicalHeaderKey(name)]; blocked {
			writer.WriteHeader(handler.contract.InvalidResponseStatus)
			return nil
		}
	}
	for name, value := range response.Headers {
		writer.Header().Set(name, value)
	}
	for _, value := range response.SetCookieHeaders {
		writer.Header().Add("Set-Cookie", value)
	}
	writer.WriteHeader(response.Status)
	if len(response.Body) > 0 {
		_, _ = writer.Write(response.Body)
	}
	return nil
}

func parsePluginCallHandler(helper httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	contract, err := loadPluginHandlerContract()
	if err != nil {
		return nil, err
	}
	helper.Next()
	args := make([]string, 0, 3)
	for len(args) < 3 && helper.NextArg() {
		args = append(args, helper.Val())
	}
	if len(args) != 3 || helper.NextArg() || args[0] == "" || args[1] == "" || args[2] == "" {
		return nil, errors.New(contract.Diagnostics.InvalidDirective)
	}
	streamContract, err := loadHTTPStreamHandlerContract()
	if err != nil {
		return nil, err
	}
	if args[2] != streamContract.CallMode {
		if !streamContract.SupportsMode(args[2]) {
			return nil, errors.New(streamContract.Diagnostics.UnsupportedMode)
		}
		return &httpStreamHandler{Instance: args[0], Capability: args[1], Mode: args[2]}, nil
	}
	return &pluginCallHandler{Instance: args[0], Capability: args[1], Mode: args[2]}, nil
}

func loadPluginHandlerContract() (pluginHandlerContract, error) {
	contents, err := coreassets.Contract(coreassets.CaddyPlugin)
	if err != nil {
		return pluginHandlerContract{}, err
	}
	var contract pluginHandlerContract
	if err := json.Unmarshal(contents, &contract); err != nil {
		return pluginHandlerContract{}, err
	}
	return contract, nil
}

func pluginDispatchAppConfig(instances []PluginInstance) (string, json.RawMessage, error) {
	contract, err := loadPluginHandlerContract()
	if err != nil {
		return "", nil, err
	}
	contents, err := json.Marshal(dispatchAppConfig{Instances: instances})
	return contract.App, contents, err
}

var _ caddycore.App = (*dispatchApp)(nil)
var _ caddycore.Provisioner = (*dispatchApp)(nil)
var _ caddycore.Provisioner = (*pluginCallHandler)(nil)
var _ caddyhttp.MiddlewareHandler = (*pluginCallHandler)(nil)
