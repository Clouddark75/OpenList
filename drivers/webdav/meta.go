package webdav

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	Vendor   string `json:"vendor" type:"select" options:"sharepoint,other" default:"other"`
	Address  string `json:"address" required:"true"`
	Username string `json:"username" required:"true"`
	Password string `json:"password" required:"true"`
	TenantID string `json:"tenant_id" help:"Opcional para SharePoint: ID del tenant (ej: 83daeb17-e8de-45a3-ae70-69c9dbdf4cbf). Déjalo vacío para auto-detectar."`
	driver.RootPath
	TlsInsecureSkipVerify bool `json:"tls_insecure_skip_verify" default:"false"`
}

var config = driver.Config{
	Name:        "WebDav",
	LocalSort:   true,
	OnlyProxy:   true,
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &WebDav{}
	})
}
