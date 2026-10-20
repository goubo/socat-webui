<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import 'element-plus/theme-chalk/el-message.css'
import 'element-plus/theme-chalk/el-message-box.css'
import { api, ApiError, clearToken } from '../api'
import type { HealthInfo, PrecheckResult, ProxyDetail, ProxyInput, ProxyView, RuntimeState } from '../types'

defineProps<{ version: string }>()
const emit = defineEmits<{ logout: [] }>()

const proxies = ref<ProxyView[]>([])
const health = ref<HealthInfo | null>(null)
const listLoading = ref(false)
let pollTimer: number | undefined

const statusMeta: Record<RuntimeState, { label: string; type: 'success' | 'info' | 'danger' | 'warning' }> = {
  running: { label: '运行中', type: 'success' },
  stopped: { label: '已停止', type: 'info' },
  failed: { label: '失败', type: 'danger' },
  restarting: { label: '重启中', type: 'warning' },
}

function statusInfo(row: { runtime: { status: RuntimeState } }) {
  return statusMeta[row.runtime.status] ?? statusMeta.stopped
}

async function refresh(showLoading = false) {
  if (showLoading) listLoading.value = true
  try {
    ;[proxies.value, health.value] = await Promise.all([api.listProxies(), api.health()])
  } catch (err) {
    if (!(err instanceof ApiError && err.status === 401)) {
      ElMessage.error(err instanceof Error ? err.message : '加载失败')
    }
  } finally {
    listLoading.value = false
  }
}

onMounted(() => {
  refresh(true)
  pollTimer = window.setInterval(() => refresh(), 5000)
})
onUnmounted(() => window.clearInterval(pollTimer))

async function toggle(row: ProxyView, enabled: boolean) {
  try {
    await (enabled ? api.enableProxy(row.id) : api.disableProxy(row.id))
    ElMessage.success(enabled ? '已启用' : '已停用')
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '操作失败')
  }
  refresh()
}

async function remove(row: ProxyView) {
  try {
    await ElMessageBox.confirm(`确定删除代理「${row.name}」？进程将一并停止。`, '删除确认', { type: 'warning' })
  } catch {
    return
  }
  try {
    await api.deleteProxy(row.id)
    ElMessage.success('已删除')
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '删除失败')
  }
  refresh()
}

async function logout() {
  try {
    await api.logout()
  } catch {
    // 登出失败也继续清理本地会话
  }
  clearToken()
  emit('logout')
}

// ---------- 新增 / 编辑 ----------
const dialogVisible = ref(false)
const editing = ref<ProxyView | null>(null)
const submitting = ref(false)
const form = reactive<ProxyInput>({ name: '', localPort: 15201, remoteHost: '', remotePort: 80 })
const prechecking = ref(false)
const precheckResult = ref<PrecheckResult | null>(null)

const portConflict = computed(() => {
  if (!form.localPort) return ''
  const hit = proxies.value.find((p) => p.localPort === form.localPort && p.id !== editing.value?.id)
  return hit ? `本地端口已被代理「${hit.name}」使用` : ''
})

function openCreate() {
  editing.value = null
  Object.assign(form, { name: '', localPort: 15201, remoteHost: '', remotePort: 80 })
  precheckResult.value = null
  dialogVisible.value = true
}

function openEdit(row: ProxyView) {
  editing.value = row
  Object.assign(form, {
    name: row.name,
    localPort: row.localPort,
    remoteHost: row.remoteHost,
    remotePort: row.remotePort,
  })
  precheckResult.value = null
  dialogVisible.value = true
}

async function runPrecheck() {
  prechecking.value = true
  try {
    precheckResult.value = await api.precheck({ ...form })
  } catch (err) {
    precheckResult.value = null
    ElMessage.error(err instanceof Error ? err.message : '检查失败')
  } finally {
    prechecking.value = false
  }
}

async function submitForm() {
  if (!form.name.trim() || !form.remoteHost || !form.localPort || !form.remotePort) {
    ElMessage.warning('请完整填写表单')
    return
  }
  if (portConflict.value) {
    ElMessage.error(portConflict.value)
    return
  }
  submitting.value = true
  try {
    let result: ProxyView
    if (editing.value) {
      result = await api.updateProxy(editing.value.id, { ...form })
    } else {
      result = await api.createProxy({ ...form }, true)
    }
    if (!result.enabled) {
      ElMessage.success('代理配置已保存')
    } else if (result.runtime.status === 'running') {
      ElMessage.success(editing.value ? '已保存并重启代理' : '代理已创建并启动')
    } else {
      const meta = statusInfo(result)
      const detail = result.runtime.detail ? `：${result.runtime.detail}` : ''
      ElMessage.warning(`配置已保存，代理当前${meta.label}${detail}`)
    }
    dialogVisible.value = false
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '提交失败')
  } finally {
    submitting.value = false
  }
  refresh()
}

// ---------- 详情 ----------
const drawerVisible = ref(false)
const detail = ref<ProxyDetail | null>(null)
const detailLoading = ref(false)

async function openDetail(row: ProxyView) {
  drawerVisible.value = true
  detailLoading.value = true
  try {
    detail.value = await api.getProxy(row.id)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载详情失败')
  } finally {
    detailLoading.value = false
  }
}

const eventLabels: Record<string, string> = {
  created: '创建',
  updated: '修改',
  enabled: '启用',
  disabled: '停用',
  exit_failed: '异常退出',
  restore_failed: '恢复失败',
}

function fmtTime(value?: string) {
  if (!value) return '-'
  return new Date(value).toLocaleString('zh-CN', { hour12: false })
}
</script>

<template>
  <el-container class="page">
    <el-header class="header">
      <div class="brand">
        <h1>socat-webui</h1>
        <el-tag type="info" effect="plain">{{ version }}</el-tag>
        <el-tag v-if="health" :type="health.socat.available ? 'success' : 'danger'">
          socat {{ health.socat.available ? health.socat.version : '不可用' }}
        </el-tag>
        <el-tag v-if="health" type="info">代理 {{ health.proxies.total }} · 运行 {{ health.proxies.running }}</el-tag>
      </div>
      <div class="header-actions">
        <el-button @click="refresh(true)">刷新</el-button>
        <el-button type="primary" @click="openCreate">新增代理</el-button>
        <el-button @click="logout">退出登录</el-button>
      </div>
    </el-header>

    <el-main>
      <el-alert
        v-if="health && !health.socat.available"
        type="error"
        :closable="false"
        show-icon
        class="socat-alert"
        :title="`socat 不可用：${health.socat.error ?? '未检测到'}。请安装 socat 后重试，代理启停操作暂被拒绝。`"
      />
      <el-table v-loading="listLoading" :data="proxies" empty-text="暂无代理，点击右上角新增">
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column label="本地端口" width="100">
          <template #default="{ row }">
            <el-tag type="primary" effect="plain">{{ row.localPort }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="远端地址" min-width="180">
          <template #default="{ row }">{{ row.remoteHost }}:{{ row.remotePort }}</template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tooltip v-if="row.runtime.detail" :content="row.runtime.detail" placement="top">
              <el-tag :type="statusInfo(row).type">{{ statusInfo(row).label }}</el-tag>
            </el-tooltip>
            <el-tag v-else :type="statusInfo(row).type">{{ statusInfo(row).label }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="启用" width="90">
          <template #default="{ row }">
            <el-switch :model-value="row.enabled" @change="(val: any) => toggle(row, !!val)" />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row)">详情</el-button>
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-main>

    <el-dialog
      v-model="dialogVisible"
      :title="editing ? '编辑代理' : '新增代理'"
      width="min(520px, calc(100vw - 24px))"
    >
      <el-form label-width="90px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" maxlength="64" placeholder="例如：数据库映射" />
        </el-form-item>
        <el-form-item label="本地端口" required>
          <el-input-number v-model="form.localPort" :min="1" :max="65535" />
          <span v-if="portConflict" class="field-error">{{ portConflict }}</span>
        </el-form-item>
        <el-form-item label="远端地址" required>
          <el-input v-model="form.remoteHost" placeholder="远端 IP 或主机名" />
        </el-form-item>
        <el-form-item label="远端端口" required>
          <el-input-number v-model="form.remotePort" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item>
          <el-button :loading="prechecking" @click="runPrecheck">连通性检查</el-button>
          <template v-if="precheckResult">
            <el-tag :type="precheckResult.remoteOk ? 'success' : 'danger'" class="pc-tag">
              远端{{ precheckResult.remoteOk ? '可达' : '不可达' }}
            </el-tag>
            <el-tag :type="precheckResult.localFree ? 'success' : 'danger'" class="pc-tag">
              本地端口{{ precheckResult.localFree ? '空闲' : '被占' }}
            </el-tag>
          </template>
        </el-form-item>
        <el-alert
          v-if="precheckResult && (!precheckResult.remoteOk || !precheckResult.localFree)"
          type="warning"
          :closable="false"
          :title="precheckResult.remoteErr || precheckResult.localErr"
        />
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitForm">保存</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="drawerVisible" title="代理详情" size="min(420px, 92vw)">
      <div v-loading="detailLoading">
        <template v-if="detail">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="名称">{{ detail.proxy.name }}</el-descriptions-item>
            <el-descriptions-item label="映射">
              本地 {{ detail.proxy.localPort }} → {{ detail.proxy.remoteHost }}:{{ detail.proxy.remotePort }}
            </el-descriptions-item>
            <el-descriptions-item label="状态">
              {{ statusMeta[detail.proxy.runtime.status].label }}
              <span v-if="detail.proxy.runtime.restartCount > 0">
                （已重启 {{ detail.proxy.runtime.restartCount }} 次）
              </span>
            </el-descriptions-item>
            <el-descriptions-item label="进程 PID">
              {{ detail.proxy.runtime.pid ?? '-' }}
            </el-descriptions-item>
            <el-descriptions-item label="启动时间">
              {{ fmtTime(detail.proxy.runtime.startedAt) }}
            </el-descriptions-item>
            <el-descriptions-item v-if="detail.proxy.runtime.detail" label="状态详情">
              {{ detail.proxy.runtime.detail }}
            </el-descriptions-item>
          </el-descriptions>
          <h3 class="event-title">最近事件</h3>
          <el-timeline v-if="detail.events.length">
            <el-timeline-item
              v-for="ev in detail.events"
              :key="ev.id"
              :timestamp="fmtTime(ev.createdAt)"
              :type="ev.type.endsWith('failed') ? 'danger' : 'primary'"
            >
              {{ eventLabels[ev.type] ?? ev.type }} {{ ev.detail }}
            </el-timeline-item>
          </el-timeline>
          <el-empty v-else description="暂无事件" :image-size="60" />
        </template>
      </div>
    </el-drawer>
  </el-container>
</template>

<style scoped>
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: auto;
  min-height: 60px;
  gap: 12px;
  padding: 10px 20px;
  border-bottom: 1px solid #e4e7ed;
}
.brand {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  align-items: center;
}
.header-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  justify-content: flex-end;
}
.header-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.brand h1 {
  margin: 0;
  font-size: 18px;
}
.socat-alert {
  margin-bottom: 12px;
}
.field-error {
  margin-left: 8px;
  color: #f56c6c;
  font-size: 12px;
}
.pc-tag {
  margin-left: 8px;
}
.event-title {
  margin: 20px 0 12px;
  font-size: 14px;
}

@media (max-width: 720px) {
  .header {
    align-items: flex-start;
    flex-direction: column;
    padding: 12px;
  }
  .header-actions {
    justify-content: flex-start;
    width: 100%;
  }
  :deep(.el-main) {
    padding: 12px;
  }
}
</style>
