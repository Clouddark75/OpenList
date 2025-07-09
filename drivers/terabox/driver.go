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

const (
	defaultChunkSize     = 10 << 20 // 10MB
	maxChunkSize        = 20 << 20 // 20MB
	chunkThreshold      = 20 << 20 // 20MB
	maxRetries          = 3
	retryDelay          = 2 * time.Second
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
	if stream.GetSize() > chunkThreshold {
		return d.chunkedUpload(ctx, dstDir, stream, up)
	}
	return d.singleUpload(ctx, dstDir, stream, up)
}

func (d *Terabox) singleUpload(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
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

	// Create upload session
	session, err := d.createUploadSession(ctx, dstDir, stream)
	if err != nil {
		return fmt.Errorf("session creation failed: %w", err)
	}

	// Upload chunks
	if err := d.uploadAllChunks(ctx, stream, session, up); err != nil {
		return fmt.Errorf("chunk upload failed: %w", err)
	}

	// Finalize upload
	return d.finalizeUpload(ctx, stream, session)
}

func (d *Terabox) createUploadSession(ctx context.Context, dstDir model.Obj, stream model.FileStreamer) (*UploadSession, error) {
	params := map[string]string{
		"path":        stdpath.Join(dstDir.GetPath(), stream.GetName()),
		"size":        strconv.FormatInt(stream.GetSize(), 10),
		"isdir":       "0",
		"autoinit":    "1",
		"block_list":  "[]",
		"rtype":       "1",
	}

	var session UploadSession
	_, err := d.retryRequest(func() ([]byte, error) {
		return d.post_form("/api/precreate", nil, params, &session)
	})
	if err != nil {
		return nil, err
	}

	if session.Errno != 0 {
		return nil, fmt.Errorf("api error: errno %d", session.Errno)
	}

	return &session, nil
}

func (d *Terabox) uploadAllChunks(ctx context.Context, stream model.FileStreamer, session *UploadSession, up driver.UpdateProgress) error {
	chunkSize := d.calculateChunkSize(stream.GetSize())
	totalChunks := int(math.Ceil(float64(stream.GetSize()) / float64(chunkSize)))
	md5Hasher := md5.New()

	tempFile, err := os.CreateTemp("", "terabox-upload-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, stream); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	for seq := 0; seq < totalChunks; seq++ {
		if utils.IsCanceled(ctx) {
			return ctx.Err()
		}

		offset := int64(seq) * chunkSize
		chunkData := make([]byte, chunkSize)
		n, err := tempFile.ReadAt(chunkData, offset)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read chunk: %w", err)
		}
		chunkData = chunkData[:n]

		md5Hasher.Write(chunkData)
		md5Sum := hex.EncodeToString(md5Hasher.Sum(nil))
		md5Hasher.Reset()

		err = d.retryRequest(func() error {
			return d.uploadSingleChunk(ctx, session.UploadID, seq, md5Sum, chunkData)
		})
		if err != nil {
			return fmt.Errorf("failed to upload chunk %d: %w", seq, err)
		}

		if up != nil {
			progress := float64(seq+1) / float64(totalChunks) * 100
			up(progress)
		}
	}

	return nil
}

func (d *Terabox) uploadSingleChunk(ctx context.Context, uploadID string, seq int, md5Sum string, chunk []byte) error {
	params := map[string]string{
		"method":     "upload",
		"uploadid":   uploadID,
		"partseq":    strconv.Itoa(seq),
		"app_id":     "250528",
		"channel":    "dubox",
		"clienttype": "0",
	}

	_, err := base.RestyClient.R().
		SetContext(ctx).
		SetQueryParams(params).
		SetFileReader("file", md5Sum, bytes.NewReader(chunk)).
		SetHeader("Cookie", d.Cookie).
		Post("https://" + d.url_domain_prefix + "-data.terabox.com/rest/2.0/pcs/superfile2")

	return err
}

func (d *Terabox) finalizeUpload(ctx context.Context, stream model.FileStreamer, session *UploadSession) error {
	params := map[string]string{
		"path":        session.Path,
		"size":        strconv.FormatInt(stream.GetSize(), 10),
		"uploadid":    session.UploadID,
		"block_list":  "[]",
		"isdir":       "0",
		"rtype":       "1",
	}

	var result CreateResp
	_, err := d.retryRequest(func() ([]byte, error) {
		return d.post_form("/api/create", nil, params, &result)
	})
	if err != nil {
		return err
	}

	if result.Errno != 0 {
		return fmt.Errorf("finalization failed: errno %d", result.Errno)
	}

	return nil
}

func (d *Terabox) retryRequest(fn func() ([]byte, error)) ([]byte, error) {
	var err error
	var res []byte

	for i := 0; i < maxRetries; i++ {
		res, err = fn()
		if err == nil {
			return res, nil
		}
		time.Sleep(retryDelay)
	}

	return nil, err
}

func (d *Terabox) calculateChunkSize(fileSize int64) int64 {
	chunkSize := defaultChunkSize

	if fileSize > 1<<30 { // 1GB
		chunkSize = maxChunkSize
	}

	if chunkSize > fileSize {
		return fileSize
	}

	return chunkSize
}

func encodeURIComponent(str string) string {
	r := url.QueryEscape(str)
	return strings.ReplaceAll(r, "+", "%20")
}

var _ driver.Driver = (*Terabox)(nil)
