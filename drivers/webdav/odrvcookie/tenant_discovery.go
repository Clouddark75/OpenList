package odrvcookie

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenIDConfig contiene la configuración de OpenID del tenant
type OpenIDConfig struct {
	TokenEndpoint string `json:"token_endpoint"`
	Issuer        string `json:"issuer"`
}

// TenantInfo contiene información del tenant
type TenantInfo struct {
	TenantID     string
	TenantRegion string
}

// DiscoverTenantID obtiene el Tenant ID desde el dominio del email o SharePoint URL
func DiscoverTenantID(emailOrDomain string) (string, error) {
	// Extraer dominio del email
	domain := emailOrDomain
	if strings.Contains(emailOrDomain, "@") {
		parts := strings.Split(emailOrDomain, "@")
		if len(parts) != 2 {
			return "", fmt.Errorf("formato de email inválido")
		}
		domain = parts[1]
	}
	
	// Si es una URL de SharePoint, extraer el tenant
	if strings.Contains(domain, ".sharepoint.com") {
		parts := strings.Split(domain, ".")
		if len(parts) > 0 {
			domain = parts[0] + ".onmicrosoft.com"
		}
	}

	// Método 1: OpenID Configuration
	tenantID, err := getTenantIDFromOpenID(domain)
	if err == nil && tenantID != "" {
		return tenantID, nil
	}

	// Método 2: Usar el dominio directamente si es .onmicrosoft.com
	if strings.HasSuffix(domain, ".onmicrosoft.com") {
		return domain, nil
	}

	return "", fmt.Errorf("no se pudo determinar el tenant ID para el dominio: %s", domain)
}

// getTenantIDFromOpenID obtiene el tenant ID desde OpenID configuration
func getTenantIDFromOpenID(domain string) (string, error) {
	// Intentar con el dominio específico
	openIDURL := fmt.Sprintf("https://login.microsoftonline.com/%s/.well-known/openid-configuration", domain)
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(openIDURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status code: %d", resp.StatusCode)
	}

	var config OpenIDConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return "", err
	}

	// Extraer tenant ID del issuer
	// Formato: https://login.microsoftonline.com/{tenant-id}/v2.0
	parts := strings.Split(config.Issuer, "/")
	for i, part := range parts {
		if part == "login.microsoftonline.com" && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}

	return "", fmt.Errorf("no se pudo extraer tenant ID del issuer")
}

// DiscoverTenantIDFromSharePoint obtiene el tenant ID desde la URL de SharePoint
func DiscoverTenantIDFromSharePoint(sharePointURL string) (string, error) {
	// Hacer una petición HEAD al SharePoint para obtener headers con tenant info
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // No seguir redirects
		},
	}
	
	resp, err := client.Head(sharePointURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Buscar en el header SPRequestGuid o similar
	if tenantID := resp.Header.Get("SPRequestGuid"); tenantID != "" {
		return tenantID, nil
	}

	// Si no hay headers útiles, extraer del dominio
	if strings.Contains(sharePointURL, ".sharepoint.com") {
		// Formato: https://universidad.sharepoint.com/...
		parts := strings.Split(sharePointURL, "/")
		if len(parts) >= 3 {
			hostParts := strings.Split(parts[2], ".")
			if len(hostParts) > 0 {
				tenantName := hostParts[0]
				return tenantName + ".onmicrosoft.com", nil
			}
		}
	}

	return "", fmt.Errorf("no se pudo determinar tenant ID desde SharePoint URL")
}
