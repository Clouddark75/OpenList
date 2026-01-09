package terabox

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	stdpath "path"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/avast/retry-go"
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
	
	// Verificar cancelación
	if utils.IsCanceled(ctx) {
		return ctx.Err()
	}
	
	// No usar retry - la API de login es sensible y podría causar bloqueos
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
	
	return nil
}

func (d *Terabox) Drop(ctx context.Context) error {
	return nil
}

func (d *Terabox) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	// MEJORA: Pasar contexto a getFiles para cancelación
	files, err := d.getFiles(ctx, dir.GetPath())
	if err != nil {
		return nil, err
	}
	return utils.SliceConvert(files, func(src File) (model.Obj, error) {
		obj := fileToObj(src)
		obj.Path = stdpath.Join(dir.GetPath(), obj.Name)
		return obj, nil
	})
}

func (d *Terabox) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	var link *model.Link
	err := retry.Do(
		func() error {
			// MEJORA: Verificar cancelación
			if utils.IsCanceled(ctx) {
				return retry.Unrecoverable(ctx.Err())
			}
			
			var err error
			if d.DownloadAPI == "crack" {
				link, err = d.linkCrack(file, args)
			} else {
				link, err = d.linkOfficial(file, args)
			}
			return err
		},
		retry.Attempts(uint(d.getRetryCount())),
		retry.Delay(time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			log.Warnf("Failed to get download link (attempt %d): %v", n+1, err)
		}),
	)
	return link, err
}

func (d *Terabox) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	// Verificar cancelación
	if utils.IsCanceled(ctx) {
		return ctx.Err()
	}
	
	params := map[string]string{
		"a": "commit",
	}
	data := map[string]string{
		"path":       stdpath.Join(parentDir.GetPath(), dirName),
		"isdir":      "1",
		"block_list": "[]",
	}
	// No necesita retry - post_form ya tiene retry interno en request()
	res, err := d.post_form("/api/create", params, data, nil)
	log.Debugln(string(res))
	return err
}

func (d *Terabox) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	// Verificar cancelación
	if utils.IsCanceled(ctx) {
		return ctx.Err()
	}
	
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
	// No necesita retry - manage() ya tiene retry interno en request()
	_, err := d.manage("move", data)
	return err
}

func (d *Terabox) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	return retry.Do(
		func() error {
			// MEJORA: Verificar cancelación
			if utils.IsCanceled(ctx) {
				return retry.Unrecoverable(ctx.Err())
			}
			
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
		},
		retry.Attempts(uint(d.getRetryCount())),
		retry.Delay(time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			log.Warnf("Failed to rename file (attempt %d): %v", n+1, err)
		}),
	)
}

func (d *Terabox) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	return retry.Do(
		func() error {
			// MEJORA: Verificar cancelación
			if utils.IsCanceled(ctx) {
				return retry.Unrecoverable(ctx.Err())
			}
			
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
		},
		retry.Attempts(uint(d.getRetryCount())),
		retry.Delay(time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			log.Warnf("Failed to copy file (attempt %d): %v", n+1, err)
		}),
	)
}

func (d *Terabox) Remove(ctx context.Context, obj model.Obj) error {
	return retry.Do(
		func() error {
			// MEJORA: Verificar cancelación
			if utils.IsCanceled(ctx) {
				return retry.Unrecoverable(ctx.Err())
			}
			
			// Ensure jsToken is available for delete operation
			if err := d.ensureJsToken(); err != nil {
				return fmt.Errorf("failed to get jsToken for remove: %v", err)
			}
			
			data := []string{obj.GetPath()}
			_, err := d.manage("delete", data)
			return err
		},
		retry.Attempts(uint(d.getRetryCount())),
		retry.Delay(time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			log.Warnf("Failed to remove file (attempt %d): %v", n+1, err)
		}),
	)
}

// MEJORA: Simplificado - sin retry wrapper redundante
func (d *Terabox) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	return d.putWithRetry(ctx, dstDir, stream, up)
}

func (d *Terabox) putWithRetry(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	// Verificar cancelación temprana
	if utils.IsCanceled(ctx) {
		return ctx.Err()
	}

	// Ensure jsToken is available before upload
	if err := d.ensureJsToken(); err != nil {
		return fmt.Errorf("failed to get jsToken for upload: %v", err)
	}
	
	if utils.IsCanceled(ctx) {
		return ctx.Err()
	}

	resp, err := base.RestyClient.R().
		SetContext(ctx).
		Get("https://" + d.url_domain_prefix + "-data.terabox.com/rest/2.0/pcs/file?method=locateupload")
	if err != nil {
		return err
	}
	
	if utils.IsCanceled(ctx) {
		return ctx.Err()
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
	
	if utils.IsCanceled(ctx) {
		return ctx.Err()
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
	tempFile, err := stream.CacheFullAndWriter(&up, nil)
	if err != nil {
		return err
	}
	
	// MEJORA CRÍTICA: Asegurar limpieza del archivo temporal
	// Si tempFile implementa io.Closer, cerrarlo al final
	if closer, ok := tempFile.(io.Closer); ok {
		defer func() {
			if closeErr := closer.Close(); closeErr != nil {
				log.Warnf("Failed to close temp file: %v", closeErr)
			}
		}()
	}
	
	if utils.IsCanceled(ctx) {
		return ctx.Err()
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
	count := int((streamSize + chunkSize - 1) / chunkSize)
	
	// Get upload threads setting with default value
	uploadThreads := d.UploadThreads
	if uploadThreads <= 0 {
		uploadThreads = 4
	}
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
	
	// Sleep con verificación de cancelación
	sleepDuration := time.Duration(len(precreateResp.BlockList)/16+5) * time.Second
	select {
	case <-time.After(sleepDuration):
	case <-ctx.Done():
		return ctx.Err()
	}
	
	return nil
}

// MEJORA CRÍTICA: Mejor control de goroutines y cancelación inmediata + progreso preciso por bytes
func (d *Terabox) uploadChunksThreaded(ctx context.Context, tempFile io.ReaderAt, chunks []ChunkInfo, 
	uploadBlockList []string, host string, params map[string]string, fileName string, 
	uploadThreads int, up driver.UpdateProgress) error {
	
	// Context cancelable para propagación inmediata
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	
	var wg sync.WaitGroup
	var mu sync.Mutex
	var uploadErr error
	var errOnce sync.Once
	
	// MEJORA: Usar atomic para bytes transferidos (más preciso que chunks)
	var uploadedBytes atomic.Int64
	var totalBytes int64
	for _, chunk := range chunks {
		totalBytes += chunk.Size
	}
	
	// Channel to limit concurrent uploads
	semaphore := make(chan struct{}, uploadThreads)
	
	// MEJORA: Canal para detener la creación de nuevas goroutines
	stopLaunching := make(chan struct{})
	
	// Función para reportar error y cancelar
	reportError := func(err error) {
		errOnce.Do(func() {
			uploadErr = err
			cancel()
			close(stopLaunching) // Detener lanzamiento de nuevas goroutines
		})
	}
	
	// MEJORA CRÍTICA: Loop corregido para salir correctamente
	for i := range chunks {
		// Verificar si debemos detenernos
		select {
		case <-ctx.Done():
			goto waitForCompletion // Salir del loop correctamente
		case <-stopLaunching:
			goto waitForCompletion
		default:
		}
		
		wg.Add(1)
		go func(chunkIndex int) {
			defer wg.Done()
			
			// Verificar cancelación antes de adquirir semáforo
			select {
			case <-ctx.Done():
				return
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			}
			
			chunk := chunks[chunkIndex]
			
			// Use retry-go for chunk upload
			chunkErr := retry.Do(
				func() error {
					// Verificar cancelación en cada intento
					select {
					case <-ctx.Done():
						return retry.Unrecoverable(ctx.Err())
					default:
					}
					
					return d.uploadSingleChunk(ctx, tempFile, chunk, host, params, fileName, 
						func(md5Hash string) {
							mu.Lock()
							uploadBlockList[chunkIndex] = md5Hash
							mu.Unlock()
							
							// MEJORA: Progreso basado en bytes reales, no chunks
							uploaded := uploadedBytes.Add(chunk.Size)
							progress := float64(uploaded) * 100.0 / float64(totalBytes)
							
							if up != nil {
								up(progress)
							}
							
							// Log con información útil
							log.Debugf("Chunk %d/%d uploaded (%.2f%% - %s/%s)", 
								chunkIndex+1, len(chunks), progress,
								formatBytes(uploaded), formatBytes(totalBytes))
						})
				},
				retry.Attempts(uint(d.getRetryCount())),
				retry.Delay(time.Second),
				retry.DelayType(retry.BackOffDelay),
				retry.OnRetry(func(n uint, err error) {
					log.Warnf("Chunk %d upload failed (attempt %d): %v", chunkIndex, n+1, err)
				}),
				// Detener retry si el contexto fue cancelado
				retry.RetryIf(func(err error) bool {
					select {
					case <-ctx.Done():
						return false
					default:
						return true
					}
				}),
			)
			
			if chunkErr != nil {
				reportError(fmt.Errorf("chunk %d upload failed after retries: %v", chunkIndex, chunkErr))
			}
		}(i)
	}
	
waitForCompletion:
	wg.Wait()
	
	// Verificar si fue cancelación o error
	if ctx.Err() != nil {
		return ctx.Err()
	}
	
	return uploadErr
}

// MEJORA: Timeout dinámico basado en tamaño del chunk
func (d *Terabox) uploadSingleChunk(ctx context.Context, tempFile io.ReaderAt, chunk ChunkInfo, 
	host string, params map[string]string, fileName string, onSuccess func(string)) error {
	
	// Verificar cancelación temprana
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	
	// Read chunk data
	chunkData := make([]byte, chunk.Size)
	_, err := tempFile.ReadAt(chunkData, chunk.Offset)
	if err != nil {
		return fmt.Errorf("failed to read chunk data: %v", err)
	}
	
	// Verificar cancelación después de leer
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
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
	
	// MEJORA: Timeout dinámico basado en tamaño (mínimo 30s, +5s por MB)
	timeoutDuration := 30*time.Second + time.Duration(chunk.Size/(1024*1024))*5*time.Second
	if timeoutDuration > 5*time.Minute {
		timeoutDuration = 5 * time.Minute // Máximo 5 minutos
	}
	
	timeoutCtx, cancel := context.WithTimeout(ctx, timeoutDuration)
	defer cancel()
	
	res, err := base.RestyClient.R().
		SetContext(timeoutCtx).
		SetQueryParams(uploadParams).
		SetFileReader("file", fileName, bytes.NewReader(chunkData)).
		SetHeader("Cookie", d.Cookie).
		Post(u)
	
	if err != nil {
		// Diferenciar entre cancelación y error de red
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return fmt.Errorf("HTTP request failed: %v", err)
		}
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

// Helper function to get retry count from config
func (d *Terabox) getRetryCount() int {
	if d.RetryCount <= 0 {
		return 10 // default
	}
	if d.RetryCount > 10 {
		return 10 // max limit
	}
	return d.RetryCount
}

func (d *Terabox) GetDetails(ctx context.Context) (*model.StorageDetails, error) {
	// MEJORA: Verificar cancelación
	if utils.IsCanceled(ctx) {
		return nil, ctx.Err()
	}
	
	var quotaResp QuotaResp
	_, err := d.get("/api/quota", nil, &quotaResp)
	if err != nil {
		return nil, err
	}
	
	if quotaResp.Errno != 0 {
		return nil, fmt.Errorf("[terabox] failed to get quota, errno: %d", quotaResp.Errno)
	}
	
	// Round to GiB for display consistency.
	const gib = int64(1024 * 1024 * 1024)
	totalGiB := (int64(quotaResp.Total) + gib/2) / gib
	usedGiB := (int64(quotaResp.Used) + gib/2) / gib
	total := totalGiB * gib
	used := usedGiB * gib
	
	return &model.StorageDetails{
		DiskUsage: model.DiskUsage{
			TotalSpace: total,
			UsedSpace:  used,
		},
	}, nil
}

var _ driver.Driver = (*Terabox)(nil)
