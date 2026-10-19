<script setup lang="ts">
import { onMounted, ref } from 'vue'

const health = ref('检测中…')

onMounted(async () => {
  try {
    const resp = await fetch('/api/health')
    health.value = JSON.stringify(await resp.json())
  } catch {
    health.value = '后端不可用'
  }
})
</script>

<template>
  <main class="placeholder">
    <h1>socat-webui</h1>
    <p>socat 端口映射控制面板（脚手架占位页，界面在 P-005 实现）</p>
    <p class="health">/api/health → {{ health }}</p>
  </main>
</template>

<style scoped>
.placeholder {
  font-family: system-ui, -apple-system, sans-serif;
  padding: 48px;
}
.health {
  color: #606266;
  word-break: break-all;
}
</style>
