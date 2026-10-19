<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getToken } from './api'
import LoginPage from './components/LoginPage.vue'
import ProxyDashboard from './components/ProxyDashboard.vue'

const authed = ref(!!getToken())

onMounted(() => {
  window.addEventListener('auth-expired', () => {
    authed.value = false
  })
})
</script>

<template>
  <LoginPage v-if="!authed" @success="authed = true" />
  <ProxyDashboard v-else @logout="authed = false" />
</template>
