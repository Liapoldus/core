package security

import (
	"io"
	"net/netip"
	"sync"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/oschwald/maxminddb-golang/v2"
)

type MMDBRegistry struct {
	mu      sync.RWMutex
	readers map[string]*maxminddb.Reader
}

func NewMMDBRegistry(providers map[string]models.DataProvider) *MMDBRegistry {
	registry := &MMDBRegistry{readers: make(map[string]*maxminddb.Reader, len(providers))}
	for _, provider := range providers {
		if _, exists := registry.readers[provider.Path]; exists {
			continue
		}
		reader, err := openMMDB(provider.Path)
		if err != nil {
			continue
		}
		registry.readers[provider.Path] = reader
	}
	return registry
}

func NewVerifiedMMDBRegistry(providers map[string]models.DataProvider) (*MMDBRegistry, error) {
	registry := &MMDBRegistry{readers: make(map[string]*maxminddb.Reader, len(providers))}
	for _, provider := range providers {
		if _, exists := registry.readers[provider.Path]; exists {
			continue
		}
		reader, err := openMMDB(provider.Path)
		if err != nil {
			registry.Close()
			return nil, err
		}
		registry.readers[provider.Path] = reader
	}
	return registry, nil
}

func openMMDB(path string) (*maxminddb.Reader, error) {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	if err := reader.Verify(); err != nil {
		_ = reader.Close()
		return nil, err
	}
	return reader, nil
}

func (registry *MMDBRegistry) Lookup(provider models.DataProvider, ip netip.Addr) (models.GeoRecord, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	reader := registry.readers[provider.Path]
	if reader == nil {
		return models.GeoRecord{}, io.EOF
	}
	var record *struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
		City struct {
			Names map[string]string `maxminddb:"names"`
		} `maxminddb:"city"`
		ASN *uint `maxminddb:"autonomous_system_number"`
	}
	if err := reader.Lookup(ip).Decode(&record); err != nil {
		return models.GeoRecord{}, err
	}
	if record == nil {
		return models.GeoRecord{}, io.EOF
	}
	return models.GeoRecord{Country: record.Country.ISOCode, City: record.City.Names["en"], ASN: record.ASN}, nil
}

func (registry *MMDBRegistry) Close() {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for name, reader := range registry.readers {
		_ = reader.Close()
		delete(registry.readers, name)
	}
}
