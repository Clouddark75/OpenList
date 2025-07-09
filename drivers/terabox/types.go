package terabox

import (
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

type File struct {
	FsId         int64  `json:"fs_id"`
	ServerMtime  int64  `json:"server_mtime"`
	Thumbs       struct {
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

type ChunkUploadStatus struct {
	UploadID      string `json:"upload_id"`
	ChunkSize     int64  `json:"chunk_size"`
	TotalChunks   int    `json:"total_chunks"`
	Completed     int    `json:"completed"`
	LastChunkMD5  string `json:"last_chunk_md5"`
	LastError     string `json:"last_error,omitempty"`
}

type VerifyChunkResponse struct {
	Errno    int    `json:"errno"`
	Verified bool   `json:"verified"`
	Message  string `json:"message,omitempty"`
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
