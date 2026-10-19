package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/model"
)

// BuildRepo 是构建版本仓储。
//
// 目前只有「按 id 取名字」这类最小读取能力：发布/构建自己的管理接口
// 还没做（属于二阶段），但测试单已经要引用构建了 —— 没有这个仓储，
// buildId 就只能是个无人校验的裸数字，写进库后前端连名字都显示不出来。
type BuildRepo struct {
	*Repo[model.Build]
}

// NewBuildRepo 构造构建仓储。
func NewBuildRepo(db *gorm.DB) *BuildRepo {
	return &BuildRepo{Repo: New[model.Build](db)}
}

// NameMap 批量取构建名称。
func (r *BuildRepo) NameMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	out := make(map[uint]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.Build
	if err := r.DB.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, translate(err)
	}
	for i := range rows {
		out[rows[i].ID] = rows[i].Name
	}
	return out, nil
}
