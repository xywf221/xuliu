<script setup>
// 燃尽图。
//
// 手画 SVG 而不是引图表库：这里要的就两条折线加一条网格，
// 为此多一个几百 KB 的依赖（还要为它单独调主题）不划算。
//
// 曲线不是「拿状态推的」—— 后端是逐日重放工时记录重建出来的
// （见 service.WorkbenchService.Burndown），所以历史那一段是真实发生过的形状。
import { computed } from 'vue'

const props = defineProps({
  // days: [{ date, remaining, consumed, added, ideal }]
  days: { type: Array, default: () => [] },
  totalEstimate: { type: Number, default: 0 },
  totalConsumed: { type: Number, default: 0 },
  height: { type: Number, default: 240 },
})

const WIDTH = 720
const PADDING = { top: 16, right: 16, bottom: 28, left: 40 }

const chart = computed(() => {
  const days = props.days || []
  const h = props.height
  const innerW = WIDTH - PADDING.left - PADDING.right
  const innerH = h - PADDING.top - PADDING.bottom

  if (days.length === 0) {
    return { empty: true, h }
  }

  // 纵轴上限取「总工时」与「任意一天剩余」的较大者：
  // 中途加需求会让剩余工时超过初始总量，轴不跟着抬就会画出边界外。
  const values = days.flatMap((d) => [Number(d.remaining) || 0, Number(d.ideal) || 0])
  const max = Math.max(props.totalEstimate || 0, ...values, 1)

  const stepX = days.length > 1 ? innerW / (days.length - 1) : 0
  const x = (i) => PADDING.left + i * stepX
  const y = (v) => PADDING.top + innerH - ((Number(v) || 0) / max) * innerH

  const line = (key) =>
    days.map((d, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)},${y(d[key]).toFixed(1)}`).join(' ')

  const actual = line('remaining')
  const ideal = line('ideal')
  const area = `${actual} L${x(days.length - 1).toFixed(1)},${(PADDING.top + innerH).toFixed(1)} L${x(0).toFixed(1)},${(PADDING.top + innerH).toFixed(1)} Z`

  // 横轴只标几个日期，全标会挤成一团。
  const tickEvery = Math.max(1, Math.ceil(days.length / 7))
  const xTicks = days
    .map((d, i) => ({ i, date: d.date }))
    .filter((t) => t.i % tickEvery === 0 || t.i === days.length - 1)

  // 纵向网格线取 4 条，值从 0 到 max。
  const yTicks = [0, 0.25, 0.5, 0.75, 1].map((r) => ({
    v: Math.round(max * r),
    y: y(max * r),
  }))

  return { empty: false, actual, ideal, area, xTicks, yTicks, x, y, days, h, max }
})

function shortDate(date) {
  // 2026-01-05 → 01-05
  return String(date || '').slice(5)
}
</script>

<template>
  <div v-if="chart.empty" class="xl-empty">该区间内没有任务，画不出燃尽曲线</div>

  <div v-else>
    <div class="legend">
      <span><i class="dot actual" />剩余工时</span>
      <span><i class="dot ideal" />理想剩余</span>
      <span class="xl-dim">
        总工时 {{ totalEstimate }} / 已消耗 {{ totalConsumed }}
      </span>
    </div>

    <svg :viewBox="`0 0 ${720} ${chart.h}`" class="chart" preserveAspectRatio="xMidYMid meet">
      <!-- 网格与纵轴刻度 -->
      <g>
        <line
          v-for="tick in chart.yTicks"
          :key="`grid-${tick.v}`"
          :x1="40"
          :x2="704"
          :y1="tick.y"
          :y2="tick.y"
          stroke="#ebeef5"
          stroke-width="1"
        />
        <text
          v-for="tick in chart.yTicks"
          :key="`label-${tick.v}`"
          :x="34"
          :y="tick.y + 4"
          text-anchor="end"
          font-size="11"
          fill="#909399"
        >
          {{ tick.v }}
        </text>
      </g>

      <!-- 实际剩余：面积 + 折线 -->
      <path :d="chart.area" fill="#409eff" fill-opacity="0.08" />
      <path :d="chart.actual" fill="none" stroke="#409eff" stroke-width="2" />

      <!-- 理想线是虚线：它只是参考，不该和真实曲线抢注意力 -->
      <path
        :d="chart.ideal"
        fill="none"
        stroke="#c0c4cc"
        stroke-width="2"
        stroke-dasharray="5 4"
      />

      <!-- 数据点（天数少时才画，多了会变成一串珠子） -->
      <g v-if="chart.days.length <= 31">
        <circle
          v-for="(d, i) in chart.days"
          :key="`pt-${i}`"
          :cx="chart.x(i)"
          :cy="chart.y(d.remaining)"
          r="3"
          fill="#fff"
          stroke="#409eff"
          stroke-width="2"
        >
          <title>{{ d.date }}：剩余 {{ d.remaining }}，当日消耗 {{ d.consumed }}</title>
        </circle>
      </g>

      <!-- 横轴日期 -->
      <text
        v-for="tick in chart.xTicks"
        :key="`xt-${tick.i}`"
        :x="chart.x(tick.i)"
        :y="chart.h - 8"
        text-anchor="middle"
        font-size="11"
        fill="#909399"
      >
        {{ shortDate(tick.date) }}
      </text>
    </svg>
  </div>
</template>

<style scoped>
.legend {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 6px;
  font-size: 13px;
}

.dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  margin-right: 6px;
  border-radius: 50%;
}

.dot.actual {
  background: #409eff;
}

.dot.ideal {
  background: #c0c4cc;
}

.chart {
  width: 100%;
  height: auto;
}
</style>
