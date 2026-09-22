package interfaces

import (
	"net/netip"

	"github.com/Liapoldus/core/internal/domain/models"
)

type GeoLookup func(models.DataProvider, netip.Addr) (models.GeoRecord, error)
