// 在 frontend 目录运行 node src/views/__tests__/vpn-offline-browser-fixture.mjs。
// 仅验证真实 VPN 页面与 API 客户端的浏览器交互；夹具不连接生产服务。
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'node:path'

const GiB = 1024 ** 3
const now = new Date().toISOString()
const requests = []
const nodes = [
  { id: 3, name: '离线源节点', enabled: false, healthy: false },
  { id: 4, name: '健康目标节点', enabled: true, healthy: true }
].map(node => ({
  ...node, base_url: `https://node-${node.id}.example.invalid`, admin_username: 'fixture',
  health_error: '', personal_user_count: 1, assigned_count: node.id === 3 ? 1 : 0,
  pending_count: node.id === 4 ? 1 : 0, last_checked_at: now, created_at: now,
  traffic_multiplier: 2, traffic_quota_bytes: 160 * GiB, traffic_used_bytes: 6 * GiB,
  traffic_remaining_bytes: 154 * GiB, allocation_quota_bytes: 80 * GiB,
  allocation_available_bytes: 0, allocation_ratio: 1, traffic_accounting_status: 'ok',
  traffic_sampled_at: now, traffic_period_end: '2099-10-21T16:00:00Z'
}))
let subscription = {
  id: 8, user_id: 17, user_email: 'fixture@example.invalid', server_id: 3, server_name: nodes[0].name,
  remote_username: 'fixture_user', group_id: 1, group_name: '默认组', group_quota_bytes: 80 * GiB,
  status: 'active', apply_status: 'pending', access_state: 'unknown', operation_status: 'pending',
  source_revocation_pending: false, offline_migration_available: true,
  quota_bytes: 80 * GiB, used_bytes: 3 * GiB, upload_bytes: GiB, download_bytes: 2 * GiB,
  remaining_bytes: 77 * GiB, period_start: now, period_end: '2099-10-21T16:00:00Z',
  next_reset_at: '2099-10-21T16:00:00Z', sampled_at: now, synced_at: now,
  accounting_status: 'ok', last_error: '源节点不可用，等待撤销旧凭据', created_at: now,
  subscription_urls: null
}

const html = `<!doctype html><html lang="zh"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>VPN 离线迁移本地夹具</title></head><body><div id="app"></div><script type="module">
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { i18n, loadLocaleMessages } from '/src/i18n/index.ts'
import View from '/src/views/admin/VpnView.vue'
import '/src/style.css'
await loadLocaleMessages('zh')
i18n.global.locale.value = 'zh'
createApp(View).use(createPinia()).use(i18n).mount('#app')
</script></body></html>`

const server = await createServer({
  configFile: false,
  root: process.cwd(),
  server: { host: '127.0.0.1', port: 4317, strictPort: true },
  resolve: { alias: { '@': resolve('src'), 'vue-i18n': 'vue-i18n/dist/vue-i18n.runtime.esm-bundler.js' } },
  define: { __INTLIFY_JIT_COMPILATION__: true, 'import.meta.env.VITE_API_BASE_URL': JSON.stringify('/api/v1') },
  plugins: [{
    name: 'vpn-offline-browser-fixture', enforce: 'pre',
    transform(source, id) {
      if (id.endsWith('/components/layout/AppLayout.vue')) return '<template><main class="mx-auto max-w-6xl p-6"><p class="mb-4 text-amber-700">本地交互夹具：无生产数据</p><slot /></main></template>'
      return source
    },
    configureServer(vite) {
      vite.middlewares.use(async (req, res, next) => {
        const url = new URL(req.url, 'http://127.0.0.1')
        const path = url.pathname
        if (path === '/') {
          res.setHeader('Content-Type', 'text/html; charset=utf-8')
          res.end(await vite.transformIndexHtml('/', html))
          return
        }
        const send = data => { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ code: 0, data })) }
        if (path === '/__vpn-smoke/requests') { send(requests); return }
        if (!path.startsWith('/api/')) { next(); return }
        if (path.endsWith('/servers')) { send(nodes); return }
        if (path.endsWith('/groups')) { send([{ id: 1, name: '默认组', quota_bytes: 80 * GiB, is_default: true, member_count: 1, subscription_count: 1, pending_count: 0, failed_count: 0 }]); return }
        if (path.endsWith('/summary')) { send({ total: 1, active: 1, disabled: 0, limited: 0, pending: subscription.apply_status === 'pending' ? 1 : 0, failed: 0, used_bytes: 3 * GiB, quota_bytes: 80 * GiB, healthy_servers: 1, total_servers: 2 }); return }
        if (path.endsWith('/subscriptions')) { send({ items: [subscription], total: 1, page: 1, page_size: 20 }); return }
        if (path.endsWith('/traffic/daily')) {
          const userTraffic = Number(url.searchParams.get('user_id')) > 0
          const used = (userTraffic ? 3 : 6) * GiB
          send({ accounting_basis: userTraffic ? 'user_proxy' : 'node_transfer', timezone: 'Asia/Shanghai', start_date: url.searchParams.get('start_date'), end_date: url.searchParams.get('end_date'), days: [{ date: now.slice(0, 10), used_bytes: used }], total_bytes: used, available_from: now, synced_at: now, partial: false, unavailable_servers: 0 })
          return
        }
        if (path === '/api/v1/admin/vpn/subscriptions/8/revoke' && req.method === 'POST') {
          let raw = ''
          for await (const chunk of req) raw += chunk
          const body = JSON.parse(raw)
          requests.push({ method: req.method, path, body })
          if (body.target_server_id !== 4 || body.allow_offline_migration !== true || requests.length !== 1) {
            res.statusCode = 409
            res.end(JSON.stringify({ code: 409, message: '夹具拒绝未确认、错误目标或重复请求' }))
            return
          }
          await new Promise(done => setTimeout(done, 1200))
          subscription = { ...subscription, server_id: 4, server_name: nodes[1].name, apply_status: 'applied', access_state: 'allowed', operation_status: 'succeeded', offline_migration_available: false, source_revocation_pending: true, last_error: '' }
          send(subscription)
          return
        }
        res.statusCode = 404
        res.end(JSON.stringify({ code: 404, message: '夹具未提供此接口' }))
      })
    }
  }, vue()]
})
await server.listen()
console.log('VPN 本地交互夹具：http://127.0.0.1:4317/')
