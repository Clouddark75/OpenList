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
	// Tamaño del buffer de lectura
	ChunkSize = 64 * 1024 // 64KB
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

	// Nueva lógica híbrida (descarga adaptativa)
	return s.downloadAdaptive(task, resp.Body, filename, fileSize)
}

// downloadAdaptive descarga primero en memoria, y si se supera el límite definido,
// automáticamente cambia a escribir en disco sin perder el progreso.
func (s SimpleHttp) downloadAdaptive(task *tool.DownloadTask, body io.Reader, filename string, fileSize int64) error {
	if err := os.MkdirAll(task.TempDir, os.ModePerm); err != nil {
		return fmt.Errorf("no se pudo crear directorio temporal: %w", err)
	}

	filePath := filepath.Join(task.TempDir, filename)

	var (
		buf     bytes.Buffer
		tmpFile *os.File
		written int64
		useDisk bool
	)

	// Pre-allocar buffer si conocemos el tamaño y es menor al límite
	if fileSize > 0 && fileSize <= InMemoryMaxSize {
		buf.Grow(int(fileSize))
	}

	// Crear archivo temporal lazy (solo si es necesario)
	createTempFile := func() error {
		if tmpFile != nil {
			return nil
		}
		var err error
		tmpFile, err = os.CreateTemp(task.TempDir, "partial-*")
		if err != nil {
			return fmt.Errorf("no se pudo crear archivo temporal: %w", err)
		}
		return nil
	}

	// Cleanup del archivo temporal
	defer func() {
		if tmpFile != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
		}
	}()

	chunk := make([]byte, ChunkSize)
	progress := int64(0)

	for {
		// Verificar cancelación del contexto
		select {
		case <-task.Ctx().Done():
			return task.Ctx().Err()
		default:
		}

		n, err := body.Read(chunk)
		if n > 0 {
			written += int64(n)
			progress += int64(n)

			// Actualizar progreso si se conoce tamaño
			if fileSize > 0 {
				task.SetProgress(progress)
			}

			// Decidir dónde escribir
			if !useDisk && written <= InMemoryMaxSize {
				// Seguir en memoria
				buf.Write(chunk[:n])
			} else {
				// Necesitamos usar disco
				if !useDisk {
					// Primera vez que superamos el límite
					useDisk = true
					
					// Crear archivo temporal
					if err := createTempFile(); err != nil {
						return err
					}

					// Volcar lo que teníamos en memoria al disco
					if buf.Len() > 0 {
						if _, err := tmpFile.Write(buf.Bytes()); err != nil {
							return fmt.Errorf("error al volcar buffer a disco: %w", err)
						}
						buf.Reset() // Liberar memoria
					}
				}

				// Escribir chunk actual al disco
				if _, err := tmpFile.Write(chunk[:n]); err != nil {
					return fmt.Errorf("error al escribir chunk en disco: %w", err)
				}
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error durante la lectura HTTP: %w", err)
		}
	}

	// Crear archivo final
	outFile, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("no se pudo crear archivo final: %w", err)
	}
	defer outFile.Close()

	// Escribir al archivo final desde la fuente correspondiente
	if useDisk {
		// Datos están en disco temporal
		if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("error al reposicionar archivo temporal: %w", err)
		}
		if _, err := io.Copy(outFile, tmpFile); err != nil {
			return fmt.Errorf("error al copiar desde archivo temporal: %w", err)
		}
	} else {
		// Datos están en memoria
		if _, err := io.Copy(outFile, &buf); err != nil {
			return fmt.Errorf("error al escribir desde memoria: %w", err)
		}
	}

	return nil
}

func init() {
	tool.Tools.Add(&SimpleHttp{})
}
