package http

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path"
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
	// Límite de 5GB para descarga en memoria
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

	filename, err := parseFilenameFromContentDisposition(resp.Header.Get("Content-Disposition"))
	if err != nil {
		filename = path.Base(resp.Request.URL.Path)
	}
	filename = strings.Trim(filename, "/")
	if len(filename) == 0 {
		filename = fmt.Sprintf("%s-%d-%x", strings.ReplaceAll(req.URL.Host, ".", "_"), time.Now().UnixMilli(), rand.Uint32())
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

	// Decidir si usar memoria o disco basado en el tamaño del archivo
	if fileSize > 0 && fileSize <= InMemoryMaxSize {
		// Descargar en memoria
		return s.downloadToMemory(task, resp.Body, filename, fileSize)
	}

	// Descargar a disco (comportamiento original)
	return s.downloadToDisk(task, resp.Body, filename, fileSize)
}

// downloadToMemory descarga el archivo directamente en memoria
func (s SimpleHttp) downloadToMemory(task *tool.DownloadTask, body io.Reader, filename string, fileSize int64) error {
	// Crear buffer en memoria con capacidad pre-asignada
	buffer := bytes.NewBuffer(make([]byte, 0, fileSize))

	// Descargar directamente al buffer en memoria
	err := utils.CopyWithCtx(task.Ctx(), buffer, body, fileSize, task.SetProgress)
	if err != nil {
		return fmt.Errorf("failed to download to memory: %w", err)
	}

	// Ahora escribir desde memoria al archivo final
	_ = os.MkdirAll(task.TempDir, os.ModePerm)
	filePath := filepath.Join(task.TempDir, filename)
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Escribir desde el buffer en memoria al archivo
	_, err = io.Copy(file, buffer)
	if err != nil {
		return fmt.Errorf("failed to write from memory to disk: %w", err)
	}

	return nil
}

// downloadToDisk descarga el archivo directamente al disco (comportamiento original)
func (s SimpleHttp) downloadToDisk(task *tool.DownloadTask, body io.Reader, filename string, fileSize int64) error {
	// save to temp dir
	_ = os.MkdirAll(task.TempDir, os.ModePerm)
	filePath := filepath.Join(task.TempDir, filename)
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	err = utils.CopyWithCtx(task.Ctx(), file, body, fileSize, task.SetProgress)
	return err
}

func init() {
	tool.Tools.Add(&SimpleHttp{})
}
