package rardecode

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/archive/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/nwaples/rardecode/v2"
)

var partRegex = regexp.MustCompile(`^.*\.part(\d+)\.rar$`)

type RarDecoder struct{}

// AcceptedExtensions devuelve las extensiones aceptadas para archivos RAR
func (RarDecoder) AcceptedExtensions() []string {
	return []string{".rar"}
}

// AcceptedMultipartExtensions devuelve las extensiones aceptadas para archivos multipart RAR
func (RarDecoder) AcceptedMultipartExtensions() map[string]tool.MultipartExtension {
	return map[string]tool.MultipartExtension{
		".part1.rar": {PartFileFormat: partRegex, SecondPartIndex: 2}, // Regex para las partes rar
	}
}

// GroupMultipartFiles agrupa los archivos multipart por su basename y ordena las partes
func (RarDecoder) GroupMultipartFiles(ss []*stream.SeekableStream) (map[string][]*stream.SeekableStream, error) {
	groups := make(map[string][]*stream.SeekableStream)
	// Agrupar los archivos por basename y número de parte
	for _, s := range ss {
		matches := partRegex.FindStringSubmatch(s.GetName()) // Aquí usamos GetName() en lugar de Name
		if matches != nil {
			base := matches[1] // Nombre base
			groupKey := base // Agrupamos por el nombre base
			groups[groupKey] = append(groups[groupKey], s) // Añadimos el archivo al grupo correspondiente

			// Ordenar las partes dentro del grupo por su número de parte
			sort.SliceStable(groups[groupKey], func(i, j int) bool {
				// Comparamos el número de parte extraído del nombre del archivo
				partI := partRegex.FindStringSubmatch(groups[groupKey][i].GetName())[2]
				partJ := partRegex.FindStringSubmatch(groups[groupKey][j].GetName())[2]
				return partI < partJ
			})
		}
	}
	return groups, nil
}

// GetMeta obtiene los metadatos del archivo RAR
func (RarDecoder) GetMeta(ss []*stream.SeekableStream, args model.ArchiveArgs) (model.ArchiveMeta, error) {
	l, err := list(ss, args.Password)
	if err != nil {
		return nil, err
	}
	_, tree := tool.GenerateMetaTreeFromFolderTraversal(l)
	return &model.ArchiveMetaInfo{
		Comment:   "",
		Encrypted: false,
		Tree:      tree,
	}, nil
}

// List no está soportado para RAR
func (RarDecoder) List(ss []*stream.SeekableStream, args model.ArchiveInnerArgs) ([]model.Obj, error) {
	return nil, errs.NotSupport
}

// Extract extrae un archivo específico desde el archivo RAR
func (RarDecoder) Extract(ss []*stream.SeekableStream, args model.ArchiveInnerArgs) (io.ReadCloser, int64, error) {
	reader, err := getReader(ss, args.Password)
	if err != nil {
		return nil, 0, err
	}
	innerPath := strings.TrimPrefix(args.InnerPath, "/")
	for {
		var header *rardecode.FileHeader
		header, err = reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		if header.Name == innerPath {
			if header.IsDir {
				break
			}
			return io.NopCloser(reader), header.UnPackedSize, nil
		}
	}
	return nil, 0, errs.ObjectNotFound
}

// Decompress descomprime los archivos en el archivo RAR
func (RarDecoder) Decompress(ss []*stream.SeekableStream, outputPath string, args model.ArchiveInnerArgs, up model.UpdateProgress) error {
	reader, err := getReader(ss, args.Password)
	if err != nil {
		return err
	}
	if args.InnerPath == "/" {
		for {
			var header *rardecode.FileHeader
			header, err = reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			name := header.Name
			if header.IsDir {
				name = name + "/"
			}
			err = decompress(reader, header, name, outputPath)
			if err != nil {
				return err
			}
		}
	} else {
		innerPath := strings.TrimPrefix(args.InnerPath, "/")
		innerBase := filepath.Base(innerPath)
		createdBaseDir := false
		for {
			var header *rardecode.FileHeader
			header, err = reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			name := header.Name
			if header.IsDir {
				name = name + "/"
			}
			if name == innerPath {
				err = _decompress(reader, header, outputPath, up)
				if err != nil {
					return err
				}
				break
			} else if strings.HasPrefix(name, innerPath+"/") {
				targetPath := filepath.Join(outputPath, innerBase)
				if !createdBaseDir {
					err = os.Mkdir(targetPath, 0700)
					if err != nil {
						return err
					}
					createdBaseDir = true
				}
				restPath := strings.TrimPrefix(name, innerPath+"/")
				err = decompress(reader, header, restPath, targetPath)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

var _ tool.Tool = (*RarDecoder)(nil)

func init() {
	tool.RegisterTool(RarDecoder{})
}
