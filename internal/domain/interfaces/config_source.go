package interfaces

type ConfigSource interface {
	Read(path string) ([]byte, error)
}
