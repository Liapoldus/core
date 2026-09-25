package caddy

import (
	"encoding/json"
	"errors"
	"io"

	coreassets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	caddycore "github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/google/uuid"
	"github.com/mholt/caddy-l4/layer4"
)

type l4PluginHandlerContract struct {
	Directive       string `json:"directive"`
	HandlerModule   string `json:"handlerModule"`
	TCPMode         string `json:"tcpMode"`
	UDPMode         string `json:"udpMode"`
	ReadBufferBytes int    `json:"readBufferBytes"`
	Diagnostics     struct {
		InvalidDirective    string `json:"invalidDirective"`
		AppUnavailable      string `json:"appUnavailable"`
		AppInvalid          string `json:"appInvalid"`
		BindingMissing      string `json:"bindingMissing"`
		UnsupportedMode     string `json:"unsupportedMode"`
		InstanceUnavailable string `json:"instanceUnavailable"`
	} `json:"diagnostics"`
}

type l4PluginHandler struct {
	Instance       string `json:"instance,omitempty"`
	Capability     string `json:"capability,omitempty"`
	Mode           string `json:"mode,omitempty"`
	binding        pluginBinding
	contract       l4PluginHandlerContract
	invocationMode pluginv1.InvocationMode
}

func init() {
	if _, err := loadL4PluginHandlerContract(); err != nil {
		panic(err)
	}
	caddycore.RegisterModule(l4PluginHandler{})
}

func (l4PluginHandler) CaddyModule() caddycore.ModuleInfo {
	contract, _ := loadL4PluginHandlerContract()
	return caddycore.ModuleInfo{ID: caddycore.ModuleID(contract.HandlerModule), New: func() caddycore.Module { return new(l4PluginHandler) }}
}

func (handler *l4PluginHandler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	contract, err := loadL4PluginHandlerContract()
	if err != nil {
		return err
	}
	if !d.Next() || d.Val() != contract.Directive {
		return errors.New(contract.Diagnostics.InvalidDirective)
	}
	args := make([]string, 0, 3)
	for len(args) < 3 && d.NextArg() {
		args = append(args, d.Val())
	}
	if len(args) != 3 || d.NextArg() || args[0] == "" || args[1] == "" || args[2] == "" || d.NextBlock(d.Nesting()) {
		return errors.New(contract.Diagnostics.InvalidDirective)
	}
	handler.Instance = args[0]
	handler.Capability = args[1]
	handler.Mode = args[2]
	return nil
}

func (handler *l4PluginHandler) Provision(ctx caddycore.Context) error {
	contract, err := loadL4PluginHandlerContract()
	if err != nil {
		return err
	}
	handler.contract = contract
	var requiredMode pluginv1.InvocationMode
	switch handler.Mode {
	case contract.TCPMode:
		requiredMode = pluginv1.InvocationMode_INVOCATION_MODE_TCP
	case contract.UDPMode:
		requiredMode = pluginv1.InvocationMode_INVOCATION_MODE_UDP
	default:
		return errors.New(contract.Diagnostics.UnsupportedMode)
	}
	pluginContract, err := loadPluginHandlerContract()
	if err != nil {
		return err
	}
	value, err := ctx.App(pluginContract.App)
	if err != nil {
		return errors.Join(errors.New(contract.Diagnostics.AppUnavailable), err)
	}
	app, ok := value.(*dispatchApp)
	if !ok {
		return errors.New(contract.Diagnostics.AppInvalid)
	}
	binding, ok := app.bindings[handler.Instance]
	if !ok || handler.Capability == "" {
		return errors.New(contract.Diagnostics.BindingMissing)
	}
	if _, supported := binding.invocationModes[handler.Capability][requiredMode]; !supported {
		return errors.New(contract.Diagnostics.UnsupportedMode)
	}
	handler.binding = binding
	handler.invocationMode = requiredMode
	return nil
}

func (handler *l4PluginHandler) Handle(connection *layer4.Connection, _ layer4.Handler) error {
	if handler.invocationMode == pluginv1.InvocationMode_INVOCATION_MODE_UDP {
		return handler.handleUDP(connection)
	}
	session, err := handler.openL4Session(connection, handler.contract.TCPMode)
	if err != nil {
		return errors.New(handler.contract.Diagnostics.InstanceUnavailable)
	}
	defer session.Close()

	buffer := make([]byte, handler.contract.ReadBufferBytes)
	for {
		count, readErr := connection.Read(buffer)
		if count > 0 {
			response, exchangeErr := session.Exchange(buffer[:count])
			if exchangeErr != nil {
				return errors.New(handler.contract.Diagnostics.InstanceUnavailable)
			}
			if response.Drop {
				return nil
			}
			if err := writeAll(connection, response.Payload); err != nil {
				return err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func (handler *l4PluginHandler) handleUDP(connection *layer4.Connection) error {
	buffer := make([]byte, handler.contract.ReadBufferBytes)
	for {
		count, readErr := connection.Read(buffer)
		if count > 0 || readErr == nil {
			session, err := handler.openL4Session(connection, handler.contract.UDPMode)
			if err != nil {
				return errors.New(handler.contract.Diagnostics.InstanceUnavailable)
			}
			response, exchangeErr := session.Exchange(buffer[:count])
			if exchangeErr != nil {
				_ = session.Close()
				return errors.New(handler.contract.Diagnostics.InstanceUnavailable)
			}
			if err := session.Close(); err != nil {
				return errors.New(handler.contract.Diagnostics.InstanceUnavailable)
			}
			if !response.Drop {
				if err := writeDatagram(connection, response.Payload); err != nil {
					return err
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func (handler *l4PluginHandler) openL4Session(connection *layer4.Connection, transport string) (plugins.L4Session, error) {
	return handler.binding.client.OpenL4Stream(connection.Context, handler.Capability, plugins.L4StreamContext{
		Transport:   transport,
		Connection:  uuid.NewString(),
		Source:      connection.RemoteAddr().String(),
		Destination: connection.LocalAddr().String(),
	})
}

func loadL4PluginHandlerContract() (l4PluginHandlerContract, error) {
	contents, err := coreassets.Contract(coreassets.CaddyL4Plugin)
	if err != nil {
		return l4PluginHandlerContract{}, err
	}
	var contract l4PluginHandlerContract
	if err := json.Unmarshal(contents, &contract); err != nil {
		return l4PluginHandlerContract{}, err
	}
	return contract, nil
}

func writeAll(writer io.Writer, contents []byte) error {
	for len(contents) > 0 {
		written, err := writer.Write(contents)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		contents = contents[written:]
	}
	return nil
}

func writeDatagram(writer io.Writer, contents []byte) error {
	written, err := writer.Write(contents)
	if err != nil {
		return err
	}
	if written != len(contents) {
		return io.ErrShortWrite
	}
	return nil
}

var _ caddycore.Provisioner = (*l4PluginHandler)(nil)
var _ layer4.NextHandler = (*l4PluginHandler)(nil)
var _ caddyfile.Unmarshaler = (*l4PluginHandler)(nil)
