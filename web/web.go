package web

import "embed"

// DistFS 内嵌前端构建产物（web/dist），构建链先执行 pnpm build 再 go build。
// dist 仅含 .gitkeep 时也可编译，运行时返回"前端资源未构建"提示。
//
//go:embed all:dist
var DistFS embed.FS
