package caddy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	coreassets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	plugincontracts "github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	plugintransport "github.com/Liapoldus/pluginprotocol/transport"
	caddycore "github.com/caddyserver/caddy/v2"
	caddyhttp "github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type httpStreamHandlerContract struct {
	App                             string   `json:"app"`
	Directive                       string   `json:"directive"`
	OrderAnchor                     string   `json:"orderAnchor"`
	HandlerModule                   string   `json:"handlerModule"`
	CallMode                        string   `json:"callMode"`
	HTTPStreamMode                  string   `json:"httpStreamMode"`
	WebSocketMode                   string   `json:"websocketMode"`
	SSEMode                         string   `json:"sseMode"`
	HTTPKind                        string   `json:"httpKind"`
	WebSocketKind                   string   `json:"websocketKind"`
	SSEKind                         string   `json:"sseKind"`
	Version                         int      `json:"version"`
	MaxRequestBytes                 int64    `json:"maxRequestBytes"`
	MaxContextBytes                 int      `json:"maxContextBytes"`
	MaxConcurrentStreams            int      `json:"maxConcurrentStreams"`
	UnavailableStatus               int      `json:"unavailableStatus"`
	InvalidResponseStatus           int      `json:"invalidResponseStatus"`
	InvalidRequestStatus            int      `json:"invalidRequestStatus"`
	WebSocketRejectedStatus         int      `json:"websocketRejectedStatus"`
	SSEContentType                  string   `json:"sseContentType"`
	SSECacheControl                 string   `json:"sseCacheControl"`
	ContentTypeHeader               string   `json:"contentTypeHeader"`
	CacheControlHeader              string   `json:"cacheControlHeader"`
	SetCookieHeader                 string   `json:"setCookieHeader"`
	SSEEventPrefix                  string   `json:"sseEventPrefix"`
	SSEIDPrefix                     string   `json:"sseIDPrefix"`
	SSERetryPrefix                  string   `json:"sseRetryPrefix"`
	SSEDataPrefix                   string   `json:"sseDataPrefix"`
	SSELineEnding                   string   `json:"sseLineEnding"`
	SSEEventEnding                  string   `json:"sseEventEnding"`
	DefaultStreamStatus             int      `json:"defaultStreamStatus"`
	ContextVersionField             string   `json:"contextVersionField"`
	ContextKindField                string   `json:"contextKindField"`
	ContextMethodField              string   `json:"contextMethodField"`
	ContextPathField                string   `json:"contextPathField"`
	ContextQueryField               string   `json:"contextQueryField"`
	ContextHeadersField             string   `json:"contextHeadersField"`
	ContextRequestIDField           string   `json:"contextRequestIDField"`
	ContextRemoteAddrField          string   `json:"contextRemoteAddrField"`
	ContextContextField             string   `json:"contextContextField"`
	ContextCookiesField             string   `json:"contextCookiesField"`
	ContextOfferedSubprotocolsField string   `json:"contextOfferedSubprotocolsField"`
	MetadataStatusField             string   `json:"metadataStatusField"`
	MetadataHeadersField            string   `json:"metadataHeadersField"`
	MetadataVersionField            string   `json:"metadataVersionField"`
	CookieHeader                    string   `json:"cookieHeader"`
	WebSocketSubprotocolHeader      string   `json:"websocketSubprotocolHeader"`
	InvalidSSECharacters            string   `json:"invalidSSECharacters"`
	InvalidSSEEventCharacters       string   `json:"invalidSSEEventCharacters"`
	InvalidSSEIDCharacters          string   `json:"invalidSSEIDCharacters"`
	BlockedRequestHeaders           []string `json:"blockedRequestHeaders"`
	BlockedResponseHeaders          []string `json:"blockedResponseHeaders"`
	Diagnostics                     struct {
		InvalidDirective    string `json:"invalidDirective"`
		InvalidInstance     string `json:"invalidInstance"`
		UnsupportedMode     string `json:"unsupportedMode"`
		InstanceUnavailable string `json:"instanceUnavailable"`
		InvalidContext      string `json:"invalidContext"`
		InvalidMessage      string `json:"invalidMessage"`
		InvalidMetadata     string `json:"invalidMetadata"`
		InvalidRequest      string `json:"invalidRequest"`
		RequestTooLarge     string `json:"requestTooLarge"`
		StreamLimitExceeded string `json:"streamLimitExceeded"`
	} `json:"diagnostics"`
}

func (contract httpStreamHandlerContract) SupportsMode(mode string) bool {
	return mode == contract.HTTPStreamMode || mode == contract.WebSocketMode || mode == contract.SSEMode
}

type httpStreamHandler struct {
	Instance    string `json:"instance,omitempty"`
	Capability  string `json:"capability,omitempty"`
	Mode        string `json:"mode,omitempty"`
	binding     pluginBinding
	contract    httpStreamHandlerContract
	mode        pluginv1.InvocationMode
	streamSlots chan struct{}
}

func init() {
	if _, err := loadHTTPStreamHandlerContract(); err != nil {
		panic(err)
	}
	caddycore.RegisterModule(httpStreamHandler{})
}

func (httpStreamHandler) CaddyModule() caddycore.ModuleInfo {
	contract, _ := loadHTTPStreamHandlerContract()
	return caddycore.ModuleInfo{ID: caddycore.ModuleID(contract.HandlerModule), New: func() caddycore.Module { return new(httpStreamHandler) }}
}

func (handler *httpStreamHandler) Provision(ctx caddycore.Context) error {
	contract, err := loadHTTPStreamHandlerContract()
	if err != nil {
		return err
	}
	handler.contract = contract
	if contract.MaxConcurrentStreams < 1 {
		return errors.New(contract.Diagnostics.InvalidInstance)
	}
	handler.streamSlots = make(chan struct{}, contract.MaxConcurrentStreams)
	switch handler.Mode {
	case contract.HTTPStreamMode:
		handler.mode = pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM
	case contract.WebSocketMode:
		handler.mode = pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET
	case contract.SSEMode:
		handler.mode = pluginv1.InvocationMode_INVOCATION_MODE_SSE
	default:
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
	if !ok || handler.Capability == "" {
		return errors.New(contract.Diagnostics.InvalidInstance)
	}
	if _, supported := binding.invocationModes[handler.Capability][handler.mode]; !supported {
		return errors.New(contract.Diagnostics.UnsupportedMode)
	}
	handler.binding = binding
	return nil
}

func (handler *httpStreamHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request, _ caddyhttp.Handler) error {
	if request.ContentLength > handler.contract.MaxRequestBytes {
		writer.WriteHeader(http.StatusRequestEntityTooLarge)
		return nil
	}
	if (handler.mode == pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET || handler.mode == pluginv1.InvocationMode_INVOCATION_MODE_SSE) && request.Method != http.MethodGet {
		writer.WriteHeader(handler.contract.InvalidRequestStatus)
		return nil
	}
	select {
	case handler.streamSlots <- struct{}{}:
		defer func() { <-handler.streamSlots }()
	case <-request.Context().Done():
		return nil
	}
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	stream, err := handler.binding.conn.OpenStream(ctx)
	if err != nil {
		writer.WriteHeader(handler.contract.UnavailableStatus)
		return nil
	}
	open, err := handler.openMessage(request)
	if err != nil || stream.Send(open) != nil {
		writer.WriteHeader(handler.contract.InvalidRequestStatus)
		return nil
	}
	switch handler.mode {
	case pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM:
		return handler.serveHTTPStream(writer, request, stream, cancel)
	case pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET:
		return handler.serveWebSocket(writer, request, stream, cancel)
	case pluginv1.InvocationMode_INVOCATION_MODE_SSE:
		return handler.serveSSE(writer, request, stream)
	default:
		writer.WriteHeader(handler.contract.InvalidRequestStatus)
		return nil
	}
}

func (handler *httpStreamHandler) openMessage(request *http.Request) (*pluginv1.StreamMessage, error) {
	contextObject := map[string]any{
		handler.contract.ContextVersionField:    handler.contract.Version,
		handler.contract.ContextKindField:       handler.kind(),
		handler.contract.ContextMethodField:     request.Method,
		handler.contract.ContextPathField:       request.URL.Path,
		handler.contract.ContextQueryField:      request.URL.RawQuery,
		handler.contract.ContextHeadersField:    handler.safeHeaders(request),
		handler.contract.ContextRequestIDField:  uuid.NewString(),
		handler.contract.ContextRemoteAddrField: request.RemoteAddr,
	}
	if handler.mode == pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM {
		contextObject[handler.contract.ContextContextField] = map[string]string{}
	}
	if values := request.Header.Values(handler.contract.CookieHeader); len(values) > 0 {
		parsed, err := plugins.ParseCookieHeader(values)
		if err != nil {
			return nil, err
		}
		if policy, allowed := handler.binding.cookies[handler.Capability]; allowed {
			parsed, err = plugins.FilterCookiePairs(policy, handler.Instance, handler.Capability, parsed)
			if err != nil {
				return nil, err
			}
		} else {
			parsed = []plugins.CookiePair{}
		}
		contextObject[handler.contract.ContextCookiesField] = parsed
	}
	if handler.mode == pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET {
		contextObject[handler.contract.ContextOfferedSubprotocolsField] = offeredSubprotocols(request, handler.contract.WebSocketSubprotocolHeader)
	}
	encoded, err := json.Marshal(contextObject)
	if err != nil {
		return nil, err
	}
	if len(encoded) > handler.contract.MaxContextBytes {
		return nil, plugins.ErrProtocolViolation
	}
	mode := handler.mode
	return &pluginv1.StreamMessage{Capability: handler.Capability, Body: &pluginv1.StreamMessage_Open{Open: &pluginv1.StreamOpen{Mode: &mode, ConnectionId: uuid.NewString(), ContextJson: encoded}}}, nil
}

func (handler *httpStreamHandler) kind() string {
	switch handler.mode {
	case pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM:
		return handler.contract.HTTPKind
	case pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET:
		return handler.contract.WebSocketKind
	default:
		return handler.contract.SSEKind
	}
}

func (handler *httpStreamHandler) safeHeaders(request *http.Request) map[string]string {
	blocked := make(map[string]struct{}, len(handler.contract.BlockedRequestHeaders))
	for _, name := range handler.contract.BlockedRequestHeaders {
		blocked[http.CanonicalHeaderKey(name)] = struct{}{}
	}
	headers := make(map[string]string, len(request.Header))
	for name, values := range request.Header {
		if _, forbidden := blocked[http.CanonicalHeaderKey(name)]; forbidden || len(values) == 0 {
			continue
		}
		headers[name] = strings.Join(values, ",")
	}
	return headers
}

func (handler *httpStreamHandler) serveHTTPStream(writer http.ResponseWriter, request *http.Request, stream plugins.ProtocolStream, cancel context.CancelFunc) error {
	defer request.Body.Close()
	sendErrors := make(chan error, 1)
	go func() {
		buffer := make([]byte, 32*1024)
		var received int64
		for {
			count, readErr := request.Body.Read(buffer)
			if count > 0 {
				received += int64(count)
				if received > handler.contract.MaxRequestBytes {
					sendErrors <- errors.New(handler.contract.Diagnostics.RequestTooLarge)
					cancel()
					return
				}
				if err := stream.Send(&pluginv1.StreamMessage{Capability: handler.Capability, Body: &pluginv1.StreamMessage_HttpRequestChunk{HttpRequestChunk: &pluginv1.HttpRequestChunk{Payload: append([]byte(nil), buffer[:count]...)}}}); err != nil {
					sendErrors <- err
					cancel()
					return
				}
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					sendErrors <- stream.Send(&pluginv1.StreamMessage{Capability: handler.Capability, Body: &pluginv1.StreamMessage_HttpRequestChunk{HttpRequestChunk: &pluginv1.HttpRequestChunk{EndStream: true}}})
					return
				}
				sendErrors <- readErr
				cancel()
				return
			}
		}
	}()

	started := false
	for {
		message, err := stream.Recv()
		if err != nil {
			if !started {
				cancel()
				select {
				case sendErr := <-sendErrors:
					if sendErr != nil {
						if sendErr.Error() == handler.contract.Diagnostics.RequestTooLarge {
							writer.WriteHeader(http.StatusRequestEntityTooLarge)
							return nil
						}
					}
				default:
				}
				writer.WriteHeader(handler.contract.UnavailableStatus)
			}
			return nil
		}
		switch body := message.GetBody().(type) {
		case *pluginv1.StreamMessage_HttpResponseStart:
			if started || !handler.validStatus(body.HttpResponseStart.GetStatusCode()) || handler.applyHTTPMetadata(writer.Header(), request.Host, body.HttpResponseStart.GetMetadataJson()) != nil {
				if !started {
					writer.WriteHeader(handler.contract.InvalidResponseStatus)
				}
				return nil
			}
			writer.WriteHeader(int(body.HttpResponseStart.GetStatusCode()))
			started = true
		case *pluginv1.StreamMessage_HttpResponseChunk:
			if !started || int64(len(body.HttpResponseChunk.GetPayload())) > transportMaxStreamMessageBytes() {
				return nil
			}
			if _, err := writer.Write(body.HttpResponseChunk.GetPayload()); err != nil {
				return nil
			}
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
			if body.HttpResponseChunk.GetEndStream() {
				return nil
			}
		case *pluginv1.StreamMessage_Close:
			if !started {
				writer.WriteHeader(handler.contract.UnavailableStatus)
			}
			return nil
		default:
			if !started {
				writer.WriteHeader(handler.contract.InvalidResponseStatus)
			}
			return nil
		}
	}
}

func (handler *httpStreamHandler) serveWebSocket(writer http.ResponseWriter, request *http.Request, stream plugins.ProtocolStream, cancel context.CancelFunc) error {
	message, err := stream.Recv()
	if err != nil {
		writer.WriteHeader(handler.contract.UnavailableStatus)
		return nil
	}
	handshake := message.GetWebsocketHandshake()
	if handshake == nil {
		writer.WriteHeader(handler.contract.InvalidResponseStatus)
		return nil
	}
	if !handshake.GetAccepted() {
		if handshake.GetSubprotocol() != "" || handler.applyHTTPMetadata(writer.Header(), request.Host, handshake.GetMetadataJson()) != nil {
			writer.WriteHeader(handler.contract.InvalidResponseStatus)
			return nil
		}
		writer.WriteHeader(handler.contract.WebSocketRejectedStatus)
		return nil
	}
	selected := handshake.GetSubprotocol()
	if selected != "" && !contains(offeredSubprotocols(request, handler.contract.WebSocketSubprotocolHeader), selected) {
		writer.WriteHeader(handler.contract.InvalidResponseStatus)
		return nil
	}
	if handler.applyHTTPMetadata(writer.Header(), request.Host, handshake.GetMetadataJson()) != nil {
		writer.WriteHeader(handler.contract.InvalidResponseStatus)
		return nil
	}
	protocols := []string(nil)
	if selected != "" {
		protocols = []string{selected}
	}
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{Subprotocols: protocols})
	if err != nil {
		return nil
	}
	connection.SetReadLimit(transportMaxStreamMessageBytes())
	defer connection.Close(websocket.StatusNormalClosure, "")
	go func() {
		for {
			kind, payload, readErr := connection.Read(request.Context())
			if readErr != nil {
				cancel()
				return
			}
			messageKind := pluginv1.WebSocketMessageKind_WEBSOCKET_MESSAGE_KIND_BINARY
			if kind == websocket.MessageText {
				messageKind = pluginv1.WebSocketMessageKind_WEBSOCKET_MESSAGE_KIND_TEXT
			}
			if err := stream.Send(&pluginv1.StreamMessage{Capability: handler.Capability, Body: &pluginv1.StreamMessage_WebsocketMessage{WebsocketMessage: &pluginv1.WebSocketMessage{Kind: messageKind, Payload: payload, Direction: pluginv1.StreamDirection_STREAM_DIRECTION_REQUEST}}}); err != nil {
				cancel()
				return
			}
		}
	}()
	for {
		message, err := stream.Recv()
		if err != nil {
			return nil
		}
		frame := message.GetWebsocketMessage()
		if frame == nil || frame.GetDirection() != pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE {
			return nil
		}
		kind := websocket.MessageBinary
		if frame.GetKind() == pluginv1.WebSocketMessageKind_WEBSOCKET_MESSAGE_KIND_TEXT {
			kind = websocket.MessageText
		} else if frame.GetKind() != pluginv1.WebSocketMessageKind_WEBSOCKET_MESSAGE_KIND_BINARY {
			return nil
		}
		if err := connection.Write(request.Context(), kind, frame.GetPayload()); err != nil {
			return nil
		}
	}
}

func (handler *httpStreamHandler) serveSSE(writer http.ResponseWriter, request *http.Request, stream plugins.ProtocolStream) error {
	started := false
	for {
		message, err := stream.Recv()
		if err != nil {
			if !started {
				writer.WriteHeader(handler.contract.UnavailableStatus)
			}
			return nil
		}
		if event := message.GetSseEvent(); event != nil {
			if !handler.validStreamSSEEvent(event) {
				if !started {
					writer.WriteHeader(handler.contract.InvalidResponseStatus)
				}
				return nil
			}
			if !started {
				writer.Header().Set(handler.contract.ContentTypeHeader, handler.contract.SSEContentType)
				writer.Header().Set(handler.contract.CacheControlHeader, handler.contract.SSECacheControl)
				writer.WriteHeader(http.StatusOK)
				started = true
			}
			flusher, canFlush := writer.(http.Flusher)
			if event.GetEvent() != "" {
				_, _ = io.WriteString(writer, handler.contract.SSEEventPrefix+event.GetEvent()+handler.contract.SSELineEnding)
			}
			if event.GetId() != "" {
				_, _ = io.WriteString(writer, handler.contract.SSEIDPrefix+event.GetId()+handler.contract.SSELineEnding)
			}
			if event.RetryMillis != nil {
				_, _ = io.WriteString(writer, handler.contract.SSERetryPrefix+strconv.FormatUint(uint64(event.GetRetryMillis()), 10)+handler.contract.SSELineEnding)
			}
			for _, line := range strings.Split(event.GetData(), handler.contract.SSELineEnding) {
				_, _ = io.WriteString(writer, handler.contract.SSEDataPrefix+line+handler.contract.SSELineEnding)
			}
			_, _ = io.WriteString(writer, handler.contract.SSEEventEnding)
			if canFlush {
				flusher.Flush()
			}
		} else if message.GetClose() != nil {
			if !started {
				writer.Header().Set(handler.contract.ContentTypeHeader, handler.contract.SSEContentType)
				writer.Header().Set(handler.contract.CacheControlHeader, handler.contract.SSECacheControl)
				writer.WriteHeader(http.StatusOK)
			}
			return nil
		} else {
			if !started {
				writer.WriteHeader(handler.contract.InvalidResponseStatus)
			}
			return nil
		}
	}
}

func (handler *httpStreamHandler) validStatus(status uint32) bool {
	return status >= 200 && status <= 599
}

func (handler *httpStreamHandler) applyHTTPMetadata(headers http.Header, requestHost string, metadata []byte) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &values); err != nil {
		return err
	}
	version, present := values[handler.contract.MetadataVersionField]
	if !present {
		return plugins.ErrInvalidHTTPResponseAction
	}
	var metadataVersion int
	if err := json.Unmarshal(version, &metadataVersion); err != nil || metadataVersion != handler.contract.Version {
		return plugins.ErrInvalidHTTPResponseAction
	}
	delete(values, handler.contract.MetadataVersionField)
	statusJSON, err := json.Marshal(handler.contract.DefaultStreamStatus)
	if err != nil {
		return err
	}
	values[handler.contract.MetadataStatusField] = statusJSON
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	_, cookies, err := plugincontracts.DecodeHTTPResponseAction(encoded, requestHost)
	if err != nil {
		return err
	}
	var action plugincontracts.HTTPResponseAction
	if err := json.Unmarshal(encoded, &action); err != nil {
		return err
	}
	blocked := make(map[string]struct{}, len(handler.contract.BlockedResponseHeaders))
	for _, name := range handler.contract.BlockedResponseHeaders {
		blocked[http.CanonicalHeaderKey(name)] = struct{}{}
	}
	for name := range action.Headers {
		if _, forbidden := blocked[http.CanonicalHeaderKey(name)]; forbidden {
			return plugins.ErrInvalidHTTPResponseAction
		}
	}
	for name, value := range action.Headers {
		headers.Set(name, value)
	}
	for _, cookie := range cookies {
		headers.Add(handler.contract.SetCookieHeader, cookie)
	}
	return nil
}

func offeredSubprotocols(request *http.Request, header string) []string {
	var protocols []string
	seen := make(map[string]struct{})
	for _, headerValue := range request.Header.Values(header) {
		for _, value := range strings.Split(headerValue, ",") {
			protocol := strings.TrimSpace(value)
			if protocol == "" {
				continue
			}
			if _, duplicate := seen[protocol]; duplicate {
				continue
			}
			seen[protocol] = struct{}{}
			protocols = append(protocols, protocol)
		}
	}
	return protocols
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (handler *httpStreamHandler) validStreamSSEEvent(event *pluginv1.SseEvent) bool {
	return event != nil && !strings.ContainsAny(event.GetData(), handler.contract.InvalidSSECharacters) && !strings.ContainsAny(event.GetEvent(), handler.contract.InvalidSSEEventCharacters) && !strings.ContainsAny(event.GetId(), handler.contract.InvalidSSEIDCharacters)
}

func transportMaxStreamMessageBytes() int64 {
	return int64(plugintransport.MaxStreamMessageBytes)
}

func loadHTTPStreamHandlerContract() (httpStreamHandlerContract, error) {
	contents, err := coreassets.Contract(coreassets.CaddyHTTPStream)
	if err != nil {
		return httpStreamHandlerContract{}, err
	}
	var contract httpStreamHandlerContract
	if err := json.Unmarshal(contents, &contract); err != nil {
		return httpStreamHandlerContract{}, err
	}
	return contract, nil
}

var _ caddycore.Provisioner = (*httpStreamHandler)(nil)
var _ caddyhttp.MiddlewareHandler = (*httpStreamHandler)(nil)
