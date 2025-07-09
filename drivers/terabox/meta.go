package terabox

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	driver.RootPath
	Cookie string `json:"cookie" required:"true"`
	//JsToken        string `json:"js_token" type:"string" required:"true"`
	UserAgent      string `json:"user_agent" required:"true" default:"terabox;1.40.0.132;PC;PC-Windows;10.0.26100;Windows TeraBox"`
	DownloadAPI    string `json:"download_api" type:"select" options:"official,crack" default:"official"`
	OrderBy        string `json:"order_by" type:"select" options:"name,time,size" default:"name"`
	OrderDirection string `json:"order_direction" type:"select" options:"asc,desc" default:"asc"`
	UploadThreads  int    `json:"upload_threads" default:"2"` // New setting
}

var config = driver.Config{
	Name:        "Terabox",
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Terabox{}
	})
}
