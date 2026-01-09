package terabox

import (
	"fmt"
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

type File struct {
	FsId        int64 `json:"fs_id"`
	ServerMtime int64 `json:"server_mtime"`
	Thumbs struct {
		Url3 string `json:"url3"`
	} `json:"thumbs"`
	Size           int64  `json:"size"`
	Path           string `json:"path"`
	ServerFilename string `json:"server_filename"`
	Isdir          int    `json:"isdir"`
}

type ListResp struct {
	Errno    int    `json:"errno"`
	GuidInfo string `json:"guid_info"`
	List     []File `json:"list"`
	Guid     int    `json:"guid"`
}

func fileToObj(f File) *model.ObjThumb {
	return &model.ObjThumb{
		Object: model.Object{
			ID:       strconv.FormatInt(f.FsId, 10),
			Name:     f.ServerFilename,
			Size:     f.Size,
			Modified: time.Unix(f.ServerMtime, 0),
			IsFolder: f.Isdir == 1,
		},
		Thumbnail: model.Thumbnail{Thumbnail: f.Thumbs.Url3},
	}
}

type DownloadResp struct {
	Errno int `json:"errno"`
	Dlink []struct {
		Dlink string `json:"dlink"`
	} `json:"dlink"`
}

type DownloadResp2 struct {
	Errno int `json:"errno"`
	Info  []struct {
		Dlink string `json:"dlink"`
	} `json:"info"`
}

type HomeInfoResp struct {
	Errno int `json:"errno"`
	Data  struct {
		Sign1     string `json:"sign1"`
		Sign3     string `json:"sign3"`
		Timestamp int    `json:"timestamp"`
	} `json:"data"`
}

type PrecreateResp struct {
	Path       string `json:"path"`
	Uploadid   string `json:"uploadid"`
	ReturnType int    `json:"return_type"`
	BlockList  []int  `json:"block_list"`
	Errno      int    `json:"errno"`
}

type CheckLoginResp struct {
	Errno int `json:"errno"`
}

type LocateUploadResp struct {
	Host string `json:"host"`
}

type CreateResp struct {
	Errno int `json:"errno"`
}

// ChunkInfo represents information about a chunk to be uploaded
type ChunkInfo struct {
	Index  int   // Chunk index (0-based)
	Offset int64 // Byte offset in the file
	Size   int64 // Size of this chunk in bytes
}

// ManageResp represents the response from file management operations
type ManageResp struct {
	Errno  int `json:"errno"`
	Info   []struct {
		Errno int    `json:"errno"`
		Path  string `json:"path"`
	} `json:"info"`
	TaskId    int64  `json:"task_id"`
	RequestId string `json:"request_id"`
}

// ErrorResp represents a standard error response from the API
type ErrorResp struct {
	Errno     int    `json:"errno"`
	ErrMsg    string `json:"errmsg"`
	RequestId string `json:"request_id"`
}

// JsTokenResp represents the response containing jsToken information
type JsTokenResp struct {
	Errno int    `json:"errno"`
	Token string `json:"token"`
}

// QuotaResp represents the response for quota/space information
type QuotaResp struct {
	Errno      int    `json:"errno"`
	Errmsg     string `json:"errmsg"`
	Total      int64  `json:"total"`
	Used       int64  `json:"used"`
	Free       int64  `json:"free"`
	Expire     bool   `json:"expire"`
	SboxUsed   int64  `json:"sbox_used"`
	ServerTime int64  `json:"server_time"`
}

// UserInfoResp represents user information response
type UserInfoResp struct {
	Errno int `json:"errno"`
	Data  struct {
		Username string `json:"username"`
		Avatar   string `json:"avatar"`
		VipType  int    `json:"vip_type"`
	} `json:"data"`
}

// formatBytes formatea bytes en formato legible (B, KiB, MiB, GiB, etc.)
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
