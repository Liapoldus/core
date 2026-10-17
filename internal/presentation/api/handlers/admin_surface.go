package handlers

type AdminSurface struct {
	Service      string   `json:"service"`
	Namespace    string   `json:"namespace"`
	Version      string   `json:"version"`
	Title        string   `json:"title"`
	Capabilities []string `json:"capabilities"`
}
