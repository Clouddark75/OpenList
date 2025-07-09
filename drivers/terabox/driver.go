package terabox

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	stdpath "path"
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	log "github.com/sirupsen/logrus"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

const (
	minChunkSize    int64 = 4 << 20   // 4MB minimum chunk size
	maxChunkSize    int64 = 128 << 20 // 128MB maximum chunk size
	maxUploadRetries      = 3
	retryDelay           = 5 * time.Second
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
	// 1. Get upload server
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

	// 2. Pre-create file
	rawPath := stdpath.Join(dstDir.GetPath(), stream.GetName())
	chunkSize := calculateOptimalChunkSize(stream.GetSize())

	var precreateBlockListStr string
	if stream.GetSize() > minChunkSize {
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
	_, err = d.post_form("/api/precreate", nil, data, &precreateResp)
	if err != nil {
		return err
	}
	if precreateResp.Errno != 0 {
		return fmt.Errorf("precreate failed with errno: %d", precreateResp.Errno)
	}
	if precreateResp.ReturnType == 2 {
		return nil
	}

	// 3. Upload chunks with worker pool
	uploadedMD5s, err := d.uploadChunksWithWorkers(
		ctx,
		"https://"+locateupload_resp.Host+"/rest/2.0/pcs/superfile2",
		stream,
		precreateResp.Uploadid,
		chunkSize,
		up,
	)
	if err != nil {
		return err
	}

	// 4. Finalize upload
	uploadBlockListStr, err := utils.Json.MarshalToString(uploadedMD5s)
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
	_, err = d.post_form("/api/create", map[string]string{"rtype": "1"}, data, &createResp)
	if err != nil {
		return err
	}
	if createResp.Errno != 0 {
		return fmt.Errorf("finalize failed with errno: %d", createResp.Errno)
	}

	return nil
}

func (d *Terabox) uploadChunksWithWorkers(ctx context.Context, server string, stream model.FileStreamer, uploadID string, chunkSize int64, up driver.UpdateProgress) ([]string, error) {
	tempFile, err := stream.CacheFullInTempFile()
	if err != nil {
		return nil, err
	}
	defer tempFile.Close()

	fileSize := stream.GetSize()
	chunkCount := int(math.Ceil(float64(fileSize) / float64(chunkSize)))
	md5s := make([]string, chunkCount)
	completed := 0

	type chunkJob struct {
		index int
		start int64
		size  int64
	}

	results := make(chan struct {
		index int
		md5   string
		err   error
	}, chunkCount)

	jobs := make(chan chunkJob, chunkCount)

	// Worker function
	worker := func() {
		buf := make([]byte, chunkSize)
		h := md5.New()
		for job := range jobs {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := tempFile.ReadAt(buf[:job.size], job.start)
				if err != nil {
					results <- struct {
						index int
						md5   string
						err   error
					}{job.index, "", err}
					continue
				}

				h.Reset()
				h.Write(buf[:n])
				chunkMD5 := hex.EncodeToString(h.Sum(nil))

				var uploadErr error
				for retry := 0; retry < maxUploadRetries; retry++ {
					params := map[string]string{
						"method":   "upload",
						"partseq":  strconv.Itoa(job.index),
						"uploadid": uploadID,
					}

					res, err := base.RestyClient.R().
						SetContext(ctx).
						SetQueryParams(params).
						SetFileReader("file", stream.GetName(), bytes.NewReader(buf[:n])).
						Post(server)

					if err == nil && res.StatusCode() == 200 {
						results <- struct {
							index int
							md5   string
							err   error
						}{job.index, chunkMD5, nil}
						break
					}
					uploadErr = err
					time.Sleep(retryDelay)
				}

				if uploadErr != nil {
					results <- struct {
						index int
						md5   string
						err   error
					}{job.index, "", uploadErr}
				}
			}
		}
	}

	// Start workers
	for w := 0; w < d.UploadThreads; w++ {
		go worker()
	}

	// Distribute jobs
	go func() {
		defer close(jobs)
		for i := 0; i < chunkCount; i++ {
			start := int64(i) * chunkSize
			size := min(chunkSize, fileSize-start)
			jobs <- chunkJob{
				index: i,
				start: start,
				size:  size,
			}
		}
	}()

	// Collect results
	for i := 0; i < chunkCount; i++ {
		result := <-results
		if result.err != nil {
			return nil, fmt.Errorf("chunk %d upload failed: %w", result.index, result.err)
		}
		md5s[result.index] = result.md5
		completed++
		up(float64(completed) / float64(chunkCount) * 100)
	}

	return md5s, nil
}

func calculateOptimalChunkSize(fileSize int64) int64 {
	chunkSize := minChunkSize
	for fileSize > chunkSize*10 && chunkSize < maxChunkSize {
		chunkSize *= 2
	}
	return min(chunkSize, maxChunkSize)
}

var _ driver.Driver = (*Terabox)(nil)
