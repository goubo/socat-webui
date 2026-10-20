<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import { api, getToken } from './api'
import LoginPage from './components/LoginPage.vue'
import ProxyDashboard from './components/ProxyDashboard.vue'

const authed = ref(!!getToken())
const appVersion = ref('未知')

function handleAuthExpired() {
  authed.value = false
}

onMounted(() => {
  window.addEventListener('auth-expired', handleAuthExpired)
  api.version().then(({ version }) => {
    appVersion.value = version
  }).catch(() => {
    // 登录页仍需正常使用，版本获取失败时保留明确的未知状态。
  })
})

onUnmounted(() => window.removeEventListener('auth-expired', handleAuthExpired))
</script>

<template>
  <el-config-provider :locale="zhCn">
    <LoginPage v-if="!authed" :version="appVersion" @success="authed = true" />
    <ProxyDashboard v-else :version="appVersion" @logout="authed = false" />
  </el-config-provider>
</template>
