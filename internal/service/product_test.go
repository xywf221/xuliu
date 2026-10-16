package service_test

import (
	"testing"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/service"
)

// 产品详情页上的两个统计（需求数、计划数）来自两条独立的 count 查询，
// 它们失败时会被静默跳过、只留一个 0 —— 所以「恒为 0」这种错不会报错，
// 只会一直显示错误的信息。这里把两个数字都钉住。
func TestProductDetailCountsStoriesAndPlans(t *testing.T) {
	h := newHarness(t)
	productID := h.createProduct("统计产品", "STAT", model.ACLOpen, []uint{reviewer})

	for _, title := range []string{"一期", "二期"} {
		if _, err := h.plans.Create(h.ctx, dto.PlanReq{
			ProductID: productID, Title: title,
		}); err != nil {
			t.Fatalf("建计划 %s 失败: %v", title, err)
		}
	}

	// 两条需求，把其中一条关掉：计数只算「未关闭」的。
	alive := h.createStory(productID, "还在走的需求", nil)
	h.createStory(productID, "已经关掉的需求", nil)
	if _, err := h.stories.Apply(h.ctx, alive, service.ActionInput{
		Action: "close", ClosedReason: model.StoryCloseCancel,
	}); err != nil {
		t.Fatalf("关闭需求失败: %v", err)
	}

	product, err := h.products.Get(h.ctx, productID)
	if err != nil {
		t.Fatalf("取产品详情失败: %v", err)
	}
	if product.PlanCount != 2 {
		t.Errorf("计划数应为 2，实际 %d", product.PlanCount)
	}
	if product.StoryCount != 1 {
		t.Errorf("需求数应只算未关闭的 1 条，实际 %d", product.StoryCount)
	}
}
