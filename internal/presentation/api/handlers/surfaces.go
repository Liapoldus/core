package handlers

func CloneAdminSurfaces(surfaces []AdminSurface) []AdminSurface {
	return append([]AdminSurface(nil), surfaces...)
}
