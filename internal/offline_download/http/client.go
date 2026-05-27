package http

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

const (
	InMemoryMaxSize = int64(5 * 1024 * 1024 * 1024)
)

type SimpleHttp struct {
	client http.Client
}

func (s SimpleHttp) Name() string {
	return "SimpleHttp"
}

func (s SimpleHttp) Items() []model.SettingItem {
	return nil
}

func (s SimpleHttp) Init() (string, error) {
	return "ok", nil
}

func (s SimpleHttp) IsReady() bool {
	return true
}

func (s SimpleHttp) AddURL(args *tool.AddUrlArgs) (string, error) {
	panic("should not be called")
}

func (s SimpleHttp) Remove(task *tool.DownloadTask) error {
	panic("should not be called")
}

func (s SimpleHttp) Status(task *tool.DownloadTask) (*tool.Status, error) {
	panic("should not be called")
}

func (s SimpleHttp) Run(task *tool.DownloadTask) error {
	streamPut := task.DeletePolicy == tool.UploadDownloadStream
	method := http.MethodGet
	if streamPut {
		method = http.MethodHead
	}

	req, err := http.NewRequestWithContext(task.Ctx(), method, task.Url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", base.UserAgent)
	if streamPut {
		req.Header.Set("Range", "bytes=0-")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("http status code %d", resp.StatusCode)
	}

	// Nuevo commit: sanitizeFilename reemplaza path.Base
	filename, err := parseFilenameFromContentDisposition(resp.Header.Get("Content-Disposition"))
	if err != nil {
		filename, err = sanitizeFilename(resp.Request.URL.Path)
	}
	if err != nil {
		filename = strings.ReplaceAll(req.URL.Host, ":", "_")
		filename = fmt.Sprintf("%s-%d-%x", filename, time.Now().UnixMilli(), rand.Uint32())
	}

	fileSize := resp.ContentLength
	if streamPut {
		if fileSize == 0 {
			start, end, _ := http_range.ParseContentRange(resp.Header.Get("Content-Range"))
			fileSize = start + end
		}
		task.SetTotalBytes(fileSize)
		task.TempDir = filename
		return nil
	}

	task.SetTotalBytes(fileSize)

	// Nuevo commit: MkdirAll con manejo de error
	if err := os.MkdirAll(task.TempDir, os.ModePerm); err != nil {
		return err
	}

	filePath := filepath.Join(task.TempDir, filename)

	// Nuevo commit: validación de path traversal
	cleanTempDir := filepath.Clean(task.TempDir) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(filePath)+string(filepath.Separator), cleanTempDir) {
		return fmt.Errorf("filename illegal")
	}

	// Tu cambio: elegir entre memoria o disco según tamaño
	if fileSize > 0 && fileSize <= InMemoryMaxSize {
		return s.downloadToMemory(task, resp.Body, filePath, fileSize)
	}
	return s.downloadToDisk(task, resp.Body, filePath, fileSize)
}

func (s SimpleHttp) downloadToMemory(task *tool.DownloadTask, body io.Reader, filePath string, fileSize int64) error {
	buffer := bytes.NewBuffer(make([]byte, 0, fileSize))

	if err := utils.CopyWithCtx(task.Ctx(), buffer, body, fileSize, task.SetProgress); err != nil {
		return fmt.Errorf("failed to download to memory: %w", err)
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	if _, err = io.Copy(file, buffer); err != nil {
		return fmt.Errorf("failed to write from memory to disk: %w", err)
	}

	return nil
}

func (s SimpleHttp) downloadToDisk(task *tool.DownloadTask, body io.Reader, filePath string, fileSize int64) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	return utils.CopyWithCtx(task.Ctx(), file, body, fileSize, task.SetProgress)
}

func init() {
	tool.Tools.Add(&SimpleHttp{})
}
