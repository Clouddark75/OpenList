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
)

const (
	// Límite de 5GB para descarga en memoria
	InMemoryMaxSize = int64(5 * 1024 * 1024 * 1024)
	// Tamaño del buffer de lectura
	ChunkSize = 64 * 1024 // 64KB
)

type SimpleHttp struct{}

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
	// Crear cliente HTTP local para esta descarga
	client := &http.Client{}
	
	streamPut := task.DeletePolicy == tool.UploadDownloadStream
	method := http.MethodGet
	
	// Para streaming, primero hacemos HEAD para obtener metadata
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

	resp, err := client.Do(req)
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
		
		// En lugar de solo retornar, ahora descargamos con optimización
		// Guardamos el filename para que Transfer lo use
		task.TempDir = filename
		
		// Si el archivo es pequeño (≤ 5GB), lo descargamos en memoria primero
		// y lo escribimos a un archivo temporal que TransferTask usará
		if fileSize > 0 && fileSize <= InMemoryMaxSize {
			return s.downloadToTempForStreaming(task, client, filename, fileSize)
		}
		
		// Si es muy grande, dejamos que TransferTask lo maneje con streaming directo
		return nil
	}

	task.SetTotalBytes(fileSize)

	// Descarga normal (no streaming)
	return s.downloadAdaptive(task, resp.Body, filename, fileSize)
}

// downloadToTempForStreaming descarga en memoria y guarda a temp para que TransferTask lo use
func (s SimpleHttp) downloadToTempForStreaming(task *tool.DownloadTask, client *http.Client, filename string, fileSize int64) error {
	// Hacer el GET request real
	req, err := http.NewRequestWithContext(task.Ctx(), http.MethodGet, task.Url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", base.UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("http status code %d", resp.StatusCode)
	}

	// Descargar en memoria
	buffer := bytes.NewBuffer(make([]byte, 0, fileSize))
	
	chunk := make([]byte, ChunkSize)
	progress := int64(0)

	for {
		// Verificar cancelación
		select {
		case <-task.Ctx().Done():
			return task.Ctx().Err()
		default:
		}

		n, err := resp.Body.Read(chunk)
		if n > 0 {
			progress += int64(n)
			buffer.Write(chunk[:n])
			
			if fileSize > 0 {
				task.SetProgress(float64(progress))
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error durante la lectura HTTP: %w", err)
		}
	}

	// Crear directorio temporal
	if err := os.MkdirAll(task.TempDir, os.ModePerm); err != nil {
		return fmt.Errorf("no se pudo crear directorio temporal: %w", err)
	}

	// Escribir a archivo temporal que TransferTask usará
	filePath := filepath.Join(task.TempDir, filename)
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("no se pudo crear archivo temporal: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, buffer); err != nil {
		return fmt.Errorf("error al escribir desde memoria: %w", err)
	}

	return nil
}

// downloadAdaptive descarga primero en memoria, y si se supera el límite definido,
// automáticamente cambia a escribir directamente al archivo final sin usar temporales.
func (s SimpleHttp) downloadAdaptive(task *tool.DownloadTask, body io.Reader, filename string, fileSize int64) error {
	if err := os.MkdirAll(task.TempDir, os.ModePerm); err != nil {
		return fmt.Errorf("no se pudo crear directorio temporal: %w", err)
	}

	filePath := filepath.Join(task.TempDir, filename)

	var (
		buf      bytes.Buffer
		outFile  *os.File
		written  int64
		useDisk  bool
	)

	// Pre-allocar buffer si conocemos el tamaño y es menor al límite
	if fileSize > 0 && fileSize <= InMemoryMaxSize {
		buf.Grow(int(fileSize))
	}

	// Crear archivo final lazy (solo si es necesario)
	createFinalFile := func() error {
		if outFile != nil {
			return nil
		}
		var err error
		outFile, err = os.Create(filePath)
		if err != nil {
			return fmt.Errorf("no se pudo crear archivo final: %w", err)
		}
		return nil
	}

	// Cleanup del archivo si hubo error
	defer func() {
		if outFile != nil {
			outFile.Close()
		}
	}()

	chunk := make([]byte, ChunkSize)
	progress := int64(0)

	for {
		// Verificar cancelación del contexto
		select {
		case <-task.Ctx().Done():
			// Si hubo error, eliminar archivo parcial
			if outFile != nil {
				outFile.Close()
				os.Remove(filePath)
			}
			return task.Ctx().Err()
		default:
		}

		n, err := body.Read(chunk)
		if n > 0 {
			written += int64(n)
			progress += int64(n)

			// Actualizar progreso si se conoce tamaño
			if fileSize > 0 {
				task.SetProgress(float64(progress))
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
					
					// Crear archivo final
					if err := createFinalFile(); err != nil {
						return err
					}

					// Volcar lo que teníamos en memoria al archivo final
					if buf.Len() > 0 {
						if _, err := outFile.Write(buf.Bytes()); err != nil {
							return fmt.Errorf("error al volcar buffer a disco: %w", err)
						}
						buf.Reset() // Liberar memoria
					}
				}

				// Escribir chunk actual al archivo final
				if _, err := outFile.Write(chunk[:n]); err != nil {
					return fmt.Errorf("error al escribir chunk en disco: %w", err)
				}
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			// En caso de error, eliminar archivo parcial
			if outFile != nil {
				outFile.Close()
				os.Remove(filePath)
			}
			return fmt.Errorf("error durante la lectura HTTP: %w", err)
		}
	}

	// Si terminamos con datos en memoria, escribirlos ahora
	if !useDisk {
		if err := createFinalFile(); err != nil {
			return err
		}
		if _, err := io.Copy(outFile, &buf); err != nil {
			return fmt.Errorf("error al escribir desde memoria: %w", err)
		}
	}

	return nil
}

func init() {
	tool.Tools.Add(&SimpleHttp{})
}
