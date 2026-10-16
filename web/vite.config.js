import { existsSync } from 'node:fs'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// 构建产物直接落在 internal/web/dist 下：go:embed 只能引用包目录内的文件，
// 所以前端不再往 web/dist 里打一份、再拷一次 —— 少一步就少一处漏拷。
const outDir = path.resolve(
  fileURLToPath(new URL('.', import.meta.url)),
  '../internal/web/dist',
)

// embedPlaceholder 在构建结束后把 .gitkeep 放回 dist。
//
// 为什么必须有这个文件：`//go:embed all:dist` 在目录为空时是编译错误
// （"cannot embed directory dist: contains no embeddable files"），
// 目录不存在时同样编不过 —— 都不是运行时能兜住的情况，`web.go` 里那句
// exists 判断救不了尚未构建的仓库，能救的只有这个占位文件。
// 而 dist 的内容本身被 .gitignore 排除（只留 !.gitkeep），所以刚 clone 下来、
// 还没装 Node 也没跑过构建的那份仓库，全靠这个占位文件才能 `go build`。
//
// emptyOutDir 每次构建都会清空整个目录，连带把这个占位文件删掉 ——
// 于是开发者一次 git commit 就可能把「无前端也能编译」这条性质弄丢，
// 而症状出现在别人机器上。所以在构建末尾无条件补回来。
function embedPlaceholder() {
  return {
    name: 'xuliu-embed-placeholder',
    apply: 'build',
    async closeBundle() {
      if (!existsSync(outDir)) {
        await mkdir(outDir, { recursive: true })
      }
      await writeFile(
        path.join(outDir, '.gitkeep'),
        '本文件是 go:embed 的占位符，不是遗留物，请勿删除。\n' +
          '\n' +
          'internal/web/dist 的内容由 web/ 下的 npm run build 生成，不入库；\n' +
          '而 //go:embed all:dist 要求这个目录存在且非空 —— 没有它，\n' +
          '一份尚未构建前端的 clone 连 go build 都过不去。\n',
      )
    },
  }
}

export default defineConfig({
  plugins: [vue(), embedPlaceholder()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir,
    emptyOutDir: true,
    // 单文件体积告警阈值调高：Element Plus 整包就在 1MB 上下，
    // 默认 500KB 会让每次构建都刷一条没有行动价值的警告。
    chunkSizeWarningLimit: 1500,
  },
  server: {
    port: 5173,
    // 开发期把 /api 代理到后端，前后端同源，省掉 CORS 与预检。
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
})
