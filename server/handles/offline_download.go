package handles

import (
	"strings"

	_115 "github.com/OpenListTeam/OpenList/v4/drivers/115"
	_115_open "github.com/OpenListTeam/OpenList/v4/drivers/115_open"
	_123 "github.com/OpenListTeam/OpenList/v4/drivers/123"
	_123_open "github.com/OpenListTeam/OpenList/v4/drivers/123_open"
	"github.com/OpenListTeam/OpenList/v4/drivers/pikpak"
	"github.com/OpenListTeam/OpenList/v4/drivers/thunder"
	"github.com/OpenListTeam/OpenList/v4/drivers/thunder_browser"
	"github.com/OpenListTeam/OpenList/v4/drivers/thunderx"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

type SetAria2Req struct {
	Uri    string `json:"uri" form:"uri"`
	Secret string `json:"secret" form:"secret"`
}

func SetAria2(c *gin.Context) {
	var req SetAria2Req
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	items := []model.SettingItem{
		{Key: conf.Aria2Uri, Value: req.Uri, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
		{Key: conf.Aria2Secret, Value: req.Secret, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_tool, err := tool.Tools.Get("aria2")
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	version, err := _tool.Init()
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, version)
}

type SetQbittorrentReq struct {
	Url      string `json:"url" form:"url"`
	Seedtime string `json:"seedtime" form:"seedtime"`
}

func SetQbittorrent(c *gin.Context) {
	var req SetQbittorrentReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	items := []model.SettingItem{
		{Key: conf.QbittorrentUrl, Value: req.Url, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
		{Key: conf.QbittorrentSeedtime, Value: req.Seedtime, Type: conf.TypeNumber, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_tool, err := tool.Tools.Get("qBittorrent")
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if _, err := _tool.Init(); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, "ok")
}

type SetTransmissionReq struct {
	Uri      string `json:"uri" form:"uri"`
	Seedtime string `json:"seedtime" form:"seedtime"`
}

func SetTransmission(c *gin.Context) {
	var req SetTransmissionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	items := []model.SettingItem{
		{Key: conf.TransmissionUri, Value: req.Uri, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
		{Key: conf.TransmissionSeedtime, Value: req.Seedtime, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_tool, err := tool.Tools.Get("Transmission")
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if _, err := _tool.Init(); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, "ok")
}

type Set115Req struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func Set115(c *gin.Context) {
	var req Set115Req
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if req.TempDir != "" {
		storage, _, err := op.GetStorageAndActualPath(req.TempDir)
		if err != nil {
			common.ErrorStrResp(c, "storage does not exists", 400)
			return
		}
		if storage.Config().CheckStatus && storage.GetStorage().Status != op.WORK {
			common.ErrorStrResp(c, "storage not init: "+storage.GetStorage().Status, 400)
			return
		}
		if _, ok := storage.(*_115.Pan115); !ok {
			common.ErrorStrResp(c, "unsupported storage driver for offline download, only 115 Cloud is supported", 400)
			return
		}
	}
	items := []model.SettingItem{
		{Key: conf.Pan115TempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_tool, err := tool.Tools.Get("115 Cloud")
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if _, err := _tool.Init(); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, "ok")
}

type Set115OpenReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func Set115Open(c *gin.Context) {
	var req Set115OpenReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if req.TempDir != "" {
		storage, _, err := op.GetStorageAndActualPath(req.TempDir)
		if err != nil {
			common.ErrorStrResp(c, "storage does not exists", 400)
			return
		}
		if storage.Config().CheckStatus && storage.GetStorage().Status != op.WORK {
			common.ErrorStrResp(c, "storage not init: "+storage.GetStorage().Status, 400)
			return
		}
		if _, ok := storage.(*_115_open.Open115); !ok {
			common.ErrorStrResp(c, "unsupported storage driver for offline download, only 115 Open is supported", 400)
			return
		}
	}
	items := []model.SettingItem{
		{Key: conf.Pan115OpenTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_tool, err := tool.Tools.Get("115 Open")
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if _, err := _tool.Init(); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, "ok")
}

type Set123PanReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func Set123Pan(c *gin.Context) {
	var req Set123PanReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if req.TempDir != "" {
		storage, _, err := op.GetStorageAndActualPath(req.TempDir)
		if err != nil {
			common.ErrorStrResp(c, "storage does not exists", 400)
			return
		}
		if storage.Config().CheckStatus && storage.GetStorage().Status != op.WORK {
			common.ErrorStrResp(c, "storage not init: "+storage.GetStorage().Status, 400)
			return
		}
		if _, ok := storage.(*_123.Pan123); !ok {
			common.ErrorStrResp(c, "unsupported storage driver for offline download, only 123Pan is supported", 400)
			return
		}
	}
	items := []model.SettingItem{
		{Key: conf.Pan123TempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_tool, err := tool.Tools.Get("123Pan")
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if _, err := _tool.Init(); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, "ok")
}

type Set123OpenReq struct {
	TempDir     string `json:"temp_dir" form:"temp_dir"`
	CallbackUrl string `json:"callback_url" form:"callback_url"`
}

func Set123Open(c *gin.Context) {
	var req Set123OpenReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if req.TempDir != "" {
		storage, _, err := op.GetStorageAndActualPath(req.TempDir)
		if err != nil {
			common.ErrorStrResp(c, "storage does not exists", 400)
			return
		}
		if storage.Config().CheckStatus && storage.GetStorage().Status != op.WORK {
			common.ErrorStrResp(c, "storage not init: "+storage.GetStorage().Status, 400)
			return
		}
		if _, ok := storage.(*_123_open.Open123); !ok {
			common.ErrorStrResp(c, "unsupported storage driver for offline download, only 123 Open is supported", 400)
			return
		}
	}
	items := []model.SettingItem{
		{Key: conf.Pan123OpenTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
		{Key: conf.Pan123OpenOfflineDownloadCallbackUrl, Value: req.CallbackUrl, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_tool, err := tool.Tools.Get("123 Open")
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if _, err := _tool.Init(); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, "ok")
}

type SetPikPakReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func SetPikPak(c *gin.Context) {
	var req SetPikPakReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if req.TempDir != "" {
		storage, _, err := op.GetStorageAndActualPath(req.TempDir)
		if err != nil {
			common.ErrorStrResp(c, "storage does not exists", 400)
			return
		}
		if storage.Config().CheckStatus && storage.GetStorage().Status != op.WORK {
			common.ErrorStrResp(c, "storage not init: "+storage.GetStorage().Status, 400)
			return
		}
		if _, ok := storage.(*pikpak.PikPak); !ok {
			common.ErrorStrResp(c, "unsupported storage driver for offline download, only PikPak is supported", 400)
			return
		}
	}
	items := []model.SettingItem{
		{Key: conf.PikPakTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_tool, err := tool.Tools.Get("PikPak")
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if _, err := _tool.Init(); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, "ok")
}

type SetThunderReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func SetThunder(c *gin.Context) {
	var req SetThunderReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if req.TempDir != "" {
		storage, _, err := op.GetStorageAndActualPath(req.TempDir)
		if err != nil {
			common.ErrorStrResp(c, "storage does not exists", 400)
			return
		}
		if storage.Config().CheckStatus && storage.GetStorage().Status != op.WORK {
			common.ErrorStrResp(c, "storage not init: "+storage.GetStorage().Status, 400)
			return
		}
		if _, ok := storage.(*thunder.Thunder); !ok {
			common.ErrorStrResp(c, "unsupported storage driver for offline download, only Thunder is supported", 400)
			return
		}
	}
	items := []model.SettingItem{
		{Key: conf.ThunderTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_tool, err := tool.Tools.Get("Thunder")
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if _, err := _tool.Init(); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, "ok")
}

type SetThunderXReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func SetThunderX(c *gin.Context) {
	var req SetThunderXReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if req.TempDir != "" {
		storage, _, err := op.GetStorageAndActualPath(req.TempDir)
		if err != nil {
			common.ErrorStrResp(c, "storage does not exists", 400)
			return
		}
		if storage.Config().CheckStatus && storage.GetStorage().Status != op.WORK {
			common.ErrorStrResp(c, "storage not init: "+storage.GetStorage().Status, 400)
			return
		}
		if _, ok := storage.(*thunderx.ThunderX); !ok {
			common.ErrorStrResp(c, "unsupported storage driver for offline download, only ThunderX is supported", 400)
			return
		}
	}
	items := []model.SettingItem{
		{Key: conf.ThunderXTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_tool, err := tool.Tools.Get("ThunderX")
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if _, err := _tool.Init(); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, "ok")
}

type SetThunderBrowserReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func SetThunderBrowser(c *gin.Context) {
	var req SetThunderBrowserReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if req.TempDir != "" {
		storage, _, err := op.GetStorageAndActualPath(req.TempDir)
		if err != nil {
			common.ErrorStrResp(c, "storage does not exists", 400)
			return
		}
		if storage.Config().CheckStatus && storage.GetStorage().Status != op.WORK {
			common.ErrorStrResp(c, "storage not init: "+storage.GetStorage().Status, 400)
			return
		}
		switch storage.(type) {
		case *thunder_browser.ThunderBrowser, *thunder_browser.ThunderBrowserExpert:
		default:
			common.ErrorStrResp(c, "unsupported storage driver for offline download, only ThunderBrowser is supported", 400)
			return
		}
	}
	items := []model.SettingItem{
		{Key: conf.ThunderBrowserTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_tool, err := tool.Tools.Get("ThunderBrowser")
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if _, err := _tool.Init(); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, "ok")
}

func OfflineDownloadTools(c *gin.Context) {
	tools := tool.Tools.Names()
	common.SuccessResp(c, tools)
}

type AddOfflineDownloadReq struct {
	Urls         []string `json:"urls"`
	Path         string   `json:"path"`
	Tool         string   `json:"tool"`
	DeletePolicy string   `json:"delete_policy"`
}

// splitURLs expands a slice of URL strings by splitting any concatenated
// HTTP/HTTPS URLs within a single entry into individual URLs.
// For example, "https://a.comhttps://b.com" becomes ["https://a.com", "https://b.com"].
// Non-HTTP schemes (magnet:, ed2k://, etc.) are returned as-is.
func splitURLs(urls []string) []string {
	var result []string
	for _, raw := range urls {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		// Only attempt to split entries that contain HTTP/HTTPS URLs.
		// Non-HTTP schemes (magnet, ed2k, ftp, etc.) won't contain
		// "http://" or "https://" mid-string, so they pass through unchanged.
		if !strings.Contains(raw, "http://") && !strings.Contains(raw, "https://") {
			result = append(result, raw)
			continue
		}
		// Split on "https://" first, then "http://", re-attaching the scheme.
		// Strategy: replace "https://" with a sentinel, split on "http://",
		// then restore "https://" — this handles interleaved http/https correctly.
		const sentinel = "\x00HTTPS\x00"
		normalized := strings.ReplaceAll(raw, "https://", sentinel)
		parts := strings.Split(normalized, "http://")
		for i, part := range parts {
			// Restore https:// sentinel back
			part = strings.ReplaceAll(part, sentinel, "https://")
			if i == 0 && part == "" {
				// The string started with "http://", skip the empty leading part
				continue
			}
			// Re-attach "http://" to all parts except the first non-empty one
			// (which either started with https:// already, or is a bare fragment
			// before the first http:// — both handled below).
			var url string
			if strings.HasPrefix(part, "https://") {
				url = part
			} else if i == 0 {
				// This part came before any "http://", meaning the original string
				// started with "https://" (already restored) — use as-is.
				url = part
			} else {
				url = "http://" + part
			}
			url = strings.TrimSpace(url)
			if url != "" {
				result = append(result, url)
			}
		}
	}
	return result
}

func AddOfflineDownload(c *gin.Context) {
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if !user.CanAddOfflineDownloadTasks() {
		common.ErrorStrResp(c, "permission denied", 403)
		return
	}

	var req AddOfflineDownloadReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	reqPath, err := user.JoinPath(req.Path)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	meta, err := op.GetNearestMeta(reqPath)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		common.ErrorResp(c, err, 500, true)
		return
	}
	if !common.CanWrite(user, meta, reqPath) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}

	// Expand any concatenated URLs within each entry
	expandedUrls := splitURLs(req.Urls)

	var tasks []task.TaskExtensionInfo
	for _, url := range expandedUrls {
		t, err := tool.AddURL(c, &tool.AddURLArgs{
			URL:          url,
			DstDirPath:   reqPath,
			Tool:         req.Tool,
			DeletePolicy: tool.DeletePolicy(req.DeletePolicy),
		})
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
		if t != nil {
			tasks = append(tasks, t)
		}
	}
	common.SuccessResp(c, gin.H{
		"tasks": getTaskInfos(tasks),
	})
}
