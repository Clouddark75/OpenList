// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package webdav

import (
	"context"
	"net/http"
	"path"
	"path/filepath"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// slashClean is equivalent to but slightly more efficient than
// path.Clean("/" + name).
func slashClean(name string) string {
	if name == "" || name[0] != '/' {
		name = "/" + name
	}
	return path.Clean(name)
}

func moveResource(ctx context.Context, src, dstDir, srcDir, srcName, dstName string) error {
	if srcDir == dstDir {
		return fs.Rename(ctx, src, dstName)
	}
	if _, err := fs.Move(context.WithValue(ctx, conf.NoTaskKey, struct{}{}), src, dstDir); err != nil {
		return err
	}
	if srcName != dstName {
		return fs.Rename(ctx, path.Join(dstDir, srcName), dstName)
	}
	return nil
}

// runDeadPropsFileMutation updates dead props and applies the file operation
// inside one DB transaction. If the file operation fails the DB changes are
// rolled back. No backup/staging copies of the data are ever made on disk.
func runDeadPropsFileMutation(ctx context.Context, update func(*gorm.DB) error, apply func() error) error {
	return withDeadPropsTransaction(ctx, func(tx *gorm.DB) error {
		if err := update(tx); err != nil {
			return err
		}
		return apply()
	})
}

// copyResource copies directly to the destination (no staging directory).
func copyResource(ctx context.Context, src, dstDir, dstName string, dstExisted, shallow bool) error {
	dstPath := path.Join(dstDir, dstName)
	if dstExisted {
		if err := fs.Remove(ctx, dstPath); err != nil {
			return err
		}
	}
	if shallow {
		return fs.MakeDir(ctx, dstPath)
	}
	_, err := fs.CopyTo(context.WithValue(ctx, conf.NoTaskKey, struct{}{}), src, dstDir, dstName)
	return err
}

// moveFiles moves files and/or directories from src to dst.
// Individual item permission checks are skipped for performance reasons.
//
// See section 9.9.4 for when various HTTP status codes apply.
func moveFiles(ctx context.Context, src, dst string, overwrite bool) (status int, err error) {
	srcDir := path.Dir(src)
	dstDir := path.Dir(dst)
	srcName := path.Base(src)
	dstName := path.Base(dst)
	user := ctx.Value(conf.UserKey).(*model.User)
	if srcDir != dstDir && !user.CanMove() {
		return http.StatusForbidden, nil
	}
	if srcName != dstName && !user.CanRename() {
		return http.StatusForbidden, nil
	}
	srcMeta, err := op.GetNearestMeta(srcDir)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		return http.StatusInternalServerError, err
	}
	dstMeta, err := op.GetNearestMeta(dstDir)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		return http.StatusInternalServerError, err
	}
	if !common.CanWrite(user, srcMeta, srcDir) || !common.CanWrite(user, dstMeta, dstDir) {
		return http.StatusForbidden, nil
	}
	if _, err = fs.Get(ctx, src, &fs.GetArgs{}); err != nil {
		if errs.IsObjectNotFound(err) {
			return http.StatusNotFound, err
		}
		return http.StatusInternalServerError, err
	}
	if dstDirInfo, err := fs.Get(ctx, dstDir, &fs.GetArgs{}); err != nil {
		if errs.IsObjectNotFound(err) {
			return http.StatusConflict, err
		}
		return http.StatusMethodNotAllowed, err
	} else if !dstDirInfo.IsDir() {
		return http.StatusConflict, nil
	}
	dstExisted := false
	if _, err = fs.Get(ctx, dst, &fs.GetArgs{}); err == nil {
		dstExisted = true
		if !overwrite {
			return http.StatusPreconditionFailed, nil
		}
	} else if !errs.IsObjectNotFound(err) {
		return http.StatusInternalServerError, err
	}
	err = runDeadPropsFileMutation(ctx,
		func(tx *gorm.DB) error { return moveDeadProps(tx, src, dst) },
		func() error {
			if dstExisted && overwrite {
				if err := fs.Remove(ctx, dst); err != nil {
					return err
				}
			}
			return moveResource(ctx, src, dstDir, srcDir, srcName, dstName)
		},
	)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if dstExisted {
		return http.StatusNoContent, nil
	}
	return http.StatusCreated, nil
}

// copyFiles copies files and/or directories from src to dst.
// Individual item permission checks are skipped for performance reasons.
//
// See section 9.8.5 for when various HTTP status codes apply.
func copyFiles(ctx context.Context, src, dst string, overwrite bool, depth int) (status int, err error) {
	srcDir := path.Dir(src)
	dstDir := path.Dir(dst)
	dstName := path.Base(dst)
	user := ctx.Value(conf.UserKey).(*model.User)
	if !user.CanCopy() {
		return http.StatusForbidden, nil
	}
	srcMeta, err := op.GetNearestMeta(srcDir)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		return http.StatusInternalServerError, err
	}
	if !common.CanRead(user, srcMeta, srcDir) {
		return http.StatusForbidden, nil
	}
	dstMeta, err := op.GetNearestMeta(dstDir)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		return http.StatusInternalServerError, err
	}
	if !common.CanWrite(user, dstMeta, dstDir) {
		return http.StatusForbidden, nil
	}
	srcInfo, err := fs.Get(ctx, src, &fs.GetArgs{})
	if err != nil {
		if errs.IsObjectNotFound(err) {
			return http.StatusNotFound, err
		}
		return http.StatusInternalServerError, err
	}
	shallow := depth == 0 && srcInfo.IsDir()
	if dstDirInfo, err := fs.Get(ctx, dstDir, &fs.GetArgs{}); err != nil {
		if errs.IsObjectNotFound(err) {
			return http.StatusConflict, err
		}
		return http.StatusMethodNotAllowed, err
	} else if !dstDirInfo.IsDir() {
		return http.StatusConflict, nil
	}
	dstExisted := false
	if _, err = fs.Get(ctx, dst, &fs.GetArgs{}); err == nil {
		dstExisted = true
		if !overwrite {
			return http.StatusPreconditionFailed, nil
		}
	} else if !errs.IsObjectNotFound(err) {
		return http.StatusInternalServerError, err
	}

	err = runDeadPropsFileMutation(ctx,
		func(tx *gorm.DB) error { return copyDeadProps(tx, src, dst) },
		func() error {
			return copyResource(ctx, src, dstDir, dstName, dstExisted && overwrite, shallow)
		},
	)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if dstExisted {
		return http.StatusNoContent, nil
	}
	return http.StatusCreated, nil
}

// walkFS traverses filesystem fs starting at name up to depth levels.
//
// Allowed values for depth are 0, 1 or infiniteDepth. For each visited node,
// walkFS calls walkFn. If a visited file system node is a directory and
// walkFn returns path.SkipDir, walkFS will skip traversal of this node.
func walkFS(ctx context.Context, depth int, name string, info model.Obj, walkFn func(reqPath string, info model.Obj, err error) error) error {
	// This implementation is based on Walk's code in the standard path/path package.
	err := walkFn(name, info, nil)
	if err != nil {
		if info.IsDir() && err == filepath.SkipDir {
			return nil
		}
		return err
	}
	if !info.IsDir() || depth == 0 {
		return nil
	}
	if depth == 1 {
		depth = 0
	}
	meta, _ := op.GetNearestMeta(name)
	// Read directory names.
	objs, err := fs.List(context.WithValue(ctx, conf.MetaKey, meta), name, &fs.ListArgs{})
	//f, err := fs.OpenFile(ctx, name, os.O_RDONLY, 0)
	//if err != nil {
	//	return walkFn(name, info, err)
	//}
	//fileInfos, err := f.Readdir(0)
	//f.Close()
	if err != nil {
		return walkFn(name, info, err)
	}

	for _, fileInfo := range objs {
		filename := path.Join(name, fileInfo.GetName())
		if err != nil {
			if err := walkFn(filename, fileInfo, err); err != nil && err != filepath.SkipDir {
				return err
			}
		} else {
			err = walkFS(ctx, depth, filename, fileInfo, walkFn)
			if err != nil {
				if !fileInfo.IsDir() || err != filepath.SkipDir {
					return err
				}
			}
		}
	}
	return nil
}
