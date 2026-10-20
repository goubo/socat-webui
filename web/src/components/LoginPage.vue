<script setup lang="ts">
import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import 'element-plus/theme-chalk/el-message.css'
import { api, setToken } from '../api'

defineProps<{ version: string }>()
const emit = defineEmits<{ success: [] }>()

const form = reactive({ password: '' })
const loading = ref(false)

async function submit() {
  if (!form.password) {
    ElMessage.warning('请输入密码')
    return
  }
  loading.value = true
  try {
    const { token } = await api.login(form.password)
    setToken(token)
    emit('success')
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '登录失败')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <el-card class="login-card">
      <h2 class="title">socat-webui</h2>
      <p class="subtitle">端口映射控制面板</p>
      <p class="version">版本 {{ version }}</p>
      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item label="面板密码">
          <el-input
            v-model="form.password"
            type="password"
            name="password"
            autocomplete="current-password"
            placeholder="请输入面板密码"
            show-password
            size="large"
            @keyup.enter="submit"
          />
        </el-form-item>
        <el-button type="primary" size="large" class="login-btn" :loading="loading" @click="submit">
          登录
        </el-button>
      </el-form>
    </el-card>
  </div>
</template>

<style scoped>
.login-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100dvh;
  padding: 16px;
  background: #f5f7fa;
}
.login-card {
  width: min(360px, 100%);
}
.title {
  margin: 8px 0 4px;
  text-align: center;
}
.subtitle {
  margin: 0 0 4px;
  color: #909399;
  text-align: center;
}
.version {
  margin: 0 0 20px;
  color: #a8abb2;
  font-size: 12px;
  text-align: center;
}
.login-btn {
  width: 100%;
}
</style>
