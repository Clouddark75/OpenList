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
	"sync"
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
	sizeThreshold   int64 = 4 << 30   // 4GB threshold for larger chunks
	maxUploadRetries      = 3
	retryDelay           = 5 * time.Second
	defaultUploadThreads  = 3         // Default number of upload threads
)

type Terabox struct {
	model.Storage
	Addition
	JsToken           string
	url_domain_prefix string
	base_url          string
}

// ChunkJob represents a chunk upload job
type ChunkJob struct {
	partseq     int
	data        []byte
	chunkMD5    string
	chunkSize   int64
}

// ChunkResult represents the result of a chunk upload
type ChunkResult struct {
	partseq int
	md5     string
	err     error
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
	// Get upload server
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

	// Precreate file
	rawPath := stdpath.Join(dstDir.GetPath(), stream.GetName())
	path := encodeURIComponent(rawPath)
	streamSize := stream.GetSize()

	// Calculate optimal chunk size
	chunkSize := calculateOptimalChunkSize(streamSize)
	chunkCount := int(math.Ceil(float64(streamSize) / float64(chunkSize)))

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

	// Upload chunks with threading
	tempFile, err := stream.CacheFullInTempFile()
	if err != nil {
		return err
	}

	// Determine number of upload threads
	uploadThreads := d.getUploadThreads()
	if uploadThreads > chunkCount {
		uploadThreads = chunkCount
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

	// Create jobs channel and results channel
	jobs := make(chan ChunkJob, chunkCount)
	results := make(chan ChunkResult, chunkCount)

	// Start worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < uploadThreads; i++ {
		wg.Add(1)
		go d.uploadWorker(ctx, &wg, jobs, results, params, locateupload_resp.Host, stream.GetName())
	}

	// Prepare chunks and send to jobs channel
	go func() {
		defer close(jobs)
		h := md5.New()
		buf := make([]byte, chunkSize)
		var readBytes int64

		for partseq := 0; partseq < chunkCount; partseq++ {
			if utils.IsCanceled(ctx) {
				return
			}

			currentChunkSize := chunkSize
			if remaining := streamSize - readBytes; remaining < chunkSize {
				currentChunkSize = remaining
			}

			n, err := io.ReadFull(tempFile, buf[:currentChunkSize])
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				log.Errorf("Failed to read chunk %d: %v", partseq, err)
				return
			}

			// Calculate MD5 for the chunk
			h.Reset()
			h.Write(buf[:n])
			chunkMD5 := hex.EncodeToString(h.Sum(nil))

			// Create a copy of the data for this chunk
			chunkData := make([]byte, n)
			copy(chunkData, buf[:n])

			jobs <- ChunkJob{
				partseq:   partseq,
				data:      chunkData,
				chunkMD5:  chunkMD5,
				chunkSize: int64(n),
			}

			readBytes += int64(n)
		}
	}()

	// Wait for all workers to finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results and track progress
	uploadBlockList := make([]string, chunkCount)
	var uploadedBytes int64
	var mu sync.Mutex

	for result := range results {
		if result.err != nil {
			return fmt.Errorf("failed to upload chunk %d: %v", result.partseq, result.err)
		}

		uploadBlockList[result.partseq] = result.md5
		
		mu.Lock()
		uploadedBytes += calculateChunkSize(result.partseq, chunkSize, streamSize)
		progress := float64(uploadedBytes) / float64(streamSize) * 100
		up(progress)
		mu.Unlock()
	}

	// Complete the upload
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

// uploadWorker handles uploading chunks concurrently
func (d *Terabox) uploadWorker(ctx context.Context, wg *sync.WaitGroup, jobs <-chan ChunkJob, results chan<- ChunkResult, baseParams map[string]string, host, fileName string) {
	defer wg.Done()

	for job := range jobs {
		if utils.IsCanceled(ctx) {
			results <- ChunkResult{partseq: job.partseq, err: ctx.Err()}
			return
		}

		// Create a copy of params for this worker
		params := make(map[string]string)
		for k, v := range baseParams {
			params[k] = v
		}
		params["partseq"] = strconv.Itoa(job.partseq)

		// Upload chunk with retries
		var uploadErr error
		for retry := 0; retry < maxUploadRetries; retry++ {
			res, err := base.RestyClient.R().
				SetContext(ctx).
				SetQueryParams(params).
				SetFileReader("file", fileName, bytes.NewReader(job.data)).
				SetHeader("Cookie", d.Cookie).
				Post("https://" + host + "/rest/2.0/pcs/superfile2")
			
			if err == nil && res.StatusCode() == 200 {
				results <- ChunkResult{partseq: job.partseq, md5: job.chunkMD5, err: nil}
				uploadErr = nil
				break
			}
			
			uploadErr = fmt.Errorf("chunk upload failed: %v", err)
			if retry < maxUploadRetries-1 {
				time.Sleep(retryDelay)
			}
		}

		if uploadErr != nil {
			results <- ChunkResult{partseq: job.partseq, err: uploadErr}
			return
		}
	}
}

// getUploadThreads returns the number of upload threads to use
func (d *Terabox) getUploadThreads() int {
	// Use configured upload threads if set, otherwise use default
	if d.UploadThreads > 0 {
		return d.UploadThreads
	}
	return defaultUploadThreads
}

// calculateChunkSize calculates the size of a specific chunk
func calculateChunkSize(partseq int, chunkSize, totalSize int64) int64 {
	start := int64(partseq) * chunkSize
	end := start + chunkSize
	if end > totalSize {
		end = totalSize
	}
	return end - start
}

func calculateOptimalChunkSize(fileSize int64) int64 {
	chunkSize := minChunkSize

	// Scale up chunk size for larger files
	for fileSize > chunkSize*10 && chunkSize < maxChunkSize {
		chunkSize *= 2
	}

	// Don't exceed max chunk size
	if chunkSize > maxChunkSize {
		chunkSize = maxChunkSize
	}

	return chunkSize
}

var _ driver.Driver = (*Terabox)(nil)
