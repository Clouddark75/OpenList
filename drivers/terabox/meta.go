package terabox

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	driver.RootPath
	Cookie string `json:"cookie" required:"true" help:"Terabox session cookie (get from browser developer tools)"`
	//JsToken        string `json:"js_token" type:"string" required:"true"`
	UserAgent      string `json:"user_agent" required:"true" default:"terabox;1.40.0.132;PC;PC-Windows;10.0.26100;WindowsTeraBox" help:"User agent string for API requests"`
	DownloadAPI    string `json:"download_api" type:"select" options:"official,crack" default:"official" help:"API method for downloading files"`
	OrderBy        string `json:"order_by" type:"select" options:"name,time,size" default:"name" help:"Default sorting field for file listings"`
	OrderDirection string `json:"order_direction" type:"select" options:"asc,desc" default:"asc" help:"Default sorting direction"`
	UploadThreads  int    `json:"upload_threads" type:"number" default:"2" help:"Number of parallel threads for uploads (1-10, default: 2)"`
	RetryCount     int    `json:"retry_count" type:"number" default:"5" help:"Number of retry attempts for failed operations (1-10, default: 3)"`
}

var config = driver.Config{
	Name:              "Terabox",
	DefaultRoot:       "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Terabox{}
	})
}
