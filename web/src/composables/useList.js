import { computed, onMounted, reactive, ref } from 'vue'

// 列表页的通用状态：分页 + 筛选 + 加载。
//
// 四个列表页（产品/需求/项目/任务/缺陷）的分页逻辑一模一样，
// 各写一遍的结果是「有一个页面的空值过滤忘了写」这类不一致。
// 抽出来之后每页只剩「筛选长什么样」和「表格长什么样」。
//
// 空值一律不发给后端：`status=` 这种空参数在后端会被当成一个真实条件，
// 于是「没选状态」变成了「找状态为空字符串的记录」，一条都查不到。
export function useList(fetcher, initialFilters = {}, initialSize = 20) {
  const list = ref([])
  const total = ref(0)
  const page = ref(1)
  const size = ref(initialSize)
  const loading = ref(false)
  const filters = reactive({ ...initialFilters })

  const query = computed(() => {
    const params = { page: page.value, size: size.value }
    for (const [key, value] of Object.entries(filters)) {
      if (value === '' || value === null || value === undefined || value === 0) continue
      if (Array.isArray(value) && value.length === 0) continue
      params[key] = value
    }
    return params
  })

  async function load() {
    loading.value = true
    try {
      const data = await fetcher(query.value)
      // 兼容两种返回：分页信封 {list,total} 与裸数组。
      if (Array.isArray(data)) {
        list.value = data
        total.value = data.length
      } else {
        list.value = data.list || []
        total.value = data.total || 0
      }
    } finally {
      loading.value = false
    }
  }

  function reload() {
    page.value = 1
    return load()
  }

  function reset() {
    Object.assign(filters, initialFilters)
    return reload()
  }

  onMounted(load)

  return { list, total, page, size, loading, filters, query, load, reload, reset }
}
