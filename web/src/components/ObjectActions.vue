<script setup>
// 状态动作按钮组。
//
// 按钮**不是写死的**，而是后端按当前状态给出的可用动作（TransitionResp）：
// 状态机改了、权限点改了，这里不用动一行。
//
// 后端会把没权限的动作也下发（enabled=false）：显示成置灰 + 悬停提示「需要权限」，
// 比按钮凭空消失更容易让人明白为什么点不了。
//
// 三类动作的输入不一样，所以点下去先问一句：
//   assign  → 选人
//   resolve → 解决方案（缺陷）
//   close   → 关闭原因（需求）
//   finish  → 顺手报工时（任务）
//   change  → 改内容（需求变更，交回父组件处理）
//   edit    → 交回父组件处理（表单太大，不属于「动作」）
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

import UserSelect from '@/components/UserSelect.vue'
import { callAction, isReviewAction } from '@/utils/action-call'
import { bugResolutions, storyClosedReasons } from '@/utils/dict'

const props = defineProps({
  objectType: { type: String, required: true },
  objectId: { type: [Number, String], required: true },
  actions: { type: Array, default: () => [] },
  // 有些动作需要当前版本号（乐观锁），由详情页提供。
  version: { type: Number, default: 0 },
  size: { type: String, default: 'default' },
})

const emit = defineEmits(['done', 'edit'])

const dialogVisible = ref(false)
const submitting = ref(false)
const current = ref(null)

const form = ref({
  comment: '',
  assignedTo: 0,
  resolution: '',
  resolvedBuild: '',
  closedReason: '',
  consumed: 0,
  reviewers: [],
})

// 按 kind + action 决定弹窗里要填什么。
const fields = computed(() => {
  const a = current.value
  if (!a) return {}
  return {
    assignee: a.kind === 'assign',
    resolution: a.action === 'resolve',
    closedReason: a.action === 'close' || a.action === 'reject',
    effort: a.action === 'finish',
    reviewers: a.action === 'submitreview',
    // 评审结论只需一句评语
    review: isReviewAction(a.action),
  }
})

const dialogTitle = computed(() => current.value?.label || '执行动作')

const reasonOptions = computed(() =>
  current.value?.action === 'resolve' ? bugResolutions : storyClosedReasons,
)

function onClick(action) {
  if (!action.enabled) return
  if (action.kind === 'edit') {
    emit('edit', action)
    return
  }
  current.value = action
  form.value = {
    comment: '',
    assignedTo: 0,
    resolution: '',
    resolvedBuild: '',
    closedReason: action.action === 'reject' ? 'willnotdo' : 'done',
    consumed: 0,
    reviewers: [],
  }
  dialogVisible.value = true
}

async function submit() {
  const a = current.value
  if (!a) return

  // 必填校验放在前端是为了少一次往返；后端仍会各自再校验一遍。
  if (fields.value.assignee && !form.value.assignedTo) {
    ElMessage.warning('请选择要指派给谁')
    return
  }
  if (fields.value.resolution && !form.value.resolution) {
    ElMessage.warning('请选择解决方案')
    return
  }
  if (fields.value.closedReason && !form.value.closedReason) {
    ElMessage.warning('请选择原因')
    return
  }
  if (fields.value.reviewers && form.value.reviewers.length === 0) {
    ElMessage.warning('请至少选择一位评审人')
    return
  }

  submitting.value = true
  try {
    await callAction(props.objectType, props.objectId, a.action, {
      ...form.value,
      version: props.version,
    })
    ElMessage.success(`${a.label}成功`)
    dialogVisible.value = false
    emit('done')
  } catch {
    // 失败信息由 HTTP 层统一弹出，这里只负责不关弹窗。
  } finally {
    submitting.value = false
  }
}

// 破坏性动作再确认一次：状态流转没有撤销按钮，
// 「关错了」只能靠重新激活找回来，多问一句值得。
async function onClickWrap(action) {
  if (action.danger && action.enabled) {
    try {
      await ElMessageBox.confirm(`确定要「${action.label}」吗？`, '请确认', {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      })
    } catch {
      return
    }
  }
  onClick(action)
}
</script>

<template>
  <div class="actions">
    <el-tooltip
      v-for="action in actions"
      :key="action.action"
      :content="action.enabled ? '' : `需要权限：${action.perm}`"
      :disabled="action.enabled"
      placement="top"
    >
      <el-button
        :type="action.danger ? 'danger' : 'primary'"
        :plain="!action.danger"
        :size="size"
        :disabled="!action.enabled"
        @click="onClickWrap(action)"
      >
        {{ action.label }}
      </el-button>
    </el-tooltip>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="520px" append-to-body>
      <el-form label-width="88px">
        <el-form-item v-if="fields.assignee" label="指派给" required>
          <UserSelect v-model="form.assignedTo" />
        </el-form-item>

        <el-form-item v-if="fields.reviewers" label="评审人" required>
          <UserSelect v-model="form.reviewers" multiple />
        </el-form-item>

        <el-form-item v-if="fields.resolution" label="解决方案" required>
          <el-select v-model="form.resolution" placeholder="请选择" style="width: 100%">
            <el-option
              v-for="opt in reasonOptions"
              :key="opt.value"
              :value="opt.value"
              :label="opt.label"
            />
          </el-select>
        </el-form-item>

        <el-form-item v-if="fields.resolution" label="解决版本">
          <el-input v-model="form.resolvedBuild" placeholder="如 v1.0.1，可留空" />
        </el-form-item>

        <el-form-item v-if="fields.closedReason" label="原因" required>
          <el-select v-model="form.closedReason" placeholder="请选择" style="width: 100%">
            <el-option
              v-for="opt in reasonOptions"
              :key="opt.value"
              :value="opt.value"
              :label="opt.label"
            />
          </el-select>
        </el-form-item>

        <el-form-item v-if="fields.effort" label="消耗工时">
          <el-input-number v-model="form.consumed" :min="0" :step="0.5" :precision="1" />
          <span class="xl-dim" style="margin-left: 8px">
            顺手把这次干完的工时一起报掉；剩余工时必须为 0 才能完成
          </span>
        </el-form-item>

        <el-form-item label="备注">
          <el-input v-model="form.comment" type="textarea" :rows="3" maxlength="255" show-word-limit />
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
</style>
