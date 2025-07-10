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
	"regexp"
	stdpath "path"
	"strconv"
	"strings"
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
	jsTokenMutex      sync.RWMutex
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
	
	// Initialize JSToken on startup
	if err := d.getJsToken(ctx); err != nil {
		log.Warnf("Failed to initialize JSToken: %v", err)
		// Don't return error as JSToken might not be needed for all operations
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
	_, err := d.manage(ctx, "move", data)
	return err
}

func (d *Terabox) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	data := []base.Json{
		{
			"path":    srcObj.GetPath(),
			"newname": newName,
		},
	}
	_, err := d.manage(ctx, "rename", data)
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
	_, err := d.manage(ctx, "copy", data)
	return err
}

func (d *Terabox) Remove(ctx context.Context, obj model.Obj) error {
	data := []string{obj.GetPath()}
	_, err := d.manage(ctx, "delete", data)
	return err
}

// getJsToken retrieves the JSToken from the main page
func (d *Terabox) getJsToken(ctx context.Context) error {
	d.jsTokenMutex.Lock()
	defer d.jsTokenMutex.Unlock()
	
	// Make a GET request to the main page
	req := base.RestyClient.R().
		SetContext(ctx).
		SetHeaders(map[string]string{
			"Cookie":     d.Cookie,
			"User-Agent": base.UserAgent,
			"Referer":    d.base_url,
		})
	
	resp, err := req.Get(d.base_url)
	if err != nil {
		return fmt.Errorf("failed to get main page: %v", err)
	}
	
	if resp.StatusCode() != 200 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode())
	}
	
	body := resp.String()
	
	// Extract JSToken using the pattern from rclone implementation
	jsToken := getStrBetween(body, "`function%20fn%28a%29%7Bwindow.jsToken%20%3D%20a%7D%3Bfn%28%22", "%22%29`")
	if jsToken == "" {
		// Try alternative patterns that might be used
		patterns := []string{
			`window\.jsToken\s*=\s*"([^"]+)"`,
			`jsToken\s*=\s*"([^"]+)"`,
			`fn\("([^"]+)"\)`,
		}
		
		for _, pattern := range patterns {
			re := regexp.MustCompile(pattern)
			matches := re.FindStringSubmatch(body)
			if len(matches) > 1 {
				jsToken = matches[1]
				break
			}
		}
	}
	
	if jsToken == "" {
		return fmt.Errorf("jsToken not found in response")
	}
	
	d.JsToken = jsToken
	log.Debugf("JSToken retrieved: %s", jsToken)
	return nil
}

// getStrBetween extracts string between two delimiters
func getStrBetween(str, start, end string) string {
	startIdx := strings.Index(str, start)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(start)
	
	endIdx := strings.Index(str[startIdx:], end)
	if endIdx == -1 {
		return ""
	}
	
	return str[startIdx : startIdx+endIdx]
}

// ensureJsToken ensures JSToken is available, fetching it if necessary
func (d *Terabox) ensureJsToken(ctx context.Context) error {
	d.jsTokenMutex.RLock()
	hasToken := d.JsToken != ""
	d.jsTokenMutex.RUnlock()
	
	if !hasToken {
		return d.getJsToken(ctx)
	}
	return nil
}

// manage handles file operations with JSToken support
func (d *Terabox) manage(ctx context.Context, operation string, data interface{}) ([]byte, error) {
	// Ensure JSToken is available
	if err := d.ensureJsToken(ctx); err != nil {
		return nil, fmt.Errorf("failed to get JSToken: %v", err)
	}
	
	maxRetries := 3
	for retry := 0; retry < maxRetries; retry++ {
		result, err := d.performManageOperation(ctx, operation, data)
		if err != nil {
			// Check if it's a JSToken-related error
			if strings.Contains(err.Error(), "jsToken") || 
			   strings.Contains(err.Error(), "4000023") ||
			   strings.Contains(err.Error(), "450016") {
				log.Debugf("JSToken error detected, refreshing token (retry %d/%d)", retry+1, maxRetries)
				if tokenErr := d.getJsToken(ctx); tokenErr != nil {
					log.Warnf("Failed to refresh JSToken: %v", tokenErr)
				}
				continue
			}
			return nil, err
		}
		return result, nil
	}
	
	return nil, fmt.Errorf("operation failed after %d retries", maxRetries)
}

// performManageOperation performs the actual file operation
func (d *Terabox) performManageOperation(ctx context.Context, operation string, data interface{}) ([]byte, error) {
	params := map[string]string{
		"opera":  operation,
		"async":  "1",
		"onnest": "fail",
	}
	
	// Add JSToken to parameters
	d.jsTokenMutex.RLock()
	if d.JsToken != "" {
		params["jsToken"] = d.JsToken
	}
	d.jsTokenMutex.RUnlock()
	
	// Convert data to JSON string
	var jsonData []byte
	var err error
	if operation == "delete" {
		jsonData, err = utils.Json.Marshal(data)
	} else {
		jsonData, err = utils.Json.Marshal(data)
	}
	if err != nil {
		return nil, err
	}
	
	// Prepare form data
	formData := map[string]string{
		"filelist": string(jsonData),
	}
	
	return d.post_form("/api/filemanager", params, formData, nil)
}

func (d *Terabox) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	// Get upload server using the existing utility function
	uploadServerURL, err := d.getUploadServer(ctx)
	if err != nil {
		return err
	}
	
	// Extract host from the full URL for compatibility
	parsedURL, err := url.Parse(uploadServerURL)
	if err != nil {
		return err
	}
	uploadHost := parsedURL.Host

	// Precreate file
	rawPath := stdpath.Join(dstDir.GetPath(), stream.GetName())
	path := encodeURIComponent(rawPath)
	streamSize := stream.GetSize()

	// Calculate optimal chunk size using existing utility
	chunkSize, err := d.prepareChunkUpload(ctx, streamSize)
	if err != nil {
		return err
	}
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
		go d.uploadWorker(ctx, &wg, jobs, results, params, uploadHost, stream.GetName(), precreateResp.Uploadid)
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
func (d *Terabox) uploadWorker(ctx context.Context, wg *sync.WaitGroup, jobs <-chan ChunkJob, results chan<- ChunkResult, baseParams map[string]string, host, fileName, uploadID string) {
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
			// Use the same request pattern as the existing code
			req := base.RestyClient.R().
				SetContext(ctx).
				SetQueryParams(params).
				SetFileReader("file", fileName, bytes.NewReader(job.data)).
				SetHeaders(map[string]string{
					"Cookie":           d.Cookie,
					"Accept":           "application/json, text/plain, */*",
					"Referer":          d.base_url,
					"User-Agent":       base.UserAgent,
					"X-Requested-With": "XMLHttpRequest",
				})
			
			res, err := req.Post("https://" + host + "/rest/2.0/pcs/superfile2")
			
			if err == nil && res.StatusCode() == 200 {
				// Verify chunk upload if needed
				if verifyErr := d.verifyChunkUpload(ctx, uploadID, job.partseq); verifyErr != nil {
					log.Debugf("Chunk %d verification failed: %v", job.partseq, verifyErr)
					// Continue anyway as verification might be optional
				}
				
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
