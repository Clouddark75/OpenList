package terabox

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	stdpath "path"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	log "github.com/sirupsen/logrus"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

type Terabox struct {
	model.Storage
	Addition
	JsToken           string
	url_domain_prefix string
	base_url          string
}

func (d *Terabox) Config() driver.Config {
	return config
}

func (d *Terabox) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Terabox) Init(ctx context.Context) error {
	var resp CheckLoginResp
	d.base_url = "https://www.terabox.com"
	d.url_domain_prefix = "jp"
	_, err := d.get("/api/check/login", nil, &resp)
	if err != nil {
		return err
	}
	if resp.Errno != 0 {
		if resp.Errno == 9000 {
			return fmt.Errorf("terabox is not yet available in this area")
		}
		return fmt.Errorf("failed to check login status according to cookie")
	}
	return err
}

func (d *Terabox) Drop(ctx context.Context) error {
	return nil
}

func (d *Terabox) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	files, err := d.getFiles(dir.GetPath())
	if err != nil {
		return nil, err
	}
	return utils.SliceConvert(files, func(src File) (model.Obj, error) {
		return fileToObj(src), nil
	})
}

func (d *Terabox) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	if d.DownloadAPI == "crack" {
		return d.linkCrack(file, args)
	}
	return d.linkOfficial(file, args)
}

func (d *Terabox) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	params := map[string]string{
		"a": "commit",
	}
	data := map[string]string{
		"path":       stdpath.Join(parentDir.GetPath(), dirName),
		"isdir":      "1",
		"block_list": "[]",
	}
	res, err := d.post_form("/api/create", params, data, nil)
	log.Debugln(string(res))
	return err
}

func (d *Terabox) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	data := []base.Json{
		{
			"path":    srcObj.GetPath(),
			"dest":    dstDir.GetPath(),
			"newname": srcObj.GetName(),
		},
	}
	_, err := d.manage("move", data)
	return err
}

func (d *Terabox) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	data := []base.Json{
		{
			"path":    srcObj.GetPath(),
			"newname": newName,
		},
	}
	_, err := d.manage("rename", data)
	return err
}

func (d *Terabox) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	data := []base.Json{
		{
			"path":    srcObj.GetPath(),
			"dest":    dstDir.GetPath(),
			"newname": srcObj.GetName(),
		},
	}
	_, err := d.manage("copy", data)
	return err
}

func (d *Terabox) Remove(ctx context.Context, obj model.Obj) error {
	data := []string{obj.GetPath()}
	_, err := d.manage("delete", data)
	return err
}

func (d *Terabox) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	// Automatic switching to chunked upload for files >20MB
	if stream.GetSize() > 20<<20 {
		return d.chunkedUpload(ctx, dstDir, stream, up)
	}

	// Original single file upload implementation
	resp, err := base.RestyClient.R().
		SetContext(ctx).
		Get("https://" + d.url_domain_prefix + "-data.terabox.com/rest/2.0/pcs/file?method=locateupload")
	if err != nil {
		return err
	}
	var locateupload_resp LocateUploadResp
	err = utils.Json.Unmarshal(resp.Body(), &locateupload_resp)
	if err != nil {
		log.Debugln(resp)
		return err
	}
	log.Debugln(locateupload_resp)

	rawPath := stdpath.Join(dstDir.GetPath(), stream.GetName())
	path := encodeURIComponent(rawPath)
	streamSize := stream.GetSize()

	var precreateBlockListStr string
	if stream.GetSize() > initialChunkSize {
		precreateBlockListStr = `["5910a591dd8fc18c32a8f3df4fdc1761","a5fc157d78e6ad1c7e114b056c92821e"]`
	} else {
		precreateBlockListStr = `["5910a591dd8fc18c32a8f3df4fdc1761"]`
	}

	data := map[string]string{
		"path":                  rawPath,
		"autoinit":              "1",
		"target_path":           dstDir.GetPath(),
		"block_list":            precreateBlockListStr,
		"size":                  strconv.FormatInt(stream.GetSize(), 10),
		"local_mtime":           strconv.FormatInt(stream.ModTime().Unix(), 10),
		"file_limit_switch_v34": "true",
	}
	var precreateResp PrecreateResp
	log.Debugln(data)
	res, err := d.post_form("/api/precreate", nil, data, &precreateResp)
	if err != nil {
		return err
	}
	log.Debugf("%+v", precreateResp)
	if precreateResp.Errno != 0 {
		log.Debugln(string(res))
		return fmt.Errorf("[terabox] failed to precreate file, errno: %d", precreateResp.Errno)
	}
	if precreateResp.ReturnType == 2 {
		return nil
	}

	tempFile, err := stream.CacheFullInTempFile()
	if err != nil {
		return err
	}

	params := map[string]string{
		"method":     "upload",
		"path":       path,
		"uploadid":   precreateResp.Uploadid,
		"app_id":     "250528",
		"web":        "1",
		"channel":    "dubox",
		"clienttype": "0",
		"uploadsign": "0",
	}

	chunkSize := calculateChunkSize(streamSize)
	chunkByteData := make([]byte, chunkSize)
	count := int(math.Ceil(float64(streamSize) / float64(chunkSize)))
	left := streamSize
	uploadBlockList := make([]string, 0, count)
	h := md5.New()
	for partseq := 0; partseq < count; partseq++ {
		if utils.IsCanceled(ctx) {
			return ctx.Err()
		}
		byteSize := chunkSize
		var byteData []byte
		if left >= chunkSize {
			byteData = chunkByteData
		} else {
			byteSize = left
			byteData = make([]byte, byteSize)
		}
		left -= byteSize
		_, err = io.ReadFull(tempFile, byteData)
		if err != nil {
			return err
		}

		h.Write(byteData)
		uploadBlockList = append(uploadBlockList, hex.EncodeToString(h.Sum(nil)))
		h.Reset()

		u := "https://" + locateupload_resp.Host + "/rest/2.0/pcs/superfile2"
		params["partseq"] = strconv.Itoa(partseq)
		res, err := base.RestyClient.R().
			SetContext(ctx).
			SetQueryParams(params).
			SetFileReader("file", stream.GetName(), bytes.NewReader(byteData)).
			SetHeader("Cookie", d.Cookie).
			Post(u)
		if err != nil {
			return err
		}
		log.Debugln(res.String())
		if count > 0 {
			up(float64(partseq) * 100 / float64(count))
		}
	}

	params = map[string]string{
		"isdir": "0",
		"rtype": "1",
	}

	uploadBlockListStr, err := utils.Json.MarshalToString(uploadBlockList)
	if err != nil {
		return err
	}
	data = map[string]string{
		"path":        rawPath,
		"size":        strconv.FormatInt(stream.GetSize(), 10),
		"uploadid":    precreateResp.Uploadid,
		"target_path": dstDir.GetPath(),
		"block_list":  uploadBlockListStr,
		"local_mtime": strconv.FormatInt(stream.ModTime().Unix(), 10),
	}
	var createResp CreateResp
	res, err = d.post_form("/api/create", params, data, &createResp)
	log.Debugln(string(res))
	if err != nil {
		return err
	}
	if createResp.Errno != 0 {
		return fmt.Errorf("[terabox] failed to create file, errno: %d", createResp.Errno)
	}
	time.Sleep(time.Duration(len(precreateResp.BlockList)/16+5) * time.Second)
	return nil
}

func (d *Terabox) chunkedUpload(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	log.WithFields(log.Fields{
		"size": stream.GetSize(),
		"name": stream.GetName(),
	}).Debug("Starting optimized chunked upload")

	// Create temp file
	tempFile, err := os.CreateTemp("", "terabox-upload-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Write stream to temp file
	if _, err := io.Copy(tempFile, stream); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}
	if _, err := tempFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek temp file: %w", err)
	}

	// Initialize upload session
	resp, err := base.RestyClient.R().
		SetContext(ctx).
		Get("https://" + d.url_domain_prefix + "-data.terabox.com/rest/2.0/pcs/file?method=locateupload")
	if err != nil {
		return err
	}

	var locateResp LocateUploadResp
	if err = utils.Json.Unmarshal(resp.Body(), &locateResp); err != nil {
		return err
	}

	rawPath := stdpath.Join(dstDir.GetPath(), stream.GetName())
	path := encodeURIComponent(rawPath)

	precreateData := map[string]string{
		"path":                  rawPath,
		"autoinit":              "1",
		"target_path":           dstDir.GetPath(),
		"block_list":            `["5910a591dd8fc18c32a8f3df4fdc1761"]`,
		"size":                  strconv.FormatInt(stream.GetSize(), 10),
		"local_mtime":           strconv.FormatInt(stream.ModTime().Unix(), 10),
		"file_limit_switch_v34": "true",
	}

	var precreateResp PrecreateResp
	if _, err = d.post_form("/api/precreate", nil, precreateData, &precreateResp); err != nil {
		return err
	}

	if precreateResp.Errno != 0 {
		return fmt.Errorf("precreate failed: errno %d", precreateResp.Errno)
	}

	// Upload chunks
	chunkSize := calculateChunkSize(stream.GetSize())
	totalChunks := int(math.Ceil(float64(stream.GetSize()) / float64(chunkSize)))
	uploadBlockList := make([]string, 0, totalChunks)
	h := md5.New()

	for partseq := 0; partseq < totalChunks; partseq++ {
		if utils.IsCanceled(ctx) {
			return ctx.Err()
		}

		offset := int64(partseq) * chunkSize
		chunkData := make([]byte, chunkSize)
		n, err := tempFile.ReadAt(chunkData, offset)
		if err != nil && err != io.EOF {
			return err
		}
		chunkData = chunkData[:n]

		h.Write(chunkData)
		uploadBlockList = append(uploadBlockList, hex.EncodeToString(h.Sum(nil)))
		h.Reset()

		params := map[string]string{
			"method":     "upload",
			"path":       path,
			"uploadid":   precreateResp.Uploadid,
			"app_id":     "250528",
			"web":        "1",
			"channel":    "dubox",
			"clienttype": "0",
			"uploadsign": "0",
			"partseq":    strconv.Itoa(partseq),
		}

		_, err = base.RestyClient.R().
			SetContext(ctx).
			SetQueryParams(params).
			SetFileReader("file", stream.GetName(), bytes.NewReader(chunkData)).
			SetHeader("Cookie", d.Cookie).
			Post("https://" + locateResp.Host + "/rest/2.0/pcs/superfile2")
		if err != nil {
			return err
		}

		up(float64(partseq+1) * 100 / float64(totalChunks))
	}

	// Finalize upload
	blockListStr, err := utils.Json.MarshalToString(uploadBlockList)
	if err != nil {
		return err
	}

	createData := map[string]string{
		"path":        rawPath,
		"size":        strconv.FormatInt(stream.GetSize(), 10),
		"uploadid":    precreateResp.Uploadid,
		"target_path": dstDir.GetPath(),
		"block_list":  blockListStr,
		"local_mtime": strconv.FormatInt(stream.ModTime().Unix(), 10),
	}

	var createResp CreateResp
	if _, err = d.post_form("/api/create", map[string]string{"isdir": "0", "rtype": "1"}, createData, &createResp); err != nil {
		return err
	}

	if createResp.Errno != 0 {
		return fmt.Errorf("create failed: errno %d", createResp.Errno)
	}

	return nil
}

func encodeURIComponent(str string) string {
	r := url.QueryEscape(str)
	r = strings.ReplaceAll(r, "+", "%20")
	return r
}

// Fix the calculateChunkSize function
func calculateChunkSize(streamSize int64) int64 {
    const (
        initialChunkSize     = 4 << 20 // 4MB (implicitly int64)
        initialSizeThreshold = 4 << 30 // 4GB (implicitly int64)
    )
    
    chunkSize := initialChunkSize
    sizeThreshold := initialSizeThreshold

    if streamSize < chunkSize {
        return streamSize
    }

    for streamSize > sizeThreshold {
        chunkSize <<= 1
        sizeThreshold <<= 1
    }

    return chunkSize
}

var _ driver.Driver = (*Terabox)(nil)
