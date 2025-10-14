package odrvcookie

import (
	"fmt"
)

// GetCookie obtiene el driveAccessToken para SharePoint
// Este es el token real que necesitas para acceder a archivos via WebDAV/Graph
func GetCookie(username, password, siteUrl string) (string, error) {
	return GetCookieWithTenant(username, password, siteUrl, "")
}

// GetCookieWithTenant obtiene el driveAccessToken con un tenant ID específico
func GetCookieWithTenant(username, password, siteUrl, tenantID string) (string, error) {
	ca := NewWithTenant(username, password, siteUrl, tenantID)
	
	// Obtener el driveAccessToken (no el token OAuth2 base)
	driveToken, err := ca.GetDriveAccessToken()
	if err != nil {
		return "", err
	}
	
	// Retorna el header completo listo para usar
	return fmt.Sprintf("Bearer %s", driveToken), nil
}
