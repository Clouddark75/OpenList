package handles

import (
	"context"	
	"fmt"
	stdpath "path"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/task"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/pkg/generic"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

type MkdirOrLinkReq struct {
	Path string `json:"path" form:"path"`
}

func FsMkdir(c *gin.Context) {
	var req MkdirOrLinkReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	reqPath, err := user.JoinPath(req.Path)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	if !user.CanWrite() {
		meta, err := op.GetNearestMeta(stdpath.Dir(reqPath))
		if err != nil {
			if !errors.Is(errors.Cause(err), errs.MetaNotFound) {
				common.ErrorResp(c, err, 500, true)
				return
			}
		}
		if !common.CanWrite(meta, reqPath) {
			common.ErrorResp(c, errs.PermissionDenied, 403)
			return
		}
	}
	if err := fs.MakeDir(c.Request.Context(), reqPath); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c)
}

type MoveCopyReq struct {
	SrcDir       string   `json:"src_dir"`
	DstDir       string   `json:"dst_dir"`
	Names        []string `json:"names"`
	Overwrite    bool     `json:"overwrite"`
	SkipExisting bool     `json:"skipExisting"`
}

// Función auxiliar para obtener los nombres válidos con merge recursivo
func getValidNamesWithMerge(ctx context.Context, srcDir, dstDir string, names []string, overwrite, skipExisting bool) ([]string, error) {
	if overwrite {
		return names, nil
	}

	var validNames []string
	for _, name := range names {
		dstPath := stdpath.Join(dstDir, name)
		srcPath := stdpath.Join(srcDir, name)
		
		dstObj, _ := fs.Get(ctx, dstPath, &fs.GetArgs{NoLog: true})
		
		if dstObj != nil {
			if !skipExisting {
				return nil, fmt.Errorf("file [%s] exists", name)
			}
			
			// Si skipExisting está activado y el destino existe
			srcObj, err := fs.Get(ctx, srcPath, &fs.GetArgs{NoLog: true})
			if err != nil {
				continue
			}
			
			// Si ambos son directorios, necesitamos hacer merge recursivo
			if srcObj.IsDir() && dstObj.IsDir() {
				// Agregamos este directorio para procesamiento recursivo
				validNames = append(validNames, name)
			}
			// Si es un archivo y existe, lo saltamos (no lo agregamos a validNames)
		} else {
			// El destino no existe, lo agregamos
			validNames = append(validNames, name)
		}
	}
	
	return validNames, nil
}

// Función para procesar recursivamente las carpetas con merge
func processFolderMerge(ctx context.Context, srcPath, dstPath string, operation string) error {
	// Listar contenido de la carpeta origen
	srcFiles, err := fs.List(ctx, srcPath, &fs.ListArgs{})
	if err != nil {
		return err
	}
	
	for _, srcFile := range srcFiles {
		srcFilePath := stdpath.Join(srcPath, srcFile.GetName())
		dstFilePath := stdpath.Join(dstPath, srcFile.GetName())
		
		dstFileObj, _ := fs.Get(ctx, dstFilePath, &fs.GetArgs{NoLog: true})
		
		if dstFileObj != nil {
			// El archivo/carpeta ya existe en el destino
			if srcFile.IsDir() && dstFileObj.IsDir() {
				// Ambos son directorios, hacer merge recursivo
				err = processFolderMerge(ctx, srcFilePath, dstFilePath, operation)
				if err != nil {
					return err
				}
			}
			// Si es un archivo que ya existe, lo saltamos
		} else {
			// El archivo/carpeta no existe en el destino, lo copiamos/movemos
			if operation == "copy" {
				_, err = fs.Copy(ctx, srcFilePath, dstPath, false)
			} else if operation == "move" {
				_, err = fs.Move(ctx, srcFilePath, dstPath, false)
			}
			
			if err != nil {
				return err
			}
		}
	}
	
	return nil
}

func FsMove(c *gin.Context) {
	var req MoveCopyReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if len(req.Names) == 0 {
		common.ErrorStrResp(c, "Empty file names", 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if !user.CanMove() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	srcDir, err := user.JoinPath(req.SrcDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	dstDir, err := user.JoinPath(req.DstDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}

	validNames, err := getValidNamesWithMerge(c.Request.Context(), srcDir, dstDir, req.Names, req.Overwrite, req.SkipExisting)
	if err != nil {
		common.ErrorStrResp(c, err.Error(), 403)
		return
	}

	var addedTasks []task.TaskExtensionInfo
	
	// Procesar cada archivo/carpeta
	for i, name := range validNames {
		srcPath := stdpath.Join(srcDir, name)
		dstPath := stdpath.Join(dstDir, name)
		
		srcObj, err := fs.Get(c.Request.Context(), srcPath, &fs.GetArgs{NoLog: true})
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
		
		dstObj, _ := fs.Get(c.Request.Context(), dstPath, &fs.GetArgs{NoLog: true})
		
		// Si ambos son directorios y skipExisting está activado, hacer merge
		if req.SkipExisting && srcObj.IsDir() && dstObj != nil && dstObj.IsDir() {
			err = processFolderMerge(c.Request.Context(), srcPath, dstPath, "move")
			if err != nil {
				common.ErrorResp(c, err, 500)
				return
			}
			// Después del merge, remover la carpeta origen si está vacía
			// (opcional, dependiendo del comportamiento deseado)
			continue
		}
		
		// Operación normal de move
		t, err := fs.Move(c.Request.Context(), srcPath, dstDir, len(validNames) > i+1)
		if t != nil {
			addedTasks = append(addedTasks, t)
		}
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
	}

	if len(addedTasks) > 0 {
		common.SuccessResp(c, gin.H{
			"message": fmt.Sprintf("Successfully created %d move task(s)", len(addedTasks)),
			"tasks":   getTaskInfos(addedTasks),
		})
	} else {
		common.SuccessResp(c, gin.H{
			"message": "Move operations completed immediately",
		})
	}
}

func FsCopy(c *gin.Context) {
	var req MoveCopyReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if len(req.Names) == 0 {
		common.ErrorStrResp(c, "Empty file names", 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if !user.CanCopy() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	srcDir, err := user.JoinPath(req.SrcDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	dstDir, err := user.JoinPath(req.DstDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}

	validNames, err := getValidNamesWithMerge(c.Request.Context(), srcDir, dstDir, req.Names, req.Overwrite, req.SkipExisting)
	if err != nil {
		common.ErrorStrResp(c, err.Error(), 403)
		return
	}

	var addedTasks []task.TaskExtensionInfo
	
	// Procesar cada archivo/carpeta
	for i, name := range validNames {
		srcPath := stdpath.Join(srcDir, name)
		dstPath := stdpath.Join(dstDir, name)
		
		srcObj, err := fs.Get(c.Request.Context(), srcPath, &fs.GetArgs{NoLog: true})
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
		
		dstObj, _ := fs.Get(c.Request.Context(), dstPath, &fs.GetArgs{NoLog: true})
		
		// Si ambos son directorios y skipExisting está activado, hacer merge
		if req.SkipExisting && srcObj.IsDir() && dstObj != nil && dstObj.IsDir() {
			err = processFolderMerge(c.Request.Context(), srcPath, dstPath, "copy")
			if err != nil {
				common.ErrorResp(c, err, 500)
				return
			}
			continue
		}
		
		// Operación normal de copy
		t, err := fs.Copy(c.Request.Context(), srcPath, dstDir, len(validNames) > i+1)
		if t != nil {
			addedTasks = append(addedTasks, t)
		}
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
	}

	if len(addedTasks) > 0 {
		common.SuccessResp(c, gin.H{
			"message": fmt.Sprintf("Successfully created %d copy task(s)", len(addedTasks)),
			"tasks":   getTaskInfos(addedTasks),
		})
	} else {
		common.SuccessResp(c, gin.H{
			"message": "Copy operations completed immediately",
		})
	}
}

type RenameReq struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Overwrite bool   `json:"overwrite"`
}

func FsRename(c *gin.Context) {
	var req RenameReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if !user.CanRename() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	reqPath, err := user.JoinPath(req.Path)
	if err == nil {
		err = checkRelativePath(req.Name)
	}
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	if !req.Overwrite {
		dstPath := stdpath.Join(stdpath.Dir(reqPath), req.Name)
		if dstPath != reqPath {
			if res, _ := fs.Get(c.Request.Context(), dstPath, &fs.GetArgs{NoLog: true}); res != nil {
				common.ErrorStrResp(c, fmt.Sprintf("file [%s] exists", req.Name), 403)
				return
			}
		}
	}
	if err := fs.Rename(c.Request.Context(), reqPath, req.Name); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c)
}

func checkRelativePath(path string) error {
	if strings.ContainsAny(path, "/\\") || path == "" || path == "." || path == ".." {
		return errs.RelativePath
	}
	return nil
}

type RemoveReq struct {
	Dir   string   `json:"dir"`
	Names []string `json:"names"`
}

func FsRemove(c *gin.Context) {
	var req RemoveReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if len(req.Names) == 0 {
		common.ErrorStrResp(c, "Empty file names", 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if !user.CanRemove() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	reqDir, err := user.JoinPath(req.Dir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	for _, name := range req.Names {
		err := fs.Remove(c.Request.Context(), stdpath.Join(reqDir, name))
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
	}
	//fs.ClearCache(req.Dir)
	common.SuccessResp(c)
}

type RemoveEmptyDirectoryReq struct {
	SrcDir string `json:"src_dir"`
}

func FsRemoveEmptyDirectory(c *gin.Context) {
	var req RemoveEmptyDirectoryReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}

	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if !user.CanRemove() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	srcDir, err := user.JoinPath(req.SrcDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}

	meta, err := op.GetNearestMeta(srcDir)
	if err != nil {
		if !errors.Is(errors.Cause(err), errs.MetaNotFound) {
			common.ErrorResp(c, err, 500, true)
			return
		}
	}
	common.GinWithValue(c, conf.MetaKey, meta)

	rootFiles, err := fs.List(c.Request.Context(), srcDir, &fs.ListArgs{})
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}

	// record the file path
	filePathMap := make(map[model.Obj]string)
	// record the parent file
	fileParentMap := make(map[model.Obj]model.Obj)
	// removing files
	removingFiles := generic.NewQueue[model.Obj]()
	// removed files
	removedFiles := make(map[string]bool)
	for _, file := range rootFiles {
		if !file.IsDir() {
			continue
		}
		removingFiles.Push(file)
		filePathMap[file] = srcDir
	}

	for !removingFiles.IsEmpty() {

		removingFile := removingFiles.Pop()
		removingFilePath := fmt.Sprintf("%s/%s", filePathMap[removingFile], removingFile.GetName())

		if removedFiles[removingFilePath] {
			continue
		}

		subFiles, err := fs.List(c.Request.Context(), removingFilePath, &fs.ListArgs{Refresh: true})
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}

		if len(subFiles) == 0 {
			// remove empty directory
			err = fs.Remove(c.Request.Context(), removingFilePath)
			removedFiles[removingFilePath] = true
			if err != nil {
				common.ErrorResp(c, err, 500)
				return
			}
			// recheck parent folder
			parentFile, exist := fileParentMap[removingFile]
			if exist {
				removingFiles.Push(parentFile)
			}

		} else {
			// recursive remove
			for _, subFile := range subFiles {
				if !subFile.IsDir() {
					continue
				}
				removingFiles.Push(subFile)
				filePathMap[subFile] = removingFilePath
				fileParentMap[subFile] = removingFile
			}
		}

	}

	common.SuccessResp(c)
}

// Link return real link, just for proxy program, it may contain cookie, so just allowed for admin
func Link(c *gin.Context) {
	var req MkdirOrLinkReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	//user := c.Request.Context().Value(conf.UserKey).(*model.User)
	//rawPath := stdpath.Join(user.BasePath, req.Path)
	// why need not join base_path? because it's always the full path
	rawPath := req.Path
	storage, err := fs.GetStorage(rawPath, &fs.GetStoragesArgs{})
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if storage.Config().NoLinkURL {
		common.SuccessResp(c, model.Link{
			URL: fmt.Sprintf("%s/p%s?d&sign=%s",
				common.GetApiUrl(c),
				utils.EncodePath(rawPath, true),
				sign.Sign(rawPath)),
		})
		return
	}
	link, _, err := fs.Link(c.Request.Context(), rawPath, model.LinkArgs{IP: c.ClientIP(), Header: c.Request.Header, Redirect: true})
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	defer link.Close()
	common.SuccessResp(c, link)
}

func getTaskInfos(tasks []task.TaskExtensionInfo) []interface{} {
	infos := make([]interface{}, len(tasks))
	for i, t := range tasks {
		infos[i] = t
	}
	return infos
}
