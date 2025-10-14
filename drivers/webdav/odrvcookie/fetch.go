package odrvcookie

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CookieAuth estructura simple de autenticación
type CookieAuth struct {
	user     string
	pass     string
	endpoint string
	tenantID string
}

// TokenResponse respuesta del token OAuth2
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   string `json:"expires_in"` // Viene como string a veces
}

// GetExpiresInSeconds convierte expires_in a int
func (t *TokenResponse) GetExpiresInSeconds() int {
	if t.ExpiresIn == "" {
		return 3600 // Default 1 hora
	}
	// Intentar convertir a int
	var seconds int
	fmt.Sscanf(t.ExpiresIn, "%d", &seconds)
	if seconds <= 0 {
		return 3600
	}
	return seconds
}

// RenderListDataResponse respuesta de SharePoint con driveAccessToken
type RenderListDataResponse struct {
	ListData struct {
		DriveAccessToken string `json:".driveAccessToken"`
		DriveUrl         string `json:".driveUrl"`
	} `json:"ListData"`
}

// New crea una instancia de CookieAuth
func New(pUser, pPass, pEndpoint string) CookieAuth {
	return NewWithTenant(pUser, pPass, pEndpoint, "")
}

// NewWithTenant crea una instancia con tenant ID específico
func NewWithTenant(pUser, pPass, pEndpoint, pTenantID string) CookieAuth {
	tenantID := pTenantID
	if tenantID == "" {
		tenantID = "common" // Default
	}
	
	return CookieAuth{
		user:     pUser,
		pass:     pPass,
		endpoint: pEndpoint,
		tenantID: tenantID,
	}
}

// GetAccessToken obtiene el token usando ROPC
func (ca *CookieAuth) GetAccessToken() (*TokenResponse, error) {
	parsedURL, err := url.Parse(ca.endpoint)
	if err != nil {
		return nil, fmt.Errorf("URL inválida: %w", err)
	}
	
	hostname := parsedURL.Host
	resource := fmt.Sprintf("https://%s", hostname)
	
	// Usar tenant específico o common
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/token", ca.tenantID)
	
	// Client ID de Microsoft Office
	clientID := "d3590ed6-52b3-4102-aeff-aad2292ab01c"
	
	data := url.Values{}
	data.Set("resource", resource)
	data.Set("client_id", clientID)
	data.Set("grant_type", "password")
	data.Set("username", ca.user)
	data.Set("password", ca.pass)
	data.Set("scope", "openid")
	
	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error %d: %s", resp.StatusCode, string(body))
	}
	
	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("error parseando token: %w", err)
	}
	
	return &tokenResp, nil
}

// GetDriveAccessToken obtiene el driveAccessToken específico para archivos
// Este es el token que realmente necesitas para WebDAV/Graph API
func (ca *CookieAuth) GetDriveAccessToken() (string, error) {
	// Primero obtener el token OAuth2 base
	baseToken, err := ca.GetAccessToken()
	if err != nil {
		return "", fmt.Errorf("error obteniendo token base: %w", err)
	}
	
	// Extraer información del endpoint
	parsedURL, err := url.Parse(ca.endpoint)
	if err != nil {
		return "", err
	}
	
	// Construir el path para RenderListData
	sitePath := parsedURL.Path
	
	// Si el path ya viene en la URL, usarlo directamente
	// Ej: https://tenant-my.sharepoint.com/personal/user/Documents -> /personal/user/Documents
	// Ej: https://tenant.sharepoint.com/sites/sitename/Shared Documents -> /sites/sitename/Shared Documents
	if sitePath != "" && sitePath != "/" {
		// Ya tenemos el path completo, usarlo tal cual
		// No hacer nada, sitePath ya está bien
	} else {
		// Si no hay path, intentar adivinar
		if strings.Contains(parsedURL.Host, "-my.sharepoint.com") {
			// OneDrive personal - necesita el path completo
			return "", fmt.Errorf("para OneDrive personal, especifica la URL completa incluyendo /personal/usuario/Documents")
		} else {
			// SharePoint de equipo - usar default
			sitePath = "/Shared Documents"
		}
	}
	
	// Construir URL de RenderListData
	apiURL := fmt.Sprintf("%s://%s/_api/web/GetListUsingPath(DecodedUrl=@a1)/RenderListDataAsStream",
		parsedURL.Scheme, parsedURL.Host)
	
	// Agregar parámetros de query
	params := url.Values{}
	params.Set("@a1", fmt.Sprintf("'%s'", sitePath))
	params.Set("TryNewExperienceSingle", "TRUE")
	
	fullURL := apiURL + "?" + params.Encode()
	
	// Preparar el body con parámetros de renderizado
	renderParams := map[string]interface{}{
		"parameters": map[string]interface{}{
			"RenderOptions": 64,
			"ViewXml":       "<View><Query></Query></View>",
		},
	}
	
	bodyJSON, err := json.Marshal(renderParams)
	if err != nil {
		return "", err
	}
	
	req, err := http.NewRequest("POST", fullURL, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return "", err
	}
	
	// Headers requeridos
	req.Header.Set("Authorization", "Bearer "+baseToken.AccessToken)
	req.Header.Set("Accept", "application/json;odata=verbose")
	req.Header.Set("Content-Type", "application/json;odata=verbose")
	
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("error %d obteniendo driveAccessToken (URL: %s, path: %s): %s", 
			resp.StatusCode, fullURL, sitePath, string(body))
	}
	
	// Parsear respuesta para extraer .driveAccessToken
	var renderResp RenderListDataResponse
	if err := json.Unmarshal(body, &renderResp); err != nil {
		return "", fmt.Errorf("error parseando RenderListData: %w", err)
	}
	
	if renderResp.ListData.DriveAccessToken == "" {
		return "", fmt.Errorf("driveAccessToken no encontrado en la respuesta")
	}
	
	// El driveAccessToken viene como "access_token=XXX"
	// Extraer solo el token
	token := strings.TrimPrefix(renderResp.ListData.DriveAccessToken, "access_token=")
	
	return token, nil
}
