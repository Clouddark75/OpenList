package handles

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type StorageResp struct {
	model.Storage
	MountDetails *model.StorageDetails `json:"mount_details,omitempty"`
}

type detailWithIndex struct {
	idx int
	val *model.StorageDetails
}

func makeStorageResp(c *gin.Context, storages []model.Storage) []*StorageResp {
	ret := make([]*StorageResp, len(storages))
	var wg sync.WaitGroup
	
	// Timeout individual por storage: configurable, por defecto 10s
	// Esto permite drivers lentos sin afectar a los rápidos
	storageTimeout := time.Duration(setting.GetInt(conf.StorageDetailsTimeout, 10)) * time.Second
	
	// Timeout global opcional: solo para casos extremos
	// Por defecto 0 = sin timeout (comportamiento original)
	globalTimeout := time.Duration(setting.GetInt(conf.StorageDetailsGlobalTimeout, 0)) * time.Second
	
	for i, s := range storages {
		ret[i] = &StorageResp{
			Storage:      s,
			MountDetails: nil,
		}
		
		if setting.GetBool(conf.HideStorageDetailsInManagePage) {
			continue
		}
		
		d, err := op.GetStorageByMountPath(s.MountPath)
		if err != nil {
			continue
		}
		
		_, ok := d.(driver.WithDetails)
		if !ok {
			continue
		}
		
		wg.Add(1)
		go func(idx int, dri driver.Driver, mountPath string) {
			defer wg.Done()
			
			// Context con timeout individual por storage
			ctx, cancel := context.WithTimeout(c, storageTimeout)
			defer cancel()
			
			details, err := op.GetStorageDetails(ctx, dri, false)
			if err != nil {
				if !errors.Is(err, errs.NotImplement) && !errors.Is(err, errs.StorageNotInit) {
					if errors.Is(err, context.DeadlineExceeded) {
						log.Warnf("timeout loading details for %s after %v", mountPath, storageTimeout)
					} else {
						log.Errorf("failed get %s details: %+v", mountPath, err)
					}
				}
				return
			}
			ret[idx].MountDetails = details
		}(i, d, s.MountPath)
	}
	
	// Si hay timeout global configurado, usarlo
	if globalTimeout > 0 {
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		
		select {
		case <-done:
			// Todos completaron dentro del timeout
		case <-time.After(globalTimeout):
			log.Warnf("global timeout of %v reached while loading storage details", globalTimeout)
		}
	} else {
		// Sin timeout global: esperar a todos (comportamiento original)
		wg.Wait()
	}
	
	return ret
}

func ListStorages(c *gin.Context) {
	var req model.PageReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	req.Validate()
	log.Debugf("%+v", req)
	storages, total, err := db.GetStorages(req.Page, req.PerPage)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, common.PageResp{
		Content: makeStorageResp(c, storages),
		Total:   total,
	})
}

func CreateStorage(c *gin.Context) {
	var req model.Storage
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if id, err := op.CreateStorage(c.Request.Context(), req); err != nil {
		common.ErrorWithDataResp(c, err, 500, gin.H{
			"id": id,
		}, true)
	} else {
		common.SuccessResp(c, gin.H{
			"id": id,
		})
	}
}

func UpdateStorage(c *gin.Context) {
	var req model.Storage
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if err := op.UpdateStorage(c.Request.Context(), req); err != nil {
		common.ErrorResp(c, err, 500, true)
	} else {
		common.SuccessResp(c)
	}
}

func DeleteStorage(c *gin.Context) {
	idStr := c.Query("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if err := op.DeleteStorageById(c.Request.Context(), uint(id)); err != nil {
		common.ErrorResp(c, err, 500, true)
		return
	}
	common.SuccessResp(c)
}

func DisableStorage(c *gin.Context) {
	idStr := c.Query("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if err := op.DisableStorage(c.Request.Context(), uint(id)); err != nil {
		common.ErrorResp(c, err, 500, true)
		return
	}
	common.SuccessResp(c)
}

func EnableStorage(c *gin.Context) {
	idStr := c.Query("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if err := op.EnableStorage(c.Request.Context(), uint(id)); err != nil {
		common.ErrorResp(c, err, 500, true)
		return
	}
	common.SuccessResp(c)
}

func GetStorage(c *gin.Context) {
	idStr := c.Query("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	storage, err := db.GetStorageById(uint(id))
	if err != nil {
		common.ErrorResp(c, err, 500, true)
		return
	}
	common.SuccessResp(c, storage)
}

func LoadAllStorages(c *gin.Context) {
	storages, err := db.GetEnabledStorages()
	if err != nil {
		log.Errorf("failed get enabled storages: %+v", err)
		common.ErrorResp(c, err, 500, true)
		return
	}
	conf.ResetStoragesLoadSignal()
	go func(storages []model.Storage) {
		for _, storage := range storages {
			storageDriver, err := op.GetStorageByMountPath(storage.MountPath)
			if err != nil {
				log.Errorf("failed get storage driver: %+v", err)
				continue
			}
			// drop the storage in the driver
			if err := storageDriver.Drop(context.Background()); err != nil {
				log.Errorf("failed drop storage: %+v", err)
				continue
			}
			if err := op.LoadStorage(context.Background(), storage); err != nil {
				log.Errorf("failed get enabled storages: %+v", err)
				continue
			}
			log.Infof("success load storage: [%s], driver: [%s]",
				storage.MountPath, storage.Driver)
		}
		conf.SendStoragesLoadedSignal()
	}(storages)
	common.SuccessResp(c)
}
