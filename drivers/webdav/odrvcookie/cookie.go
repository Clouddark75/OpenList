package odrvcookie

import (
	"fmt"
)

// GetCookie obtiene el token OAuth2 para SharePoint/OneDrive
// Basado en el approach de rclone: usar el token OAuth2 directamente sin RenderListData
func GetCookie(username, password, siteUrl string) (string, error) {
	return GetCookieWithTenant(username, password, siteUrl, "")
}

// GetCookieWithTenant obtiene el token con un tenant ID específico
func GetCookieWithTenant(username, password, siteUrl, tenantID string) (string, error) {
	ca := NewWithTenant(username, password, siteUrl, tenantID)
	
	// Obtener el token OAuth2 directamente - NO necesitamos driveAccessToken
	token, err := ca.GetAccessToken()
	if err != nil {
		return "", err
	}
	
	// Retorna el header completo listo para usar
	return fmt.Sprintf("Bearer %s", token.AccessToken), nil
}
