package interfaces

type PluginSupervisor interface {
	Restart(instance string) error
}
