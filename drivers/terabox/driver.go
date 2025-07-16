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
	
	// Initialize jsToken first
	err := d.resetJsToken()
	if err != nil {
		log.Warnf("Failed to get initial jsToken: %v", err)
		// Continue without jsToken, it will be refreshed as needed
	}
	
	_, err = d.get("/api/check/login", nil, &resp)
	if err != nil {
		return err
	}
	if resp.Errno != 0 {
		if resp.Errno == 9000 {
			return fmt.Errorf("terabox is not yet available in this area")
		}
		return fmt.Errorf("failed to check login status according to cookie")
	}
	return nil
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
	// Ensure jsToken is available for directory creation
	if err := d.ensureJsToken(); err != nil {
		return fmt.Errorf("failed to get jsToken for mkdir: %v", err)
	}
	
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
	// Ensure jsToken is available for move operation
	if err := d.ensureJsToken(); err != nil {
		return fmt.Errorf("failed to get jsToken for move: %v", err)
	}
	
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
	// Ensure jsToken is available for rename operation
	if err := d.ensureJsToken(); err != nil {
		return fmt.Errorf("failed to get jsToken for rename: %v", err)
	}
	
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
	// Ensure jsToken is available for copy operation
	if err := d.ensureJsToken(); err != nil {
		return fmt.Errorf("failed to get jsToken for copy: %v", err)
	}
	
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
	// Ensure jsToken is available for delete operation
	if err := d.ensureJsToken(); err != nil {
		return fmt.Errorf("failed to get jsToken for remove: %v", err)
	}
	
	data := []string{obj.GetPath()}
	_, err := d.manage("delete", data)
	return err
}

func (d *Terabox) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	// Ensure jsToken is available before upload
	if err := d.ensureJsToken(); err != nil {
		return fmt.Errorf("failed to get jsToken for upload: %v", err)
	}
	
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

	// precreate file
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

	// upload chunks with threading
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
	count := int(math.Ceil(float64(streamSize) / float64(chunkSize)))
	
	// Get upload threads setting with default value of 2
	uploadThreads := d.UploadThreads
	if uploadThreads <= 0 {
		uploadThreads = 2
	}
	// Limit max threads to prevent overwhelming the server
	if uploadThreads > 10 {
		uploadThreads = 10
	}
	
	log.Infof("Starting threaded upload with %d threads for %d chunks", uploadThreads, count)

	// Prepare chunks info
	chunks := make([]ChunkInfo, count)
	left := streamSize
	for i := 0; i < count; i++ {
		byteSize := chunkSize
		if left < chunkSize {
			byteSize = left
		}
		chunks[i] = ChunkInfo{
			Index:  i,
			Offset: int64(i) * chunkSize,
			Size:   byteSize,
		}
		left -= byteSize
	}

	// Upload chunks with threading and retry
	uploadBlockList := make([]string, count)
	err = d.uploadChunksThreaded(ctx, tempFile, chunks, uploadBlockList, locateupload_resp.Host, 
		params, stream.GetName(), uploadThreads, up)
	if err != nil {
		return err
	}

	// create file
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

func (d *Terabox) uploadChunksThreaded(ctx context.Context, tempFile io.ReaderAt, chunks []ChunkInfo, 
	uploadBlockList []string, host string, params map[string]string, fileName string, 
	uploadThreads int, up driver.UpdateProgress) error {
	
	var wg sync.WaitGroup
	var mu sync.Mutex
	var uploadErr error
	
	// Channel to limit concurrent uploads
	semaphore := make(chan struct{}, uploadThreads)
	
	// Progress tracking
	completedChunks := 0
	totalChunks := len(chunks)
	
	for i := range chunks {
		if utils.IsCanceled(ctx) {
			return ctx.Err()
		}
		
		wg.Add(1)
		go func(chunkIndex int) {
			defer wg.Done()
			
			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			
			chunk := chunks[chunkIndex]
			
			// Retry logic for chunk upload
			var chunkErr error
			for retryCount := 0; retryCount < maxRetries; retryCount++ {
				if utils.IsCanceled(ctx) {
					return
				}
				
				chunkErr = d.uploadSingleChunk(ctx, tempFile, chunk, host, params, fileName, 
					func(md5Hash string) {
						mu.Lock()
						uploadBlockList[chunkIndex] = md5Hash
						completedChunks++
						progress := float64(completedChunks) * 100.0 / float64(totalChunks)
						mu.Unlock()
						
						if up != nil {
							up(progress)
						}
					})
				
				if chunkErr == nil {
					break
				}
				
				log.Warnf("Chunk %d upload failed (attempt %d/%d): %v", 
					chunkIndex, retryCount+1, maxRetries, chunkErr)
				
				if retryCount < maxRetries-1 {
					// Exponential backoff
					backoffDuration := time.Duration(retryCount+1) * retryBackoffBase
					time.Sleep(backoffDuration)
				}
			}
			
			if chunkErr != nil {
				mu.Lock()
				if uploadErr == nil {
					uploadErr = fmt.Errorf("chunk %d upload failed after %d retries: %v", 
						chunkIndex, maxRetries, chunkErr)
				}
				mu.Unlock()
			}
		}(i)
	}
	
	wg.Wait()
	return uploadErr
}

func (d *Terabox) uploadSingleChunk(ctx context.Context, tempFile io.ReaderAt, chunk ChunkInfo, 
	host string, params map[string]string, fileName string, onSuccess func(string)) error {
	
	// Read chunk data
	chunkData := make([]byte, chunk.Size)
	_, err := tempFile.ReadAt(chunkData, chunk.Offset)
	if err != nil {
		return fmt.Errorf("failed to read chunk data: %v", err)
	}
	
	// Calculate MD5 hash
	h := md5.New()
	h.Write(chunkData)
	md5Hash := hex.EncodeToString(h.Sum(nil))
	
	// Upload chunk
	u := "https://" + host + "/rest/2.0/pcs/superfile2"
	uploadParams := make(map[string]string)
	for k, v := range params {
		uploadParams[k] = v
	}
	uploadParams["partseq"] = strconv.Itoa(chunk.Index)
	
	// Create a context with timeout instead of using SetTimeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	
	res, err := base.RestyClient.R().
		SetContext(timeoutCtx).
		SetQueryParams(uploadParams).
		SetFileReader("file", fileName, bytes.NewReader(chunkData)).
		SetHeader("Cookie", d.Cookie).
		Post(u)
	
	if err != nil {
		return fmt.Errorf("HTTP request failed: %v", err)
	}
	
	if res.StatusCode() != 200 {
		return fmt.Errorf("HTTP status %d: %s", res.StatusCode(), res.String())
	}
	
	// Check response for errors
	responseBody := res.String()
	if responseBody != "" {
		errno := utils.Json.Get([]byte(responseBody), "errno").ToInt()
		if errno != 0 {
			return fmt.Errorf("upload error, errno: %d, response: %s", errno, responseBody)
		}
	}
	
	log.Debugf("Chunk %d uploaded successfully (size: %d bytes)", chunk.Index, chunk.Size)
	onSuccess(md5Hash)
	return nil
}

var _ driver.Driver = (*Terabox)(nil)
