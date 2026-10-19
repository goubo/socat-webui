<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import { getToken } from './api'
import LoginPage from './components/LoginPage.vue'
import ProxyDashboard from './components/ProxyDashboard.vue'

const authed = ref(!!getToken())

function handleAuthExpired() {
  authed.value = false
}

onMounted(() => {
  window.addEventListener('auth-expired', handleAuthExpired)
})

onUnmounted(() => window.removeEventListener('auth-expired', handleAuthExpired))
</script>

<template>
  <el-config-provider :locale="zhCn">
    <LoginPage v-if="!authed" @success="authed = true" />
    <ProxyDashboard v-else @logout="authed = false" />
  </el-config-provider>
</template>
